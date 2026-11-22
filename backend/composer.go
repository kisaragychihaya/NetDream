package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/egoist/mygo"

	"netdream/backend/ndapi"
	"netdream/backend/protocol"
)

const (
	// composerScriptKind 是 composer 脚本出错时报的固定短语（与 HTTP 的
	// 「预执行脚本」同一套写法）。
	composerScriptKind = "composer 脚本"
	// 一次运行没指定 Timeout 时的时长：composer 合法地要等它调用的请求，所以
	// 比一个钩子的 scriptTimeout 宽得多。
	composerDefaultTimeout = 60 * time.Second
)

// ComposerService runs composer scripts: one goja script that calls the
// workspace's own requests through nd.composer.run, one after another, sharing
// one variable pool. Every exported method here is bound to the frontend, so
// helpers stay unexported.
type ComposerService struct{}

// Run performs one run, pushing what happens on stream as it happens. The result
// carries the counts and — when the run did not finish normally — why; an error
// is reserved for a run that could not start at all.
func (ComposerService) Run(ctx context.Context, params ComposerRunParams, stream *mygo.Channel[ComposerEvent]) (ComposerResult, error) {
	return runComposer(ctx, params, composerEmitter(stream))
}

// Stop ends the run id names: the script is interrupted and the request it is
// waiting on is cancelled. An id that is already gone is not an error.
func (ComposerService) Stop(id string) {
	if host := lookupComposerHost(id); host != nil {
		host.cancel()
	}
}

// runComposer is Run with the page's channel already adapted, so a test can
// collect the events without a page.
func runComposer(ctx context.Context, params ComposerRunParams, emit func(ComposerEvent) error) (ComposerResult, error) {
	host, err := registerComposerHost(ctx, params, emit)
	if err != nil {
		return ComposerResult{}, err
	}
	defer unregisterComposerHost(params.ID, host)
	return host.start(), nil
}

// composerHost is one live run: the composer script's runtime, the variable pool
// the script and the requests it calls share, the connections it opened in the
// background, and where its reports go. It lives exactly as long as Run.
type composerHost struct {
	id       string
	code     string
	requests []SavedRequest
	timeout  time.Duration

	// push sends one event to the page. A channel that is already closed only
	// drops the report: the run's own bookkeeping carries on.
	push func(ComposerEvent) error
	// ctx is the run's lifetime: Stop and the page closing both cancel it, and
	// the run's deadline ends it too.
	ctx    context.Context
	cancel context.CancelFunc

	vm     *goja.Runtime
	script *scriptRun

	// reportMu serializes the run's reports, so the page sees them in one order
	// even when a background connection's goroutine reports a lifecycle line, and
	// the counters behind the retention limits stay right. Once finished, nothing
	// is pushed: done is a run's last event.
	reportMu sync.Mutex
	finished bool
	// 每个事件已经带着它发生时的行，这里只留计数：保留上限与请求脚本同一套，
	// 超限的部分丢掉并在 done 前补一条 warn。
	logs, tests               int
	passed                    int
	droppedLogs, droppedTests int

	// varsMu guards vars, the pool 被调条目的脚本与 nd.vars 共用（{{var}} 展开也读
	// 它）。眼下只有运行的 goroutine 读写它——nd.vars 本身活在 goja 运行时里，而运行
	// 时只属于一个 goroutine——但这张池子是这次运行的共享存储：锁把所有权写实，后台
	// 连接的 goroutine 与以后可能加进来的并行调用都不会踩到它。
	varsMu sync.Mutex
	vars   map[string]string

	// sockets are the connections this run opened in the background; the run
	// closes them all before it reports done, so nothing outlives it.
	sockets *composerSockets
}

// composerHosts is the live runs by id: Run registers for as long as it runs and
// Stop finds one here to cancel.
var composerHosts = struct {
	sync.Mutex
	byID map[string]*composerHost
}{byID: map[string]*composerHost{}}

