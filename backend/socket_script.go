package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"netdream/backend/ndapi"
)

// 连接脚本的三个钩子，以及它们出错时报的固定短语（与 HTTP 的「预执行脚本」一致）。
const (
	socketPhasePre     = "pre"
	socketPhaseSend    = "send"
	socketPhaseReceive = "receive"

	socketPreScriptKind     = "预连接脚本"
	socketSendScriptKind    = "发送脚本"
	socketReceiveScriptKind = "接收脚本"
)

// socketScriptHost is one connection's scripting: the three hooks, the variable
// snapshot they share, and where their reports go. Runs on one connection are
// serialized, so a variable one hook sets is there for the next. The host lives
// exactly as long as Connect.
type socketScriptHost struct {
	pre     string
	send    string
	receive string

	// emit pushes one event to the page. A channel that is already closed only
	// drops the report: scripting never ends the connection's read loop.
	emit   func(SocketEvent) error
	ctx    context.Context
	cancel context.CancelFunc

	// mu serializes the runs of one connection: two at once would each start
	// from a variable snapshot the other is about to replace.
	mu   sync.Mutex
	vars map[string]string
}

// newSocketScriptHost builds a connection's scripting state from what the page
// asked for. The context is the call's, so closing the page ends a run with it.
func newSocketScriptHost(ctx context.Context, params SocketConnectParams, emit func(SocketEvent) error) *socketScriptHost {
	if ctx == nil {
		ctx = context.Background()
	}
	hostCtx, cancel := context.WithCancel(ctx)
	return &socketScriptHost{
		pre:     params.PreScript,
		send:    params.SendScript,
		receive: params.ReceiveScript,
		emit:    emit,
		ctx:     hostCtx,
		cancel:  cancel,
		vars:    copyVars(params.Vars),
	}
}

// scripts reports whether the connection has any hook to run at all.
func (h *socketScriptHost) scripts() bool {
	return h != nil && (h.pre != "" || h.send != "" || h.receive != "")
}

// socketScripts is the live hosts by connection id. It is the scripting side of
// the connection registry: the protocol package has its own, and this one is
// registered and removed around the same Connect call.
var socketScripts = struct {
	sync.Mutex
	byID map[string]*socketScriptHost
}{byID: map[string]*socketScriptHost{}}

// registerSocketScripts takes id's scripts for as long as Connect runs. A
// connection with no script registers nothing: scripting costs nothing until
// the page writes a script. An id already in use keeps the host it has, so a
// second Connect cannot take over a live one.
func registerSocketScripts(ctx context.Context, params SocketConnectParams, emit func(SocketEvent) error) *socketScriptHost {
	id := strings.TrimSpace(params.ID)
	if id == "" || emit == nil {
		return nil
	}
	host := newSocketScriptHost(ctx, params, emit)
	if !host.scripts() {
		host.cancel()
		return nil
	}

	socketScripts.Lock()
	defer socketScripts.Unlock()
	if _, taken := socketScripts.byID[id]; taken {
		host.cancel()
		return nil
	}
	socketScripts.byID[id] = host
	return host
}

// unregisterSocketScripts forgets host unless its id has come to name another
// one, and ends any run still going: nothing outlives the connection.
func unregisterSocketScripts(id string, host *socketScriptHost) {
	if host == nil {
		return
	}
	socketScripts.Lock()
	if socketScripts.byID[strings.TrimSpace(id)] == host {
		delete(socketScripts.byID, strings.TrimSpace(id))
	}
	socketScripts.Unlock()
	host.cancel()
}

// lookupSocketScripts finds the scripts of the connection id names; nil when it
// has none or is already gone.
func lookupSocketScripts(id string) *socketScriptHost {
	socketScripts.Lock()
	defer socketScripts.Unlock()
	return socketScripts.byID[strings.TrimSpace(id)]
}

// preConnect runs the pre-connection script and writes its edits back into
// params. A failing script is the caller's error: the connection is not dialed.
func (h *socketScriptHost) preConnect(params *SocketConnectParams) error {
	if h == nil || h.pre == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	run := newScriptRunBase(socketPreScriptKind, h.vars)
	// nd.request 是连接视图：有 url/headers/query，没有 HTTP 的 method/body。
	run.setND("request", run.api.RequestGlobal(&ndapi.Request{
		URL:     params.URL,
		Headers: scriptPairs(params.Headers),
		Query:   scriptPairs(params.Params),
	}))
	out, err := h.execute(run, h.pre)
	if err == nil {
		applySocketRequest(params, run)
	}
	h.finish(socketPhasePre, out, err)
	return err
}

