package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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

	// A script can log in a loop or test in a loop, and nothing in goja bounds
	// what it keeps: the run holds what it wrote until the script ends. These cap
	// what the backend keeps (not the JS heap the script itself uses) — the
	// oldest lines stay, the rest is dropped and the page is told why.
	scriptMaxLogs      = 1000
	scriptMaxTests     = 1000
	scriptMaxTextBytes = 64 << 10
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
	// What the limits above dropped, so the outcome can say so.
	droppedLogs  int
	droppedTests int
}

// Log keeps one console line the script wrote, within the retention limits.
func (run *scriptRun) Log(level, text string, at int64) {
	if len(run.logs) >= scriptMaxLogs {
		run.droppedLogs++
		return
	}
	run.logs = append(run.logs, ScriptLog{Level: level, Text: scriptText(text), At: at})
}

// Test keeps one nd.test result the script recorded, within the same limits.
func (run *scriptRun) Test(name string, pass bool, errText string) {
	if len(run.tests) >= scriptMaxTests {
		run.droppedTests++
		return
	}
	run.tests = append(run.tests, ScriptTest{Name: scriptText(name), Pass: pass, Error: scriptText(errText)})
}

// scriptText caps one line of what a script reported. The cut follows the byte
// limit and then steps back to a rune boundary, so the page never gets half a
// character.
func scriptText(text string) string {
	if len(text) <= scriptMaxTextBytes {
		return text
	}
	cut := scriptMaxTextBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// runPreScript runs code against req and vars, then writes the script's edits
// back into req. The outcome is never nil: a script that fails halfway still
// reports the console lines and tests it produced, with the variables as they
// came in.
func runPreScript(ctx context.Context, code string, req *APIRequest, vars map[string]string) (*scriptOutcome, error) {
	return runPreScriptWithin(ctx, code, req, vars, scriptTimeout)
}

// runPreScriptWithin is runPreScript with the caller's own deadline: a composer
// call decides how long an entry's own script may take, while a hook keeps
// scriptTimeout.
func runPreScriptWithin(ctx context.Context, code string, req *APIRequest, vars map[string]string, timeout time.Duration) (out *scriptOutcome, err error) {
	out = &scriptOutcome{vars: vars}
	var run *scriptRun
	defer func() {
		if r := recover(); r != nil {
			if run != nil {
				out = run.outcome(vars)
			}
			err = scriptFailureWithin(preScriptKind, panicError(r), timeout)
		}
	}()

	run = newScriptRun(preScriptKind, req, nil, vars)
	if runErr := run.executeWithin(ctx, code, timeout); runErr != nil {
		return run.outcome(vars), runErr
	}
	run.applyRequest(req)
	return run.outcome(run.exportedVars()), nil
}

// runPostScript runs code against the request and the response it produced.
// Nothing it does reaches the request: a post script is a read-only view.
func runPostScript(ctx context.Context, code string, req *APIRequest, resp *APIResponse, vars map[string]string) (*scriptOutcome, error) {
	return runPostScriptWithin(ctx, code, req, resp, vars, scriptTimeout)
}

// runPostScriptWithin is runPostScript with the caller's own deadline.
func runPostScriptWithin(ctx context.Context, code string, req *APIRequest, resp *APIResponse, vars map[string]string, timeout time.Duration) (out *scriptOutcome, err error) {
	out = &scriptOutcome{vars: vars}
	var run *scriptRun
	defer func() {
		if r := recover(); r != nil {
			if run != nil {
				out = run.outcome(vars)
			}
			err = scriptFailureWithin(postScriptKind, panicError(r), timeout)
		}
	}()

	run = newScriptRun(postScriptKind, req, resp, vars)
	if runErr := run.executeWithin(ctx, code, timeout); runErr != nil {
		return run.outcome(vars), runErr
	}
	return run.outcome(run.exportedVars()), nil
}

// newScriptRun builds a fresh runtime with nd and console injected. Nothing is
// shared between runs: a new goja runtime per script is the isolation.
func newScriptRun(kind string, req *APIRequest, resp *APIResponse, vars map[string]string) *scriptRun {
	run := newScriptRunBase(kind, vars)
	if req != nil {
		run.setND("request", run.api.RequestGlobal(scriptRequest(req)))
	}
	if resp != nil {
		run.setND("response", run.api.ResponseGlobal(scriptResponse(resp)))
	}
	return run
}

// newScriptRunBase builds the runtime every hook starts from: nd with the
// shared helpers a script reaches for whatever it runs for. A hook attaches the
// views it adds (nd.request, nd.response, nd.message) on top.
func newScriptRunBase(kind string, vars map[string]string) *scriptRun {
	return newScriptRunReport(kind, vars, nil)
}

// newScriptRunReport builds the same runtime with a recorder of the caller's
// choosing: a composer run passes each line on as it happens, so it keeps the
// bookkeeping itself instead of leaving it to the run. A nil report records into
// the run, like a hook.
func newScriptRunReport(kind string, vars map[string]string, report ndapi.Recorder) *scriptRun {
	vm := goja.New()
	run := &scriptRun{vm: vm, kind: kind}
	if report == nil {
		report = run
	}
	run.api = ndapi.NewRun(vm, vars, report)

	nd := vm.NewObject()
	_ = nd.Set("vars", run.api.VarsGlobal())
	_ = nd.Set("test", run.api.TestGlobal())
	_ = nd.Set("assert", run.api.AssertGlobal())
	_ = nd.Set("crypto", run.api.CryptoGlobal())
	_ = nd.Set("time", run.api.TimeGlobal())

	_ = vm.Set("nd", nd)
	_ = vm.Set("console", run.api.ConsoleGlobal())
	return run
}

// setND adds one more view to the nd object the run injected.
func (run *scriptRun) setND(key string, value goja.Value) {
	nd, ok := run.vm.Get("nd").(*goja.Object)
	if !ok {
		return
	}
	_ = nd.Set(key, value)
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
func (run *scriptRun) execute(ctx context.Context, code string) error {
	return run.executeWithin(ctx, code, scriptTimeout)
}

// executeWithin is execute with the run's own deadline: a hook gets
// scriptTimeout, while a composer run legitimately waits on the requests it
// calls and gets however long the page allowed.
func (run *scriptRun) executeWithin(ctx context.Context, code string, timeout time.Duration) (err error) {
	vm := run.vm

	// One deadline for both ways a run can stop: the vm interrupt below covers
	// code that stays in JS, and the same context reaches the Go-side calls
	// (nd.crypto.pbkdf2) that an interrupt cannot touch.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run.api.BindContext(runCtx)

	stop := make(chan struct{})
	watchdone := make(chan struct{})
	go func() {
		defer close(watchdone)
		select {
		case <-runCtx.Done():
			// A deadline means the script took too long; the parent ending means
			// the page cancelled it. The message differs, so tell them apart.
			if ctx.Err() != nil {
				vm.Interrupt(scriptCancelReason)
			} else {
				vm.Interrupt(scriptTimeoutReason)
			}
		case <-stop:
		}
	}()

	defer func() {
		close(stop)
		<-watchdone
		vm.ClearInterrupt()
	}()
	defer func() {
		if r := recover(); r != nil {
			err = scriptFailureWithin(run.kind, panicError(r), timeout)
		}
	}()

	if _, runErr := vm.RunString(code); runErr != nil {
		return scriptFailureWithin(run.kind, runErr, timeout)
	}
	return nil
}

// outcome pairs the run's logs and tests with a variable set.
func (run *scriptRun) outcome(vars map[string]string) *scriptOutcome {
	return &scriptOutcome{vars: vars, logs: run.limitedLogs(), tests: run.tests}
}

// limitedLogs returns the console lines, followed by one warning when a limit
// dropped something: the page then says why output stops where it does instead
// of just ending early. The note is a console line rather than a fake nd.test
// result, which would read as a failed test.
func (run *scriptRun) limitedLogs() []ScriptLog {
	if run.droppedLogs == 0 && run.droppedTests == 0 {
		return run.logs
	}
	logs := make([]ScriptLog, len(run.logs), len(run.logs)+1)
	copy(logs, run.logs)
	return append(logs, ScriptLog{
		Level: "warn",
		Text:  overrunNotes(run.droppedLogs, run.droppedTests),
		At:    time.Now().UnixMilli(),
	})
}

// overrunNotes says what the retention limits dropped, so the page says why
// output stops where it does instead of just ending early.
func overrunNotes(droppedLogs, droppedTests int) string {
	notes := make([]string, 0, 2)
	if droppedLogs > 0 {
		notes = append(notes, fmt.Sprintf("console 输出超过 %d 条，其余 %d 条未保留", scriptMaxLogs, droppedLogs))
	}
	if droppedTests > 0 {
		notes = append(notes, fmt.Sprintf("nd.test 结果超过 %d 条，其余 %d 条未保留", scriptMaxTests, droppedTests))
	}
	return strings.Join(notes, "；")
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
	return scriptFailureWithin(kind, err, scriptTimeout)
}

// scriptFailureWithin is scriptFailure for a run whose deadline is not the
// hook's scriptTimeout: the message names the deadline it actually missed.
func scriptFailureWithin(kind string, err error, timeout time.Duration) error {
	// A Go-side call that stopped because the run ended reports the same two
	// messages a vm interrupt does; the run is over either way.
	var aborted *ndapi.AbortError
	if errors.As(err, &aborted) {
		if aborted.Canceled {
			return fmt.Errorf("%s已取消", kind)
		}
		return fmt.Errorf("%s超时：超过 %s 未执行完，已中断", kind, timeout)
	}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if reason, ok := interrupted.Value().(string); ok && reason == scriptTimeoutReason {
			return fmt.Errorf("%s超时：超过 %s 未执行完，已中断", kind, timeout)
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