// registerComposerHost takes id for one run. A second run under the same id is
// refused: Stop could not tell them apart.
func registerComposerHost(ctx context.Context, params ComposerRunParams, emit func(ComposerEvent) error) (*composerHost, error) {
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, fmt.Errorf("运行 id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	host := &composerHost{
		id:       id,
		code:     params.Script,
		requests: params.Requests,
		timeout:  composerTimeout(params.Timeout),
		push:     emit,
		ctx:      runCtx,
		cancel:   cancel,
		vars:     copyVars(params.Vars),
		sockets:  newComposerSockets(),
	}

	composerHosts.Lock()
	defer composerHosts.Unlock()
	if _, taken := composerHosts.byID[id]; taken {
		cancel()
		return nil, fmt.Errorf("运行 %q 已在进行中", id)
	}
	composerHosts.byID[id] = host
	return host, nil
}

// unregisterComposerHost forgets host unless its id has come to name another one,
// and ends whatever it is still running: nothing outlives Run.
func unregisterComposerHost(id string, host *composerHost) {
	if host == nil {
		return
	}
	composerHosts.Lock()
	if composerHosts.byID[strings.TrimSpace(id)] == host {
		delete(composerHosts.byID, strings.TrimSpace(id))
	}
	composerHosts.Unlock()
	host.cancel()
}

// lookupComposerHost finds the run id names; nil when it is already gone.
func lookupComposerHost(id string) *composerHost {
	composerHosts.Lock()
	defer composerHosts.Unlock()
	return composerHosts.byID[strings.TrimSpace(id)]
}

// composerTimeout is how long a run may take: the page's own number of seconds,
// or the default when it left it out.
func composerTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return composerDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

// composerEmitter adapts the page's channel to the function a run pushes its
// events through; no channel means nobody is listening.
func composerEmitter(stream *mygo.Channel[ComposerEvent]) func(ComposerEvent) error {
	if stream == nil {
		return nil
	}
	return stream.Send
}

// start runs the composer script and reports what it did. Every run ends with a
// done event; the result is that event's counts and reason.
func (h *composerHost) start() ComposerResult {
	runCtx, cancel := context.WithTimeout(h.ctx, h.timeout)
	defer cancel()
	h.ctx = runCtx

	h.script = newScriptRunReport(composerScriptKind, h.vars, h)
	h.vm = h.script.vm
	h.script.setND("composer", h.composerGlobal())

	err := h.script.executeWithin(runCtx, h.code, h.timeout)
	// 运行自己的上下文先结束，才是超时或取消；脚本自己的错误按原话报告。
	reason := ""
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		reason = fmt.Sprintf("运行超时：超过 %s 未完成，已中断", h.timeout)
	case errors.Is(runCtx.Err(), context.Canceled):
		reason = "运行已取消"
	case err != nil:
		reason = err.Error()
	}
	// 连接归这次运行所有：脚本结束就全部关掉，它们的 goroutine 与登记表都不外溢。
	h.sockets.closeAll()
	// nd.vars 里最后写下的值也属于这次运行的快照。
	h.pullVars()

	// 计数与事件同锁读取：后台连接可能刚报了最后一条。
	h.reportMu.Lock()
	droppedLogs, droppedTests := h.droppedLogs, h.droppedTests
	result := ComposerResult{
		Tests:  h.tests,
		Passed: h.passed,
		Failed: h.tests - h.passed,
		Reason: reason,
	}
	h.reportMu.Unlock()

	if notes := overrunNotes(droppedLogs, droppedTests); notes != "" {
		h.emit(ComposerEvent{Type: "log", Level: "warn", Text: notes})
	}

	h.emitDone(ComposerEvent{
		Type:   "done",
		Tests:  result.Tests,
		Passed: result.Passed,
		Failed: result.Failed,
		Reason: reason,
		Vars:   h.varSnapshot(),
	})
	return result
}

// emit pushes one event to the page, stamped with when it happened. Nothing is
// pushed once the run reported done.
func (h *composerHost) emit(ev ComposerEvent) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	if h.finished {
		return
	}
	h.pushEvent(ev)
}