// beforeSend runs the send hook over one outgoing message and writes a
// rewritten payload back into params. A failing script is the caller's error:
// that message is not sent.
func (h *socketScriptHost) beforeSend(params *SocketSendParams) error {
	if h == nil || h.send == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	run := newScriptRunBase(socketSendScriptKind, h.vars)
	run.setND("message", run.api.MessageGlobal(&ndapi.Message{
		Payload:  params.Payload,
		IsBinary: params.IsBinary,
		Event:    params.Event,
	}, true))
	out, err := h.execute(run, h.send)
	if err == nil {
		if payload, changed := run.api.EditedPayload(); changed {
			params.Payload = payload
		}
	}
	h.finish(socketPhaseSend, out, err)
	return err
}

// received runs the receive hook over a message the page already has. Nothing it
// does changes the message, and nothing it does stops the connection: a failing
// script only reports itself.
func (h *socketScriptHost) received(ev SocketEvent) {
	if h == nil || h.receive == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	run := newScriptRunBase(socketReceiveScriptKind, h.vars)
	run.setND("message", run.api.MessageGlobal(&ndapi.Message{
		Payload:  ev.Payload,
		IsBinary: ev.IsBinary,
		Event:    ev.Event,
	}, false))
	out, err := h.execute(run, h.receive)
	h.finish(socketPhaseReceive, out, err)
}

// execute runs one hook's code and collects what it leaves behind. Like a
// request script, a run that fails halfway still reports the console lines and
// tests it produced, with the variables as they came in.
func (h *socketScriptHost) execute(run *scriptRun, code string) (out *scriptOutcome, err error) {
	snapshot := h.vars
	out = &scriptOutcome{vars: snapshot}
	defer func() {
		if r := recover(); r != nil {
			out = run.outcome(snapshot)
			err = scriptFailure(run.kind, panicError(r))
		}
	}()

	if runErr := run.execute(h.ctx, code); runErr != nil {
		return run.outcome(snapshot), runErr
	}
	return run.outcome(run.exportedVars()), nil
}

// finish keeps the run's variables for the next one and tells the page what the
// script did. The report is the only thing a script adds to the connection's
// events: the protocol's own events reach the page exactly as before.
func (h *socketScriptHost) finish(phase string, out *scriptOutcome, err error) {
	report := SocketScriptReport{
		Phase: phase,
		Logs:  []ScriptLog{},
		Tests: []ScriptTest{},
		Vars:  map[string]string{},
	}
	if out != nil {
		if out.vars != nil {
			h.vars = out.vars
			report.Vars = out.vars
		}
		if out.logs != nil {
			report.Logs = out.logs
		}
		if out.tests != nil {
			report.Tests = out.tests
		}
	}
	if err != nil {
		report.Error = err.Error()
	}
	if h.emit == nil {
		return
	}
	// 通道已经关掉时只丢这条事件：脚本的记账照旧，protocol 的读循环不能因此停下。
	_ = h.emit(SocketEvent{Type: "script", At: time.Now().UnixMilli(), Script: &report})
}

// varsSnapshot is the variable set a script's {{var}} references expand with:
// the host's own once it exists, so a pre script's writes reach the connection.
func (h *socketScriptHost) varsSnapshot(fallback map[string]string) map[string]string {
	if h == nil {
		return fallback
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vars
}

// applySocketRequest writes what the pre script left in nd.request back into the
// connection: url, headers and query. There is no method or body to write back,
// so those fields are read and ignored.
func applySocketRequest(params *SocketConnectParams, run *scriptRun) {
	request := run.api.RequestObject()
	if request == nil {
		return
	}
	// 脚本可以给字段装上会抛错的 getter，读回时跟运行脚本一样只当这次运行失败。
	run.vm.Try(func() {
		if target := stringField(request.Get("url")); target != "" {
			params.URL = target
		}
		if headers, ok := request.Get("headers").(*goja.Object); ok {
			params.Headers = mergeKeyValues(params.Headers, headers)
		}
		if query, ok := request.Get("query").(*goja.Object); ok {
			params.Params = mergeKeyValues(params.Params, query)
		}
	})
}

// expandSocketVars substitutes {{name}} into the fields a socket connection can
// carry one in: its URL, query parameters and headers. A TCP connection ignores
// the last two, but expanding them keeps the connection in step with the page.
func expandSocketVars(params *SocketConnectParams, vars map[string]string) {
	if len(vars) == 0 {
		return
	}
	params.URL = expandVars(params.URL, vars)
	for i := range params.Params {
		params.Params[i].Key = expandVars(params.Params[i].Key, vars)
		params.Params[i].Value = expandVars(params.Params[i].Value, vars)
	}
	for i := range params.Headers {
		params.Headers[i].Key = expandVars(params.Headers[i].Key, vars)
		params.Headers[i].Value = expandVars(params.Headers[i].Value, vars)
	}
}

// copyVars is a snapshot the host owns: runs write into it, never into the map
// the page sent.
func copyVars(vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
