package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
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

// scriptRun is one live goja run. The objects it injected stay reachable, so a
// pre script's edits can be read back into the request afterwards.
type scriptRun struct {
	vm      *goja.Runtime
	kind    string
	body    string // nd.request.body as injected, to tell an edit from the original
	logs    []ScriptLog
	tests   []ScriptTest
	vars    *goja.Object
	request *goja.Object
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
	run := &scriptRun{vm: vm, kind: kind, vars: vm.NewObject()}
	for k, v := range vars {
		_ = run.vars.Set(k, v)
	}

	nd := vm.NewObject()
	_ = nd.Set("vars", run.varsGlobal())
	_ = nd.Set("request", run.requestGlobal(req))
	if resp != nil {
		_ = nd.Set("response", run.responseGlobal(resp))
	}
	_ = nd.Set("test", run.testGlobal())
	_ = nd.Set("assert", run.assertGlobal())

	_ = vm.Set("nd", nd)
	_ = vm.Set("console", run.consoleGlobal())
	return run
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

// exportedVars reads the variable object back into a plain map: whatever the
// script left in it is the set the request goes on with.
func (run *scriptRun) exportedVars() map[string]string {
	out := make(map[string]string, len(run.vars.Keys()))
	run.vm.Try(func() {
		for _, key := range run.vars.Keys() {
			if value := run.vars.Get(key); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
				out[key] = value.String()
			}
		}
	})
	return out
}

// varsGlobal is nd.vars: a plain JS object behind accessors, so a script reads
// and writes variables without a live bridge to Go.
func (run *scriptRun) varsGlobal() *goja.Object {
	vm := run.vm
	store := run.vars
	api := vm.NewObject()

	_ = api.Set("get", func(call goja.FunctionCall) goja.Value {
		value := store.Get(call.Argument(0).String())
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			return goja.Null()
		}
		return value
	})
	_ = api.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		if key == "" {
			return goja.Undefined()
		}
		_ = store.Set(key, valueText(call.Argument(1)))
		return goja.Undefined()
	})
	_ = api.Set("unset", func(call goja.FunctionCall) goja.Value {
		_ = store.Delete(call.Argument(0).String())
		return goja.Undefined()
	})
	_ = api.Set("has", func(call goja.FunctionCall) goja.Value {
		value := store.Get(call.Argument(0).String())
		return vm.ToValue(value != nil && !goja.IsUndefined(value) && !goja.IsNull(value))
	})
	_ = api.Set("toObject", func(goja.FunctionCall) goja.Value {
		out := vm.NewObject()
		for _, key := range store.Keys() {
			_ = out.Set(key, store.Get(key))
		}
		return out
	})
	return api
}

// requestGlobal is nd.request: the request as the script sees it.
func (run *scriptRun) requestGlobal(req *APIRequest) *goja.Object {
	vm := run.vm
	run.request = vm.NewObject()
	run.body = initialBody(req)

	_ = run.request.Set("method", req.Method)
	_ = run.request.Set("url", req.URL)
	_ = run.request.Set("headers", flatObject(vm, req.Headers))
	_ = run.request.Set("query", flatObject(vm, req.Params))
	_ = run.request.Set("body", run.body)
	return run.request
}

// responseGlobal is nd.response: read-only, plus json() for a parsed body.
func (run *scriptRun) responseGlobal(resp *APIResponse) *goja.Object {
	vm := run.vm
	obj := vm.NewObject()

	_ = obj.Set("status", resp.Status)
	_ = obj.Set("statusText", resp.StatusText)
	_ = obj.Set("headers", flatObject(vm, resp.Headers))
	_ = obj.Set("body", resp.Body)
	_ = obj.Set("timeMS", resp.TimeMS)
	_ = obj.Set("sizeBytes", resp.SizeBytes)

	parse := vm.Get("JSON").ToObject(vm).Get("parse")
	body := resp.Body
	_ = obj.Set("json", func(goja.FunctionCall) goja.Value {
		// Go parses first only for the message: JSON.parse then hands back
		// real JS values, so arrays keep their methods.
		var scratch any
		if err := json.Unmarshal([]byte(body), &scratch); err != nil {
			panic(run.newError("nd.response.json(): " + err.Error()))
		}
		call, ok := goja.AssertFunction(parse)
		if !ok {
			return vm.ToValue(scratch)
		}
		value, err := call(goja.Undefined(), vm.ToValue(body))
		if err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(interrupted)
			}
			panic(run.newError("nd.response.json(): " + scriptErrorText(err)))
		}
		return value
	})
	return obj
}