// emitDone is the last event of a run: after it, a background connection's late
// line cannot land behind the summary.
func (h *composerHost) emitDone(ev ComposerEvent) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	if h.finished {
		return
	}
	h.finished = true
	h.pushEvent(ev)
}

// pushEvent hands one event to the page; reportMu must be held.
func (h *composerHost) pushEvent(ev ComposerEvent) {
	if h.push == nil {
		return
	}
	if ev.At == 0 {
		ev.At = time.Now().UnixMilli()
	}
	_ = h.push(ev)
}

// Log is where the composer script's own console output goes.
func (h *composerHost) Log(level, text string, at int64) {
	h.log("", level, text, at)
}

// Test is where the composer script's own nd.test results go.
func (h *composerHost) Test(name string, pass bool, errText string) {
	h.recordTest(ScriptTest{Name: scriptText(name), Pass: pass, Error: scriptText(errText)})
}

// log emits one console line, within the limits a request script's output has.
// A line an entry's own script wrote carries that entry's name in front, so a
// run that logs across several requests can tell them apart; a connection's
// lifecycle line carries its kind ("ws", "tcp", "socketio") the same way.
func (h *composerHost) log(entry, level, text string, at int64) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	if h.finished {
		return
	}
	if h.logs >= scriptMaxLogs {
		h.droppedLogs++
		return
	}
	if entry != "" {
		text = "[" + entry + "] " + text
	}
	h.logs++
	h.pushEvent(ComposerEvent{Type: "log", Level: level, Text: scriptText(text), At: at})
}

// recordTest keeps one nd.test result and tells the page about it.
func (h *composerHost) recordTest(test ScriptTest) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	if h.finished {
		return
	}
	if h.tests >= scriptMaxTests {
		h.droppedTests++
		return
	}
	h.tests++
	if test.Pass {
		h.passed++
	}
	h.pushEvent(ComposerEvent{Type: "test", Name: test.Name, Pass: test.Pass, Error: test.Error})
}

// absorb carries one entry script's report into the run: what it logged and
// tested happened during this run, under the entry that ran it.
func (h *composerHost) absorb(entry string, out *scriptOutcome) {
	if out == nil {
		return
	}
	for _, line := range out.logs {
		h.log(entry, line.Level, line.Text, line.At)
	}
	for _, test := range out.tests {
		h.recordTest(test)
	}
}

// pullVars takes the script's nd.vars as the run's variable pool: what the
// composer script wrote is what the next called request expands.
func (h *composerHost) pullVars() {
	if h.script == nil {
		return
	}
	pool := h.script.exportedVars()
	h.varsMu.Lock()
	h.vars = pool
	h.varsMu.Unlock()
}

// pushVars hands the pool back to nd.vars, so what a called entry's script wrote
// is what the composer script reads next.
func (h *composerHost) pushVars() {
	if h.script == nil {
		return
	}
	h.varsMu.Lock()
	pool := h.vars
	h.varsMu.Unlock()
	h.script.api.ImportVars(pool)
}

// varSnapshot is the pool as a copy, so a caller reads it without the lock and
// without seeing later writes.
func (h *composerHost) varSnapshot() map[string]string {
	h.varsMu.Lock()
	defer h.varsMu.Unlock()
	return copyVars(h.vars)
}

// setVarPool replaces the pool with vars: an entry's script writes land here and
// the next call reads them.
func (h *composerHost) setVarPool(vars map[string]string) {
	if vars == nil {
		vars = map[string]string{}
	}
	h.varsMu.Lock()
	h.vars = vars
	h.varsMu.Unlock()
}

// composerGlobal is nd.composer: the calls a composer script makes into the
// workspace's own requests, and the connections it opens in the background.
func (h *composerHost) composerGlobal() *goja.Object {
	api := h.vm.NewObject()
	_ = api.Set("run", func(call goja.FunctionCall) goja.Value {
		ref := call.Argument(0)
		if ref == nil || goja.IsUndefined(ref) || goja.IsNull(ref) || strings.TrimSpace(ref.String()) == "" {
			h.throw("nd.composer.run 需要请求的名字或 id")
		}
		opts, err := h.optionsOf(call.Argument(1))
		if err != nil {
			h.throw("%s", err.Error())
		}
		return h.call(strings.TrimSpace(ref.String()), opts)
	})
	_ = api.Set("ws", h.socketGlobal(composerKindWS))
	_ = api.Set("tcp", h.socketGlobal(composerKindTCP))
	_ = api.Set("socketio", h.socketGlobal(composerKindSocketIO))
	return api
}

