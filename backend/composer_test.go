package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// composerEvents 是页面通道在测试里的替身：运行推来的每条事件都按到达顺序落进
// 列表，断言看到的就是页面会看到的顺序。
type composerEvents struct {
	mu     sync.Mutex
	events []ComposerEvent
}

func (p *composerEvents) emit(ev ComposerEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

func (p *composerEvents) snapshot() []ComposerEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ComposerEvent, len(p.events))
	copy(out, p.events)
	return out
}

// describeComposerEvents 让断言失败的报错看得出页面收到了什么。
func describeComposerEvents(events []ComposerEvent) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		part := ev.Type
		switch ev.Type {
		case "request":
			part += "/" + ev.Phase + ":" + ev.Request
		case "log":
			part += "/" + ev.Level
		case "test":
			part += "/" + ev.Name
		}
		parts = append(parts, part)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// composerRequestTrail 是事件里的 request 标记（start/end 与条目名），顺序即调用
// 顺序。
func composerRequestTrail(events []ComposerEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.Type == "request" {
			out = append(out, ev.Phase+":"+ev.Request)
		}
	}
	return out
}

// composerLogLines 是到页面的 console 行。
func composerLogLines(events []ComposerEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.Type == "log" {
			out = append(out, ev.Text)
		}
	}
	return out
}

// composerTestEvents 是到页面的断言结果，按产生顺序。
func composerTestEvents(events []ComposerEvent) []ComposerEvent {
	out := make([]ComposerEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type == "test" {
			out = append(out, ev)
		}
	}
	return out
}

// composerDone 是 run 结尾的 done 事件。
func composerDone(t *testing.T, events []ComposerEvent) ComposerEvent {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "done" {
			return events[i]
		}
	}
	t.Fatalf("事件里没有 done：%s", describeComposerEvents(events))
	return ComposerEvent{}
}