// testGlobal is nd.test(name, fn): each call records one result, and a failed
// test does not stop the script.
func (run *scriptRun) testGlobal() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			run.tests = append(run.tests, ScriptTest{Name: name, Error: "nd.test(name, fn) needs a function"})
			return goja.Undefined()
		}
		if _, err := fn(goja.Undefined()); err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(interrupted) // a fired interrupt ends the script, tests included
			}
			run.tests = append(run.tests, ScriptTest{Name: name, Error: scriptErrorText(err)})
			return goja.Undefined()
		}
		run.tests = append(run.tests, ScriptTest{Name: name, Pass: true})
		return goja.Undefined()
	}
}

// assertGlobal is nd.assert(cond, msg): a falsy condition throws msg.
func (run *scriptRun) assertGlobal() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if call.Argument(0).ToBoolean() {
			return goja.Undefined()
		}
		msg := "assertion failed"
		if arg := call.Argument(1); arg != nil && !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			if text := arg.String(); text != "" {
				msg = text
			}
		}
		panic(run.newError(msg))
	}
}

// consoleGlobal is console.log/warn/error: arguments serialized, joined with
// spaces, kept as a log line the page shows.
func (run *scriptRun) consoleGlobal() *goja.Object {
	vm := run.vm
	obj := vm.NewObject()
	stringify := vm.Get("JSON").ToObject(vm).Get("stringify")

	serialize := func(v goja.Value) string {
		if v == nil || goja.IsUndefined(v) {
			return "undefined"
		}
		if fn, ok := goja.AssertFunction(stringify); ok {
			if text, err := fn(goja.Undefined(), v); err == nil && text != nil && !goja.IsUndefined(text) && !goja.IsNull(text) {
				return text.String()
			}
		}
		var fallback string
		vm.Try(func() { fallback = fmt.Sprint(v.Export()) })
		return fallback
	}

	logAt := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, arg := range call.Arguments {
				parts = append(parts, serialize(arg))
			}
			run.logs = append(run.logs, ScriptLog{
				Level: level,
				Text:  strings.Join(parts, " "),
				At:    time.Now().UnixMilli(),
			})
			return goja.Undefined()
		}
	}

	_ = obj.Set("log", logAt("log"))
	_ = obj.Set("warn", logAt("warn"))
	_ = obj.Set("error", logAt("error"))
	return obj
}

// applyRequest writes what the pre script left in nd.request back into req.
func (run *scriptRun) applyRequest(req *APIRequest) {
	if method := stringField(run.request.Get("method")); method != "" {
		req.Method = method
	}
	if target := stringField(run.request.Get("url")); target != "" {
		req.URL = target
	}
	if headers, ok := run.request.Get("headers").(*goja.Object); ok {
		req.Headers = mergeKeyValues(req.Headers, headers)
	}
	if query, ok := run.request.Get("query").(*goja.Object); ok {
		req.Params = mergeKeyValues(req.Params, query)
	}
	if body := run.request.Get("body"); body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		if text := body.String(); text != run.body {
			req.BodyType = "raw"
			req.Raw = text
			if req.RawType == "" {
				req.RawType = "text"
			}
		}
	}
}

// newError makes a JS Error carrying msg, so the page and the script's own
// try/catch both see the message as written.
func (run *scriptRun) newError(msg string) goja.Value {
	if obj, err := run.vm.New(run.vm.Get("Error"), run.vm.ToValue(msg)); err == nil {
		return obj
	}
	return run.vm.NewTypeError(msg)
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

// flatObject turns the enabled pairs of list into a plain JS object; a later
// pair with the same key wins over an earlier one.
func flatObject(vm *goja.Runtime, list []KeyValue) *goja.Object {
	obj := vm.NewObject()
	for _, kv := range list {
		if kv.Key == "" {
			continue
		}
		_ = obj.Set(kv.Key, kv.Value)
	}
	return obj
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

// valueText stringifies a value a script stores in a variable.
func valueText(v goja.Value) string {
	if v == nil {
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
		return fmt.Errorf("%s出错：%s", kind, scriptErrorText(thrown))
	}
	return fmt.Errorf("%s出错：%v", kind, err)
}

// scriptErrorText is an exception's message, without the Go wrapping.
func scriptErrorText(err error) string {
	var thrown *goja.Exception
	if errors.As(err, &thrown) {
		return strings.TrimSpace(thrown.Error())
	}
	return strings.TrimSpace(err.Error())
}

// panicError turns a recovered panic value into an error.
func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}