// composerOptions is what one nd.composer.run call overrides.
type composerOptions struct {
	timeout int               // 秒；0 = 用条目自己的值
	vars    map[string]string // 这次调用的覆盖，浅合并不写回
	// 这次调用里条目自己的预/后脚本能跑多久；缺省是钩子那套 scriptTimeout。
	scriptTimeout time.Duration
}

// optionsOf reads the options argument; absent or null is no override at all.
func (h *composerHost) optionsOf(value goja.Value) (composerOptions, error) {
	opts := composerOptions{scriptTimeout: scriptTimeout}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return opts, nil
	}
	obj, ok := value.(*goja.Object)
	if !ok {
		return opts, fmt.Errorf("nd.composer.run 的第二个参数需要是对象")
	}

	var invalid error
	err := h.vm.Try(func() {
		if seconds := obj.Get("timeout"); seconds != nil && !goja.IsUndefined(seconds) && !goja.IsNull(seconds) {
			if n := seconds.ToInteger(); n > 0 {
				opts.timeout = int(n)
			}
		}
		if seconds := obj.Get("scriptTimeout"); seconds != nil && !goja.IsUndefined(seconds) && !goja.IsNull(seconds) {
			if n := seconds.ToInteger(); n > 0 {
				opts.scriptTimeout = time.Duration(n) * time.Second
			}
		}
		overrides := obj.Get("vars")
		if overrides == nil || goja.IsUndefined(overrides) || goja.IsNull(overrides) {
			return
		}
		vars, ok := overrides.(*goja.Object)
		if !ok {
			invalid = fmt.Errorf("options.vars 需要是对象")
			return
		}
		opts.vars = make(map[string]string, len(vars.Keys()))
		for _, key := range vars.Keys() {
			if key == "" {
				continue
			}
			value := vars.Get(key)
			if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
				continue
			}
			opts.vars[key] = value.String()
		}
	})
	if invalid != nil {
		return composerOptions{}, invalid
	}
	if err != nil {
		return composerOptions{}, fmt.Errorf("读取 options 失败：%s", ndapi.ScriptErrorText(err))
	}
	return opts, nil
}

