package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"

	"netdream/backend/ndapi"
)

// varPattern matches a {{name}} reference inside any string a request carries.
var varPattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

const (
	// scriptTimeout caps one script run, so a script that never returns cannot
	// wedge the backend.
	scriptTimeout = 5 * time.Second
	// What an interrupt carries, so a run can tell the timeout apart from the
	// page closing.
	scriptTimeoutReason = "script timeout"
	scriptCancelReason  = "script cancelled"
	// The name a script error is reported under.
	preScriptKind  = "预执行脚本"
	postScriptKind = "后执行脚本"
)

// scriptOutcome is what one script run leaves behind: the full variable set at
// exit, plus the console lines and test results it produced.
type scriptOutcome struct {
	vars  map[string]string
	logs  []ScriptLog
	tests []ScriptTest
}

// scriptRun is one live goja run: the runtime, what it runs for, the ND API
// state injected into it, and what the script reported through that state. The
// objects it injected stay reachable, so a pre script's edits can be read back
// into the request afterwards.
type scriptRun struct {
	vm    *goja.Runtime
	kind  string
	logs  []ScriptLog
	tests []ScriptTest
	api   *ndapi.Run
}

// Log keeps one console line the script wrote.
func (run *scriptRun) Log(level, text string, at int64) {
	run.logs = append(run.logs, ScriptLog{Level: level, Text: text, At: at})
}

// Test keeps one nd.test result the script recorded.
func (run *scriptRun) Test(name string, pass bool, errText string) {
	run.tests = append(run.tests, ScriptTest{Name: name, Pass: pass, Error: errText})
}

// runPreScript runs code against req and vars, then writes the script's edits
// back into req. The outcome is never nil: a script that fails halfway still
// reports the console lines and tests it produced, with the variables as they
// came in.
func runPreScript(ctx context.Context, code string, req *APIRequest, vars map[string]string) (out *scriptOutcome, err error) {
	out = &scriptOutcome{vars: vars}
	var run *scriptRun
	defer func() {
		if r := recover(); r != nil {
			if run != nil {
				out = run.outcome(vars)
			}
			err = scriptFailure(preScriptKind, panicError(r))
		}
	}()

	run = newScriptRun(preScriptKind, req, nil, vars)
	if runErr := run.execute(ctx, code); runErr != nil {
		return run.outcome(vars), runErr
	}
	run.applyRequest(req)
	return run.outcome(run.exportedVars()), nil
}

// runPostScript runs code against the request and the response it produced.
// Nothing it does reaches the request: a post script is a read-only view.
func runPostScript(ctx context.Context, code string, req *APIRequest, resp *APIResponse, vars map[string]string) (out *scriptOutcome, err error) {
	out = &scriptOutcome{vars: vars}
	var run *scriptRun
	defer func() {
		if r := recover(); r != nil {
			if run != nil {
				out = run.outcome(vars)
			}
			err = scriptFailure(postScriptKind, panicError(r))
		}
	}()

	run = newScriptRun(postScriptKind, req, resp, vars)
	if runErr := run.execute(ctx, code); runErr != nil {
		return run.outcome(vars), runErr
	}
	return run.outcome(run.exportedVars()), nil
}

// newScriptRun builds a fresh runtime with nd and console injected. Nothing is
// shared between runs: a new goja runtime per script is the isolation.
func newScriptRun(kind string, req *APIRequest, resp *APIResponse, vars map[string]string) *scriptRun {
	vm := goja.New()
	run := &scriptRun{vm: vm, kind: kind}
	run.api = ndapi.NewRun(vm, vars, run)

	nd := vm.NewObject()
	_ = nd.Set("vars", run.api.VarsGlobal())
	_ = nd.Set("request", run.api.RequestGlobal(scriptRequest(req)))
	if resp != nil {
		_ = nd.Set("response", run.api.ResponseGlobal(scriptResponse(resp)))
	}
	_ = nd.Set("test", run.api.TestGlobal())
	_ = nd.Set("assert", run.api.AssertGlobal())
	_ = nd.Set("crypto", run.api.CryptoGlobal())
	_ = nd.Set("time", run.api.TimeGlobal())

	_ = vm.Set("nd", nd)
	_ = vm.Set("console", run.api.ConsoleGlobal())
	return run
}

// scriptRequest is the request as a pre script sees it.
func scriptRequest(req *APIRequest) *ndapi.Request {
	return &ndapi.Request{
		Method:  req.Method,
		URL:     req.URL,
		Headers: scriptPairs(req.Headers),
		Query:   scriptPairs(req.Params),
		Body:    initialBody(req),
	}
}

// scriptResponse is the response as a post script sees it.
func scriptResponse(resp *APIResponse) *ndapi.Response {
	return &ndapi.Response{
		Status:     resp.Status,
		StatusText: resp.StatusText,
		Headers:    scriptPairs(resp.Headers),
		Body:       resp.Body,
		TimeMS:     resp.TimeMS,
		SizeBytes:  resp.SizeBytes,
	}
}

// scriptPairs hands a request's pairs to the script side, in order.
func scriptPairs(list []KeyValue) []ndapi.Pair {
	out := make([]ndapi.Pair, 0, len(list))
	for _, kv := range list {
		out = append(out, ndapi.Pair{Key: kv.Key, Value: kv.Value})
	}
	return out
}

