package ndapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Recorder is where a run puts what its script produced: the lines console.*
// wrote and the nd.test results, both in the order they happened. The app
// decides what to keep, so this package carries no report type of its own.
type Recorder interface {
	// Log records one console line, at in Unix milliseconds.
	Log(level, text string, at int64)
	// Test records one nd.test result; errText is empty when it passed.
	Test(name string, pass bool, errText string)
}

// Run is the ND API of one live goja run: the objects it injected, and where
// what the script recorded through them goes. Nothing is shared between runs;
// the caller owns the runtime and its lifetime.
type Run struct {
	vm      *goja.Runtime
	ctx     context.Context // the run's lifetime, for calls Go runs on its own
	body    string          // nd.request.body as injected, to tell an edit from the original
	vars    *goja.Object
	request *goja.Object
	report  Recorder
}

// NewRun builds the ND API state of one run on vm: nd.vars holds vars as it came
// in, and everything the script records goes to report. The run is unbounded
// until BindContext gives it a lifetime.
func NewRun(vm *goja.Runtime, vars map[string]string, report Recorder) *Run {
	run := &Run{vm: vm, report: report, vars: vm.NewObject(), ctx: context.Background()}
	for k, v := range vars {
		_ = run.vars.Set(k, v)
	}
	return run
}

// BindContext gives the run its lifetime. A JS interrupt only reaches the
// runtime when control is back in JS, so a call that burns its time inside Go
// (nd.crypto.pbkdf2) has to look at this context itself instead of waiting for
// one. Call it before running the script, from the goroutine that owns vm.
func (run *Run) BindContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	run.ctx = ctx
}

// AbortError is what such a call raises when the run's context ended before it
// finished: the page cancelled the run, or it ran past its deadline. The app
// turns it into the message it already shows for an interrupted run.
type AbortError struct {
	Canceled bool // cancelled by the page, rather than out of time
}

func (e *AbortError) Error() string {
	if e.Canceled {
		return "the run was cancelled"
	}
	return "the run timed out"
}

// abort reports why run.ctx ended, as the error a long call raises.
func (run *Run) abort() *AbortError {
	return &AbortError{Canceled: errors.Is(run.ctx.Err(), context.Canceled)}
}

// ExportedVars reads the variable object back into a plain map: whatever the
// script left in it is the set the request goes on with.
func (run *Run) ExportedVars() map[string]string {
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

// RequestObject is nd.request as injected, so the caller can read a pre script's
// edits back into the request it sent.
func (run *Run) RequestObject() *goja.Object { return run.request }

// OriginalBody is nd.request.body as injected, so an edit reads differently from
// the original text.
func (run *Run) OriginalBody() string { return run.body }

// VarsGlobal is nd.vars: a plain JS object behind accessors, so a script reads
// and writes variables without a live bridge to Go.
func (run *Run) VarsGlobal() *goja.Object {
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

// RequestGlobal is nd.request: the request as the script sees it.
func (run *Run) RequestGlobal(req *Request) *goja.Object {
	vm := run.vm
	run.request = vm.NewObject()
	run.body = req.Body

	_ = run.request.Set("method", req.Method)
	_ = run.request.Set("url", req.URL)
	_ = run.request.Set("headers", flatObject(vm, req.Headers))
	_ = run.request.Set("query", flatObject(vm, req.Query))
	_ = run.request.Set("body", run.body)
	return run.request
}

// ResponseGlobal is nd.response: read-only, plus json() for a parsed body.
func (run *Run) ResponseGlobal(resp *Response) *goja.Object {
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
		call, ok := goja.AssertFunction(parse)
		if !ok {
			panic(run.newError("nd.response.json(): JSON.parse is not available"))
		}
		// 只解析一次，而且就在 JS 里解析：结果是真正的 JS 值（数组有 map 等方法），
		// 也不必先在 Go 里过一遍 JSON 只为拼错误消息。
		value, err := call(goja.Undefined(), vm.ToValue(body))
		if err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(interrupted) // 中断要走中断路径，别包成普通 Error
			}
			panic(run.newError("nd.response.json(): " + ScriptErrorText(err)))
		}
		return value
	})
	return obj
}

// TestGlobal is nd.test(name, fn): each call records one result, and a failed
// test does not stop the script.
func (run *Run) TestGlobal() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			run.report.Test(name, false, "nd.test(name, fn) needs a function")
			return goja.Undefined()
		}
		if _, err := fn(goja.Undefined()); err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(interrupted) // a fired interrupt ends the script, tests included
			}
			run.report.Test(name, false, ScriptErrorText(err))
			return goja.Undefined()
		}
		run.report.Test(name, true, "")
		return goja.Undefined()
	}
}

// AssertGlobal is nd.assert(cond, msg): a falsy condition throws msg.
func (run *Run) AssertGlobal() func(goja.FunctionCall) goja.Value {
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

// ConsoleGlobal is console.log/warn/error: arguments serialized, joined with
// spaces, kept as a log line the page shows.
func (run *Run) ConsoleGlobal() *goja.Object {
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
			run.report.Log(level, strings.Join(parts, " "), time.Now().UnixMilli())
			return goja.Undefined()
		}
	}

	_ = obj.Set("log", logAt("log"))
	_ = obj.Set("warn", logAt("warn"))
	_ = obj.Set("error", logAt("error"))
	return obj
}

// newError makes a JS Error carrying msg, so the page and the script's own
// try/catch both see the message as written.
func (run *Run) newError(msg string) goja.Value {
	if obj, err := run.vm.New(run.vm.Get("Error"), run.vm.ToValue(msg)); err == nil {
		return obj
	}
	return run.vm.NewTypeError(msg)
}

// ScriptErrorText is an exception's message, without the Go wrapping. The caller
// needs it to report a failed run the way a failing nd.test does.
func ScriptErrorText(err error) string {
	var thrown *goja.Exception
	if errors.As(err, &thrown) {
		return strings.TrimSpace(thrown.Error())
	}
	return strings.TrimSpace(err.Error())
}

// flatObject turns the pairs of list into a plain JS object; a later pair with
// the same key wins over an earlier one.
func flatObject(vm *goja.Runtime, list []Pair) *goja.Object {
	obj := vm.NewObject()
	for _, kv := range list {
		if kv.Key == "" {
			continue
		}
		_ = obj.Set(kv.Key, kv.Value)
	}
	return obj
}

// valueText stringifies a value a script stores in a variable.
func valueText(v goja.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}