// hasComposerLog 报告有没有一条包含 want 的 console 行。
func hasComposerLog(events []ComposerEvent, want string) bool {
	for _, line := range composerLogLines(events) {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// blockingServer 起一个只有在请求真的进来之后才继续的服务：handler 一直等到
// 客户端取消，测试等的就是这个信号，不靠墙钟。
func blockingServer(t *testing.T, started chan<- struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 一条 composer 脚本串行调用两个条目：先 GET 断言 200，再 POST 让条目的预脚本
// 改 header。事件流、断言计数与变量写回都据此检查。
func TestComposerRunsEntriesOneAfterAnother(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery+" token="+r.Header.Get("X-Token"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/hello":
			_, _ = w.Write([]byte(`{"ok":true,"room":"` + r.URL.Query().Get("room") + `"}`))
		case "/echo":
			_, _ = w.Write([]byte(`{"token":"` + r.Header.Get("X-Token") + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:   "order-" + t.Name(),
		Vars: map[string]string{"room": "7"},
		Requests: []SavedRequest{
			{ID: "hello", Name: "取首页", Method: "GET", URL: srv.URL + "/hello?room={{room}}"},
			{
				ID: "echo", Name: "回显", Method: "POST", URL: srv.URL + "/echo",
				BodyType: "raw", Raw: `{"a":1}`, RawType: "json",
				// 条目的预脚本读写的是这次运行的变量池。
				PreScript: `nd.request.headers["X-Token"] = nd.vars.get("token"); console.log("预脚本看到", nd.vars.get("token"));`,
				// 后脚本失败不影响响应本身，也不让运行失败。
				PostScript: `throw new Error("post boom");`,
			},
		},
		Script: `
			var first = nd.composer.run("取首页");
			nd.test("首页是 200", function () {
				if (first.status !== 200) throw new Error("status " + first.status);
			});
			nd.test("首页 body 能解析", function () {
				if (first.json().room !== "7") throw new Error("body " + first.body);
			});
			nd.test("返回对象字段齐全", function () {
				if (typeof first.headers !== "object") throw new Error("headers");
				if (typeof first.timeMS !== "number" || typeof first.sizeBytes !== "number") throw new Error("timing");
				if (first.truncated !== false || first.sse !== false || first.eventCount !== 0 || first.postScriptError !== "") {
					throw new Error("shape");
				}
			});
			nd.vars.set("token", "secret");
			var second = nd.composer.run("回显");
			nd.test("回显带上脚本写的 header", function () {
				if (second.json().token !== "secret") throw new Error("body " + second.body);
			});
			nd.test("后脚本失败只记在结果里", function () {
				if (second.status !== 200) throw new Error("status " + second.status);
				if (String(second.postScriptError).indexOf("boom") < 0) throw new Error("postScriptError " + second.postScriptError);
			});
			console.log("两次调用完成", first.status, second.status);
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Tests != 5 || res.Passed != 5 || res.Failed != 0 || res.Reason != "" {
		t.Fatalf("result = %#v, want five passing tests and no reason", res)
	}

	all := events.snapshot()
	want := []string{"start:取首页", "end:取首页", "start:回显", "end:回显"}
	if got := composerRequestTrail(all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("request events = %v, want %v（事件流 %s）", got, want, describeComposerEvents(all))
	}
	// done 是最后一条，计数与返回值一致，变量全量下发。
	last := all[len(all)-1]
	if last.Type != "done" {
		t.Fatalf("last event = %s, want done（事件流 %s）", last.Type, describeComposerEvents(all))
	}
	if last.Tests != res.Tests || last.Passed != res.Passed || last.Failed != res.Failed || last.Reason != res.Reason {
		t.Fatalf("done = %#v, want the result %#v", last, res)
	}
	if last.Vars["token"] != "secret" || last.Vars["room"] != "7" {
		t.Fatalf("done vars = %v, want the script's token and the page's room", last.Vars)
	}
	// 断言结果按产生顺序到达页面。
	if tests := composerTestEvents(all); len(tests) != 5 || !tests[0].Pass || !tests[4].Pass {
		t.Fatalf("test events = %#v, want five passing results", tests)
	}
	// 条目脚本的 console 行带条目名，composer 自己的行不带。
	if !hasComposerLog(all, "[回显] ") || !hasComposerLog(all, "预脚本看到") {
		t.Fatalf("logs = %v, want the entry's line prefixed with its name", composerLogLines(all))
	}
	if !hasComposerLog(all, "两次调用完成") {
		t.Fatalf("logs = %v, want the composer script's own line", composerLogLines(all))
	}
	// {{var}} 展开到了条目自己的地址上，条目预脚本写的 header 也真的发了出去。
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(seen, " | ")
	if !strings.Contains(joined, "/hello?room=7") {
		t.Errorf("server saw %q, want the {{var}} expanded", joined)
	}
	if !strings.Contains(joined, "/echo? token=secret") {
		t.Errorf("server saw %q, want the header the entry's pre script set", joined)
	}
}

// nd.composer.run 先按 id 精确匹配，再按名字；名字不唯一与找不到都要能被脚本的
// try/catch 接住。
func TestComposerRunResolvesByIdThenName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Query().Get("which")))
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "resolve-" + t.Name(),
		Requests: []SavedRequest{
			{ID: "dup-1", Name: "重复", URL: srv.URL + "/x?which=one"},
			{ID: "dup-2", Name: "重复", URL: srv.URL + "/x?which=two"},
			{ID: "solo", Name: "唯一", URL: srv.URL + "/x?which=solo"},
		},
		Script: `
			var byID = nd.composer.run("dup-2");
			var byName = nd.composer.run("唯一");
			var ambiguous = "";
			try { nd.composer.run("重复"); } catch (e) { ambiguous = String(e.message); }
			var missing = "";
			try { nd.composer.run("没有这个"); } catch (e) { missing = String(e.message); }
			nd.vars.set("byID", byID.body);
			nd.vars.set("byName", byName.body);
			nd.vars.set("ambiguous", ambiguous);
			nd.vars.set("missing", missing);
		`,
	}

	if _, err := runComposer(context.Background(), params, events.emit); err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if vars["byID"] != "two" {
		t.Errorf("by id = %q, want the second entry's body", vars["byID"])
	}
	if vars["byName"] != "solo" {
		t.Errorf("by name = %q, want the only entry with that name", vars["byName"])
	}
	if !strings.Contains(vars["ambiguous"], "2") || !strings.Contains(vars["ambiguous"], "重复") {
		t.Errorf("ambiguous = %q, want how many entries share the name", vars["ambiguous"])
	}
	if !strings.Contains(vars["missing"], "没有这个") {
		t.Errorf("missing = %q, want the name it could not find", vars["missing"])
	}
}

// options.vars 是单次覆盖：这次调用读到覆盖值，下一次调用仍读变量池里的原值。
func TestComposerRunOptionsVarsApplyOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		_, _ = w.Write([]byte(query.Get("who") + "|" + query.Get("base")))
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:   "overlay-" + t.Name(),
		Vars: map[string]string{"who": "原值", "base": "池"},
		Requests: []SavedRequest{
			{ID: "vars", Name: "带变量", URL: srv.URL + "/x?who={{who}}&base={{base}}"},
		},
		Script: `
			var overlaid = nd.composer.run("带变量", {vars: {who: "一次"}});
			var plain = nd.composer.run("带变量");
			nd.vars.set("overlaid", overlaid.body);
			nd.vars.set("plain", plain.body);
		`,
	}

	if _, err := runComposer(context.Background(), params, events.emit); err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if got, want := vars["overlaid"], "一次|池"; got != want {
		t.Errorf("overlaid body = %q, want %q", got, want)
	}
	if got, want := vars["plain"], "原值|池"; got != want {
		t.Errorf("next call body = %q, want the pool's own value back (%q)", got, want)
	}
	if got, want := vars["who"], "原值"; got != want {
		t.Errorf("vars who = %q, want the overlay left out of the pool (%q)", got, want)
	}
}

// 条目的预脚本失败，run 抛出「请求未发出」：脚本接住了这次运行就正常结束，
// 没接住则 done 的 Reason 带错误。
func TestComposerEntryPreScriptFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	requests := []SavedRequest{
		{ID: "bad", Name: "坏条目", URL: srv.URL + "/x", PreScript: `throw new Error("boom")`},
	}

	caught := &composerEvents{}
	params := ComposerRunParams{
		ID:       "caught-" + t.Name(),
		Requests: requests,
		Script: `
			var msg = "";
			try { nd.composer.run("坏条目"); } catch (e) { msg = String(e.message); }
			nd.vars.set("msg", msg);
		`,
	}
	res, err := runComposer(context.Background(), params, caught.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	all := caught.snapshot()
	done := composerDone(t, all)
	if done.Reason != "" || res.Reason != "" {
		t.Fatalf("reason = %q, want none after the script caught the failure", done.Reason)
	}
	if !strings.Contains(done.Vars["msg"], "未发出") || !strings.Contains(done.Vars["msg"], "boom") {
		t.Fatalf("msg = %q, want the failure the script caught", done.Vars["msg"])
	}
	// 这次调用以 end 事件收尾，并带着错误；请求一个字节也没发出去。
	var endedWithError bool
	for _, ev := range all {
		if ev.Type == "request" && ev.Phase == "end" && strings.Contains(ev.Error, "boom") {
			endedWithError = true
		}
	}
	if !endedWithError {
		t.Fatalf("events = %s, want the request's end event to carry the failure", describeComposerEvents(all))
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("server got %d requests, want none after a failed pre script", got)
	}

	uncaught := &composerEvents{}
	params.ID = "uncaught-" + t.Name()
	params.Script = `nd.composer.run("坏条目");`
	res, err = runComposer(context.Background(), params, uncaught.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if !strings.Contains(res.Reason, "boom") || !strings.Contains(res.Reason, "未发出") {
		t.Fatalf("reason = %q, want the uncaught failure", res.Reason)
	}
	if done := composerDone(t, uncaught.snapshot()); done.Reason != res.Reason {
		t.Fatalf("done reason = %q, want the result's %q", done.Reason, res.Reason)
	}
}

// SSE 条目的响应体是拼接起来的 data，事件数就是 eventCount。
func TestComposerCollectsAnEventStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: one\n\ndata: two\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:       "sse-" + t.Name(),
		Requests: []SavedRequest{{ID: "events", Name: "事件流", URL: srv.URL + "/events"}},
		Script: `
			var stream = nd.composer.run("事件流");
			nd.vars.set("sse", String(stream.sse));
			nd.vars.set("count", String(stream.eventCount));
			nd.vars.set("body", stream.body);
			nd.test("事件流是 200", function () {
				if (stream.status !== 200) throw new Error("status " + stream.status);
			});
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Tests != 1 || res.Passed != 1 {
		t.Fatalf("result = %#v, want the one passing test", res)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if vars["sse"] != "true" {
		t.Errorf("sse = %q, want the response recognised as a stream", vars["sse"])
	}
	if vars["count"] != "2" {
		t.Errorf("eventCount = %q, want 2", vars["count"])
	}
	if got, want := vars["body"], "one\ntwo"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// Stop 让在途的运行及时返回，Reason 说明是取消；运行本身也会从登记表里注销。
func TestComposerStopCancelsTheRun(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := blockingServer(t, started)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:       "stop-" + t.Name(),
		Timeout:  30,
		Requests: []SavedRequest{{ID: "slow", Name: "慢条目", URL: srv.URL + "/slow"}},
		Script:   `nd.composer.run("慢条目");`,
	}

	type outcome struct {
		res ComposerResult
		err error
	}
	finished := make(chan outcome, 1)
	go func() {
		res, err := runComposer(context.Background(), params, events.emit)
		finished <- outcome{res: res, err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("请求没有到达服务端：%s", describeComposerEvents(events.snapshot()))
	}

	(ComposerService{}).Stop(params.ID)

	var got outcome
	select {
	case got = <-finished:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop 之后 Run 没有返回：%s", describeComposerEvents(events.snapshot()))
	}
	if got.err != nil {
		t.Fatalf("Run returned %v after Stop, want the result alone", got.err)
	}
	if !strings.Contains(got.res.Reason, "已取消") {
		t.Fatalf("result reason = %q, want the cancelled message", got.res.Reason)
	}
	if done := composerDone(t, events.snapshot()); !strings.Contains(done.Reason, "已取消") {
		t.Fatalf("done reason = %q, want the cancelled message", done.Reason)
	}
	// 停下的运行已经注销：同一个 id 可以直接再跑。
	if host := lookupComposerHost(params.ID); host != nil {
		t.Fatalf("host %v is still registered after Stop", host)
	}
}

// 整个运行超时（Timeout 以秒计）：停住的端点让请求一直不返回，Reason 说明是超时。
func TestComposerRunTimesOut(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := blockingServer(t, started)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:       "timeout-" + t.Name(),
		Timeout:  1,
		Requests: []SavedRequest{{ID: "slow", Name: "慢条目", URL: srv.URL + "/slow"}},
		Script:   `nd.composer.run("慢条目");`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if !strings.Contains(res.Reason, "超时") {
		t.Fatalf("reason = %q, want the timeout message", res.Reason)
	}
	if done := composerDone(t, events.snapshot()); done.Reason != res.Reason {
		t.Fatalf("done reason = %q, want the result's %q", done.Reason, res.Reason)
	}
	select {
	case <-started:
	default:
		t.Fatal("请求没有到达服务端，超时的就不是这次调用")
	}
}

// composer 脚本可以在循环里写日志、记断言：保留上限与请求脚本同一套，超出的部分
// 丢掉，done 之前补一条 warn 说明。
func TestComposerKeepsTheRetentionLimits(t *testing.T) {
	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "limits-" + t.Name(),
		Script: `
			for (var i = 0; i < 1025; i++) console.log("line", i);
			for (var j = 0; j < 1005; j++) nd.test("t" + j, function () {});
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Tests != scriptMaxTests || res.Passed != scriptMaxTests || res.Failed != 0 {
		t.Fatalf("result = %#v, want the %d kept results", res, scriptMaxTests)
	}
	if got := len(composerTestEvents(events.snapshot())); got != scriptMaxTests {
		t.Fatalf("test events = %d, want %d", got, scriptMaxTests)
	}
	// 保留的日志之外，还有一条说明丢了什么。
	logs := composerLogLines(events.snapshot())
	if len(logs) != scriptMaxLogs+1 {
		t.Fatalf("log events = %d, want %d plus one note", len(logs), scriptMaxLogs)
	}
	note := logs[len(logs)-1]
	if !strings.Contains(note, "console 输出超过") || !strings.Contains(note, "nd.test") {
		t.Fatalf("note = %q, want a warning about both dropped limits", note)
	}
}

// 同一个 id 不能有两个运行：Stop 分不开它们。
func TestComposerRunRefusesADuplicateID(t *testing.T) {
	params := ComposerRunParams{ID: "duplicate-" + t.Name()}
	host, err := registerComposerHost(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("registerComposerHost: %v", err)
	}
	defer unregisterComposerHost(params.ID, host)

	if _, err := registerComposerHost(context.Background(), params, nil); err == nil || !strings.Contains(err.Error(), "已在进行中") {
		t.Fatalf("second register = %v, want the duplicate-id error", err)
	}
	if _, err := runComposer(context.Background(), ComposerRunParams{}, nil); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("run without an id = %v, want the missing-id error", err)
	}
}

// nd.composer 只活在 composer 脚本里：被调条目的脚本看不到它，也就没法从一个
// 运行里再起一个运行。
func TestComposerGlobalIsNotVisibleToEntryScripts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "nested-" + t.Name(),
		Requests: []SavedRequest{{
			ID: "probe", Name: "探针", URL: srv.URL + "/x",
			PreScript:  `nd.vars.set("preComposer", typeof nd.composer); nd.vars.set("preRun", typeof (nd.composer && nd.composer.run));`,
			PostScript: `nd.vars.set("postComposer", typeof nd.composer);`,
		}},
		Script: `
			nd.vars.set("composerVisible", typeof nd.composer);
			nd.vars.set("apis", typeof nd.composer.ws.open + "/" + typeof nd.composer.tcp.open + "/" + typeof nd.composer.socketio.open);
			nd.composer.run("探针");
		`,
	}

	if _, err := runComposer(context.Background(), params, events.emit); err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if got, want := vars["composerVisible"], "object"; got != want {
		t.Errorf("composer script sees nd.composer as %q, want %q", got, want)
	}
	if got, want := vars["apis"], "function/function/function"; got != want {
		t.Errorf("composer APIs = %q, want %q", got, want)
	}
	for _, name := range []string{"preComposer", "preRun", "postComposer"} {
		if got, want := vars[name], "undefined"; got != want {
			t.Errorf("entry script sees %s as %q, want %q（被调条目不能嵌套 composer）", name, got, want)
		}
	}
}

// options.scriptTimeout 只作用于这一次调用里条目自己的预/后脚本：给 1s 时长脚本被
// 中断并报出真实秒数，下一次调用换回正常时长照跑。
func TestComposerRunOptionsScriptTimeout(t *testing.T) {
	if opts, err := (&composerHost{}).optionsOf(nil); err != nil || opts.scriptTimeout != scriptTimeout {
		t.Fatalf("default scriptTimeout = %v (err %v), want the hook's %v", opts.scriptTimeout, err, scriptTimeout)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "script-timeout-" + t.Name(),
		Requests: []SavedRequest{
			{
				ID: "slow", Name: "慢脚本", URL: srv.URL + "/x",
				PreScript: `nd.crypto.pbkdf2("password", "salt", 10000000, 32, "sha256"); nd.vars.set("slowRan", "yes");`,
			},
			{
				ID: "fast", Name: "短脚本", URL: srv.URL + "/x",
				PreScript: `nd.vars.set("ran", "yes");`,
			},
		},
		Script: `
			var slow = "";
			try {
				nd.composer.run("慢脚本", {scriptTimeout: 1});
			} catch (e) {
				slow = String(e.message);
			}
			nd.vars.set("slow", slow);
			nd.composer.run("短脚本", {scriptTimeout: 30});
		`,
	}

	began := time.Now()
	res, err := runComposer(context.Background(), params, events.emit)
	elapsed := time.Since(began)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("result = %#v, want the script's own catch to absorb the timeout", res)
	}

	vars := composerDone(t, events.snapshot()).Vars
	if !strings.Contains(vars["slow"], "超时") || !strings.Contains(vars["slow"], "1s") {
		t.Errorf("slow = %q, want the timeout with the seconds it was given", vars["slow"])
	}
	if vars["slowRan"] == "yes" {
		t.Error("慢脚本跑完了：scriptTimeout 没有中断它")
	}
	if got, want := vars["ran"], "yes"; got != want {
		t.Errorf("ran = %q, want the next call's pre script to run (%q)", got, want)
	}
	// 1s 的重叠之外不该再有等待：pbkdf2 是被中断的，不是被等完的。
	if elapsed > 5*time.Second {
		t.Errorf("运行耗时 %s，want about a second", elapsed)
	}
}

// 页面没给通道（或通道已经关了）时事件只是丢掉，运行照旧完成并给出计数。
func TestComposerRunWithoutAPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	res, err := (ComposerService{}).Run(context.Background(), ComposerRunParams{
		ID:       "no-page-" + t.Name(),
		Requests: []SavedRequest{{ID: "hello", Name: "取首页", URL: srv.URL + "/hello"}},
		Script: `
			var res = nd.composer.run("取首页");
			nd.test("是 200", function () { if (res.status !== 200) throw new Error("status " + res.status); });
		`,
	}, &mygo.Channel[ComposerEvent]{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tests != 1 || res.Passed != 1 || res.Failed != 0 || res.Reason != "" {
		t.Fatalf("result = %#v, want the one passing test", res)
	}
}