// call performs one nd.composer.run, the way Send performs one request: the
// entry's own pre script, then {{var}} expansion, then the request, then its post
// script. A failure reaches the script as a thrown Error; a 4xx/5xx response is
// an ordinary result.
func (h *composerHost) call(ref string, opts composerOptions) goja.Value {
	entry, err := h.resolveRequest(ref)
	if err != nil {
		h.throw("%s", err.Error())
	}
	entryName := composerRequestLabel(entry)

	// 脚本刚在 nd.vars 里写下的值先收进变量池，被调条目才看得到。
	h.pullVars()
	pool := h.varSnapshot()
	vars := pool
	if len(opts.vars) > 0 {
		// options.vars 是单次覆盖：这次调用在池的副本上跑，改不动变量池。
		vars = copyVars(pool)
		for key, value := range opts.vars {
			vars[key] = value
		}
	}
	// 变量池在调用结束时归位：条目脚本写下的变量跨调用延续，异常结束的调用也不该
	// 把它丢回调用前的样子；带覆盖的调用则把池原样放回去。
	defer func() {
		if len(opts.vars) > 0 {
			h.setVarPool(pool)
		} else {
			h.setVarPool(vars)
		}
		h.pushVars()
	}()

	h.emit(ComposerEvent{Type: "request", Phase: "start", Request: entryName})

	req := composerRequest(entry)
	if opts.timeout > 0 {
		req.Timeout = opts.timeout
	}
	if entry.PreScript != "" {
		out, preErr := runPreScriptWithin(h.ctx, entry.PreScript, &req, vars, opts.scriptTimeout)
		if out != nil {
			h.absorb(entryName, out)
			vars = out.vars
		}
		if preErr != nil {
			// 预脚本失败，请求根本不发出去。
			h.emitRequestEnd(entryName, APIResponse{}, preErr)
			h.throw("请求 %q 未发出：%s", entryName, preErr)
		}
	}
	applyVars(&req, vars)

	stream := &composerStream{}
	res, err := protocol.Do(h.ctx, protocolRequest(req), stream.emit)
	if err != nil {
		h.emitRequestEnd(entryName, APIResponse{}, err)
		h.throw("请求 %q 失败：%v", entryName, err)
	}
	resp := apiResponse(res)
	if stream.announced() {
		// 事件流不进 Body：它的 data 按到达顺序拼成脚本读到的 body。
		resp.Body = stream.body()
	}
	h.emitRequestEnd(entryName, resp, nil)

	postError := ""
	if entry.PostScript != "" {
		out, postErr := runPostScriptWithin(h.ctx, entry.PostScript, &req, &resp, vars, opts.scriptTimeout)
		if out != nil {
			h.absorb(entryName, out)
			vars = out.vars
		}
		if postErr != nil {
			// 响应本身算数，只有它的后脚本失败了。
			postError = postErr.Error()
		}
	}
	return h.responseObject(entryName, resp, stream, postError)
}

// emitRequestEnd tells the page a called request ended, however it ended.
func (h *composerHost) emitRequestEnd(entry string, resp APIResponse, err error) {
	ev := ComposerEvent{
		Type:    "request",
		Phase:   "end",
		Request: entry,
		Status:  resp.Status,
		TimeMS:  resp.TimeMS,
	}
	if err != nil {
		ev.Error = err.Error()
	}
	h.emit(ev)
}

// composerStream collects an event stream instead of handing it to the page: the
// head only says the response is a stream, and each event's data joins the body
// the script reads. protocol bounds every line and every event, so nothing here
// invents a limit of its own.
type composerStream struct {
	stream bool
	count  int
	lines  strings.Builder
}

// emit is where protocol pushes one message of the stream.
func (s *composerStream) emit(msg protocol.SSEMessage) error {
	if msg.Open != nil {
		s.stream = true
		return nil
	}
	if msg.Event == nil {
		return nil
	}
	if s.count > 0 {
		s.lines.WriteByte('\n')
	}
	s.lines.WriteString(msg.Event.Data)
	s.count++
	return nil
}

// announced reports whether the response was an event stream at all.
func (s *composerStream) announced() bool { return s.stream }

// body is the collected data, one event per line.
func (s *composerStream) body() string { return s.lines.String() }

// responseObject is what nd.composer.run hands back: the response as nd.response
// shows it, plus what only a composer call knows — the stream it collected and
// the post script that failed.
func (h *composerHost) responseObject(entry string, resp APIResponse, stream *composerStream, postError string) goja.Value {
	obj := h.script.api.ResponseGlobal(&ndapi.Response{
		Status:     resp.Status,
		StatusText: resp.StatusText,
		Headers:    scriptPairs(resp.Headers),
		Body:       resp.Body,
		TimeMS:     resp.TimeMS,
		SizeBytes:  resp.SizeBytes,
	})
	_ = obj.Set("truncated", resp.Truncated)
	_ = obj.Set("sse", stream.announced())
	_ = obj.Set("eventCount", stream.count)
	_ = obj.Set("postScriptError", postError)
	// json() 的报错带 composer 自己的前缀：脚本里写的是 nd.composer.run(...)，
	// 报错里出现 nd.response 会指错地方。
	_ = obj.Set("json", h.jsonFunc(entry, resp.Body))
	return obj
}