// execute runs code with the timeout and the page's lifetime both able to stop
// it, and turns whatever the runtime throws into an error.
func (run *scriptRun) execute(ctx context.Context, code string) (err error) {
	vm := run.vm

	timer := time.AfterFunc(scriptTimeout, func() { vm.Interrupt(scriptTimeoutReason) })

	stop := make(chan struct{})
	watchdone := make(chan struct{})
	go func() {
		defer close(watchdone)
		select {
		case <-ctx.Done():
			vm.Interrupt(scriptCancelReason)
		case <-stop:
		}
	}()

	defer func() {
		timer.Stop()
		close(stop)
		<-watchdone
		vm.ClearInterrupt()
	}()
	defer func() {
		if r := recover(); r != nil {
			err = scriptFailure(run.kind, panicError(r))
		}
	}()

	if _, runErr := vm.RunString(code); runErr != nil {
		return scriptFailure(run.kind, runErr)
	}
	return nil
}

// outcome pairs the run's logs and tests with a variable set.
func (run *scriptRun) outcome(vars map[string]string) *scriptOutcome {
	return &scriptOutcome{vars: vars, logs: run.logs, tests: run.tests}
}

// exportedVars is the variable set the script left behind: whatever is in
// nd.vars at exit is the set the request goes on with.
func (run *scriptRun) exportedVars() map[string]string {
	return run.api.ExportedVars()
}

// applyRequest writes what the pre script left in nd.request back into req.
func (run *scriptRun) applyRequest(req *APIRequest) {
	request := run.api.RequestObject()
	if method := stringField(request.Get("method")); method != "" {
		req.Method = method
	}
	if target := stringField(request.Get("url")); target != "" {
		req.URL = target
	}
	if headers, ok := request.Get("headers").(*goja.Object); ok {
		req.Headers = mergeKeyValues(req.Headers, headers)
	}
	if query, ok := request.Get("query").(*goja.Object); ok {
		req.Params = mergeKeyValues(req.Params, query)
	}
	if body := request.Get("body"); body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		if text := body.String(); text != run.api.OriginalBody() {
			req.BodyType = "raw"
			req.Raw = text
			if req.RawType == "" {
				req.RawType = "text"
			}
		}
	}
}

// initialBody is the body text a script sees in nd.request.body.
func initialBody(req *APIRequest) string {
	switch req.BodyType {
	case "raw":
		return req.Raw
	case "urlencoded":
		values := url.Values{}
		for _, kv := range req.URLEncoded {
			if kv.Key == "" {
				continue
			}
			values.Add(kv.Key, kv.Value)
		}
		return values.Encode()
	default: // "none", "formData", "binary" and anything unknown
		return ""
	}
}

// mergeKeyValues diffs current against the script's object, case-insensitively:
// a pair whose key vanished (or became null/undefined) is dropped, and every
// key the object carries is added or updated in its new spelling.
func mergeKeyValues(current []KeyValue, obj *goja.Object) []KeyValue {
	out := make([]KeyValue, 0, len(current))
	index := make(map[string]int, len(current))

	for _, kv := range current {
		key := strings.ToLower(kv.Key)
		if _, dup := index[key]; dup {
			continue
		}
		value := obj.Get(kv.Key)
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		index[key] = len(out)
		out = append(out, kv)
	}

	for _, key := range obj.Keys() {
		if key == "" {
			continue
		}
		value := obj.Get(key)
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		text := value.String()
		if at, exists := index[strings.ToLower(key)]; exists {
			// The script's own spelling of the key wins over the original.
			out[at] = KeyValue{Key: key, Value: text}
			continue
		}
		index[strings.ToLower(key)] = len(out)
		out = append(out, KeyValue{Key: key, Value: text})
	}
	return out
}

// applyVars substitutes {{name}} references into every request field that can
// carry one.
func applyVars(req *APIRequest, vars map[string]string) {
	if len(vars) == 0 {
		return
	}
	req.URL = expandVars(req.URL, vars)

	for i := range req.Params {
		req.Params[i].Key = expandVars(req.Params[i].Key, vars)
		req.Params[i].Value = expandVars(req.Params[i].Value, vars)
	}
	for i := range req.Headers {
		req.Headers[i].Key = expandVars(req.Headers[i].Key, vars)
		req.Headers[i].Value = expandVars(req.Headers[i].Value, vars)
	}
	for i := range req.Cookies {
		req.Cookies[i].Key = expandVars(req.Cookies[i].Key, vars)
		req.Cookies[i].Value = expandVars(req.Cookies[i].Value, vars)
	}
	for i := range req.URLEncoded {
		req.URLEncoded[i].Key = expandVars(req.URLEncoded[i].Key, vars)
		req.URLEncoded[i].Value = expandVars(req.URLEncoded[i].Value, vars)
	}
	for i := range req.FormData {
		req.FormData[i].Value = expandVars(req.FormData[i].Value, vars)
	}

	req.Raw = expandVars(req.Raw, vars)
	req.Auth.Username = expandVars(req.Auth.Username, vars)
	req.Auth.Password = expandVars(req.Auth.Password, vars)
	req.Auth.Token = expandVars(req.Auth.Token, vars)
}

// expandVars replaces every {{name}} in s with vars[name]; a name with no
// variable is left as written.
func expandVars(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "{{") {
		return s
	}
	return varPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSpace(varPattern.FindStringSubmatch(match)[1])
		if value, ok := vars[name]; ok {
			return value
		}
		return match
	})
}

// stringField reads a script-set field as text; absent, null and undefined all
// read as empty.
func stringField(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// scriptFailure turns a goja error into the message the page shows.
func scriptFailure(kind string, err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if reason, ok := interrupted.Value().(string); ok && reason == scriptTimeoutReason {
			return fmt.Errorf("%s超时：超过 %s 未执行完，已中断", kind, scriptTimeout)
		}
		return fmt.Errorf("%s已取消", kind)
	}
	var thrown *goja.Exception
	if errors.As(err, &thrown) {
		return fmt.Errorf("%s出错：%s", kind, ndapi.ScriptErrorText(thrown))
	}
	return fmt.Errorf("%s出错：%v", kind, err)
}

// panicError turns a recovered panic value into an error.
func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}