// jsonFunc is nd.response.json() for a composer call: the body parsed once, in
// JS, so the result is a real JS value.
func (h *composerHost) jsonFunc(entry, body string) func(goja.FunctionCall) goja.Value {
	vm := h.vm
	parse := vm.Get("JSON").ToObject(vm).Get("parse")
	label := "nd.composer.run(\"" + entry + "\").json(): "
	return func(goja.FunctionCall) goja.Value {
		call, ok := goja.AssertFunction(parse)
		if !ok {
			panic(h.newError(label + "JSON.parse is not available"))
		}
		value, err := call(goja.Undefined(), vm.ToValue(body))
		if err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(interrupted) // 中断要走中断路径，别包成普通 Error
			}
			panic(h.newError(label + ndapi.ScriptErrorText(err)))
		}
		return value
	}
}

// resolveRequest finds the entry a script names: an exact id first, so a script
// can pin one entry even when names repeat, then an exact name.
func (h *composerHost) resolveRequest(ref string) (SavedRequest, error) {
	var found *SavedRequest
	for i := range h.requests {
		if h.requests[i].ID == ref {
			found = &h.requests[i]
			break
		}
	}
	if found == nil {
		matches := make([]*SavedRequest, 0, 1)
		for i := range h.requests {
			if h.requests[i].Name == ref {
				matches = append(matches, &h.requests[i])
			}
		}
		switch len(matches) {
		case 0:
			return SavedRequest{}, fmt.Errorf("请求 %q 不存在", ref)
		case 1:
			found = matches[0]
		default:
			return SavedRequest{}, fmt.Errorf("工作区里有 %d 个请求都叫 %q，请改用 id 指定", len(matches), ref)
		}
	}
	// 其余 kind（websocket/tcp/socketio/markdown/grpc）留待以后：它们的「一次
	// 请求一份响应」语义与 HTTP 不同，先只把 http 条目（含 SSE 响应）接上。
	if kind := composerKind(*found); kind != "http" {
		return SavedRequest{}, fmt.Errorf("请求 %q 是 %s 条目，暂不支持：composer 目前只调用 http 条目", ref, kind)
	}
	return *found, nil
}

// composerKind is an entry's kind; empty reads as http, the way an old .band file
// leaves it.
func composerKind(entry SavedRequest) string {
	if kind := strings.TrimSpace(entry.Kind); kind != "" {
		return kind
	}
	return "http"
}

// composerRequestLabel is how a called entry is shown in its events: its name, or
// its id when the entry has none.
func composerRequestLabel(entry SavedRequest) string {
	if name := strings.TrimSpace(entry.Name); name != "" {
		return name
	}
	return entry.ID
}

// composerRequest maps a workspace entry onto the request Send would send, as a
// copy the run may expand in place: {{var}} rewriting and the entry's own pre
// script both write into it, and the snapshot the page sent must read the same
// on the next call.
func composerRequest(entry SavedRequest) APIRequest {
	return APIRequest{
		Method:     entry.Method,
		URL:        entry.URL,
		Params:     append([]KeyValue(nil), entry.Params...),
		Headers:    append([]KeyValue(nil), entry.Headers...),
		BodyType:   entry.BodyType,
		FormData:   append([]FormField(nil), entry.FormData...),
		URLEncoded: append([]KeyValue(nil), entry.URLEncoded...),
		Raw:        entry.Raw,
		RawType:    entry.RawType,
		BinaryPath: entry.BinaryPath,
		Auth:       entry.Auth,
		Cookies:    append([]KeyValue(nil), entry.Cookies...),
		Timeout:    entry.Timeout,
		HTTP2:      entry.HTTP2,
		PreScript:  entry.PreScript,
		PostScript: entry.PostScript,
	}
}

// throw raises a JS Error, so the script's own try/catch sees the message as
// written (the way nd.assert fails a script).
func (h *composerHost) throw(format string, args ...any) {
	panic(h.newError(fmt.Sprintf(format, args...)))
}

// newError makes a JS Error carrying msg.
func (h *composerHost) newError(msg string) goja.Value {
	if obj, err := h.vm.New(h.vm.Get("Error"), h.vm.ToValue(msg)); err == nil {
		return obj
	}
	return h.vm.NewTypeError(msg)
}
