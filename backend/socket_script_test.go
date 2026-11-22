package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"netdream/backend/ndapi"
	"netdream/backend/protocol"
)

// pageEvents 是页面通道在测试里的替身：协议事件与脚本事件都落进同一个列表，
// 断言看到的就是页面会看到的顺序。
type pageEvents struct {
	mu     sync.Mutex
	events []SocketEvent
}

func (p *pageEvents) emit(ev SocketEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

func (p *pageEvents) snapshot() []SocketEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]SocketEvent, len(p.events))
	copy(out, p.events)
	return out
}

// waitFor 等到事件满足 cond；连接与脚本都是异步的，等的是事件而不是墙钟。
func (p *pageEvents) waitFor(t *testing.T, what string, cond func([]SocketEvent) bool) []SocketEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events := p.snapshot()
		if cond(events) {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待%s超时，已有事件 %s", what, describeSocketEvents(events))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// describeSocketEvents 让等待失败的报错看得出页面收到了什么。
func describeSocketEvents(events []SocketEvent) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		part := ev.Type
		if ev.Direction != "" {
			part += "/" + ev.Direction
		}
		if ev.Script != nil {
			part += "(" + ev.Script.Phase + ")"
		}
		parts = append(parts, part)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// hasSocketEvent 报告 events 里有没有满足 cond 的某一条。
func hasSocketEvent(events []SocketEvent, cond func(SocketEvent) bool) bool {
	for _, ev := range events {
		if cond(ev) {
			return true
		}
	}
	return false
}

// socketReports 是事件里的 script 报告，按到达顺序。
func socketReports(events []SocketEvent) []SocketScriptReport {
	out := make([]SocketScriptReport, 0, len(events))
	for _, ev := range events {
		if ev.Type == "script" && ev.Script != nil {
			out = append(out, *ev.Script)
		}
	}
	return out
}

// receivedMessages 是到页面的接收消息。
func receivedMessages(events []SocketEvent) []SocketEvent {
	out := make([]SocketEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type == "message" && ev.Direction == "receive" {
			out = append(out, ev)
		}
	}
	return out
}

// indexOfSocketEvent 是第一条满足 cond 的事件下标，-1 表示没有。
func indexOfSocketEvent(events []SocketEvent, cond func(SocketEvent) bool) int {
	for i, ev := range events {
		if cond(ev) {
			return i
		}
	}
	return -1
}

// testSink 把一条连接的事件按 main 的规则交给页面替身：事件先到页面，收到的
// 消息随后进接收脚本。
type testSink struct {
	events *pageEvents
	script *socketScriptHost
}

func (s testSink) Send(ev protocol.SocketEvent) error {
	return deliverSocketEvent(socketEvent(ev), s.events.emit, s.script)
}

// startScriptedTCP 起一条真实的 TCP 连接：params 的脚本按 Connect 的方式注册，
// 协议事件经 testSink 进页面替身，serve 拿到服务端那一头。返回页面看到的事件，
// 以及客户端写到线上的每一段字节。
func startScriptedTCP(t *testing.T, params SocketConnectParams, serve func(conn net.Conn)) (*pageEvents, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening failed: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	received := make(chan string, 8)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if serve != nil {
			// 写走自己的 goroutine，读循环留在这一条里。
			go serve(conn)
		}
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				received <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	events := &pageEvents{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	params.Kind = "tcp"
	params.URL = listener.Addr().String()
	host := registerSocketScripts(ctx, params, events.emit)
	if host == nil {
		t.Fatal("connection has no script host although the params carry scripts")
	}
	t.Cleanup(func() { unregisterSocketScripts(params.ID, host) })

	go func() {
		_ = protocol.Connect(ctx, protocolConnectParams(params), testSink{events: events, script: host})
	}()
	events.waitFor(t, "连接建立", func(evs []SocketEvent) bool {
		return hasSocketEvent(evs, func(ev SocketEvent) bool { return ev.Type == "open" })
	})
	return events, received
}

// 预连接脚本改写 url/query/headers，它写回的变量参与 {{var}} 展开，产物随一条
// script 事件下发。
func TestPreScriptRewritesTheConnection(t *testing.T) {
	events := &pageEvents{}
	params := SocketConnectParams{
		ID:      "pre-" + t.Name(),
		Kind:    "websocket",
		URL:     "ws://{{host}}/{{path}}",
		Params:  []KeyValue{{Key: "room", Value: "{{room}}"}},
		Headers: []KeyValue{{Key: "Accept", Value: "*/*"}},
		Vars:    map[string]string{"host": "api.example", "path": "old", "room": "1", "token": "secret"},
		PreScript: `
			nd.request.url = "wss://" + nd.vars.get("host") + "/{{path}}";
			nd.request.query["room"] = "99";
			nd.request.headers["X-Token"] = "{{token}}";
			nd.vars.set("path", "live");
			nd.vars.set("seen", "yes");
			console.log("pre", nd.vars.get("host"));
			nd.test("改写后的地址是 wss", function () {
				if (nd.request.url.indexOf("wss://") !== 0) throw new Error("not wss");
			});
		`,
	}
	host := newSocketScriptHost(context.Background(), params, events.emit)
	t.Cleanup(host.cancel)

	if err := host.preConnect(&params); err != nil {
		t.Fatalf("preConnect: %v", err)
	}
	expandSocketVars(&params, host.varsSnapshot(nil))

	// 脚本写回的 path=live 参与展开，脚本写下的 {{token}} 也展开成变量值。
	if got, want := params.URL, "wss://api.example/live"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := params.Params[0].Value, "99"; got != want {
		t.Errorf("query room = %q, want %q", got, want)
	}
	if len(params.Headers) != 2 {
		t.Fatalf("headers = %v, want the page's Accept and the script's X-Token", params.Headers)
	}
	if params.Headers[0].Key != "Accept" {
		t.Errorf("headers = %v, want the page's own header kept", params.Headers)
	}
	if got, want := params.Headers[1].Value, "secret"; got != want {
		t.Errorf("X-Token = %q, want %q", got, want)
	}
	// 页面发来的变量快照不被改写：写回只经 script 事件。
	if got := params.Vars["path"]; got != "old" {
		t.Errorf("page vars = %q, want the snapshot as it came in", got)
	}

	reports := socketReports(events.snapshot())
	if len(reports) != 1 {
		t.Fatalf("script reports = %d, want 1", len(reports))
	}
	report := reports[0]
	if report.Phase != socketPhasePre || report.Error != "" {
		t.Fatalf("report = %#v, want a successful pre report", report)
	}
	if len(report.Logs) != 1 || !strings.Contains(report.Logs[0].Text, "pre") {
		t.Errorf("logs = %#v, want the console line the script wrote", report.Logs)
	}
	if len(report.Tests) != 1 || report.Tests[0].Name != "改写后的地址是 wss" || !report.Tests[0].Pass {
		t.Errorf("tests = %#v, want the passing nd.test the script recorded", report.Tests)
	}
	if report.Vars["seen"] != "yes" || report.Vars["path"] != "live" || report.Vars["host"] != "api.example" {
		t.Errorf("vars = %v, want the full snapshot after the run", report.Vars)
	}
}

// 预连接脚本出错就不拨号：返回的是脚本错误，而不是拨号失败的连接错误，
// 页面据此展示失败的那条 script 事件。
func TestPreScriptFailureKeepsTheConnectionFromOpening(t *testing.T) {
	params := SocketConnectParams{
		ID:        "pre-fail-" + t.Name(),
		Kind:      "tcp",
		URL:       "127.0.0.1:1",
		PreScript: `throw new Error("boom")`,
	}
	err := (SocketService{}).Connect(context.Background(), params, &mygo.Channel[SocketEvent]{})
	if err == nil {
		t.Fatal("Connect returned no error although the pre script threw")
	}
	if !strings.Contains(err.Error(), "预连接脚本出错：") {
		t.Fatalf("Connect error = %v, want the pre script's failure", err)
	}
	if strings.Contains(err.Error(), "connecting to") {
		t.Fatalf("Connect error = %v, want no dial after a failed pre script", err)
	}
	// 没有连接留在 protocol 的登记表里，也就没有拨号发生。
	if sendErr := protocol.Send(protocol.SendParams{ID: params.ID}); sendErr == nil || !strings.Contains(sendErr.Error(), "no connection") {
		t.Fatalf("protocol.Send after a failed pre script = %v, want no connection", sendErr)
	}
}

// 发送脚本的改写真的发到了线上，脚本事件在消息事件之前到达页面。
func TestSendScriptRewritesThePayloadOnTheWire(t *testing.T) {
	params := SocketConnectParams{
		ID:         "send-" + t.Name(),
		SendScript: `nd.message.payload = "[" + nd.message.payload + "]"; nd.vars.set("sent", nd.message.payload)`,
	}
	events, received := startScriptedTCP(t, params, nil)

	if err := (SocketService{}).Send(SocketSendParams{ID: params.ID, Payload: "ping"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-received:
		if want := "[ping]"; got != want {
			t.Fatalf("the wire carried %q, want the rewritten %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the message never reached the wire")
	}

	all := events.waitFor(t, "发送后的事件", func(evs []SocketEvent) bool {
		return hasSocketEvent(evs, func(ev SocketEvent) bool { return ev.Type == "message" && ev.Direction == "send" })
	})
	reports := socketReports(all)
	if len(reports) != 1 {
		t.Fatalf("script reports = %d, want 1", len(reports))
	}
	report := reports[0]
	if report.Phase != socketPhaseSend || report.Error != "" {
		t.Fatalf("report = %#v, want a successful send report", report)
	}
	if report.Vars["sent"] != "[ping]" {
		t.Errorf("vars = %v, want the payload the script settled on", report.Vars)
	}
	// 脚本先跑、消息后发：页面先看到 script 事件，再看到这条 message。
	scriptAt := indexOfSocketEvent(all, func(ev SocketEvent) bool { return ev.Type == "script" })
	messageAt := indexOfSocketEvent(all, func(ev SocketEvent) bool { return ev.Type == "message" && ev.Direction == "send" })
	if scriptAt < 0 || messageAt < 0 || scriptAt > messageAt {
		t.Fatalf("events are %s, want the send script's report before the message", describeSocketEvents(all))
	}
}

// 发送脚本出错那条消息就不发：返回的是脚本错误，线上一个字节都没有。
func TestSendScriptFailureKeepsTheMessageFromGoingOut(t *testing.T) {
	params := SocketConnectParams{
		ID:         "send-fail-" + t.Name(),
		SendScript: `throw new Error("boom")`,
	}
	events, received := startScriptedTCP(t, params, nil)

	err := (SocketService{}).Send(SocketSendParams{ID: params.ID, Payload: "ping"})
	if err == nil {
		t.Fatal("Send returned no error although the send script threw")
	}
	if !strings.Contains(err.Error(), "发送脚本出错：") {
		t.Fatalf("Send error = %v, want the send script's failure", err)
	}
	select {
	case got := <-received:
		t.Fatalf("the wire carried %q, want nothing after a failed send script", got)
	default:
	}

	all := events.snapshot()
	if hasSocketEvent(all, func(ev SocketEvent) bool { return ev.Type == "message" }) {
		t.Fatalf("events are %s, want no message event", describeSocketEvents(all))
	}
	reports := socketReports(all)
	if len(reports) != 1 || reports[0].Phase != socketPhaseSend || !strings.Contains(reports[0].Error, "发送脚本出错：") {
		t.Fatalf("reports = %#v, want one failing send report", reports)
	}
}

// 接收脚本在消息之后跑：消息原样先到页面，脚本看到的是只读视图，改不动它。
func TestReceiveScriptSeesTheMessageAfterThePageDoes(t *testing.T) {
	second := make(chan struct{})
	params := SocketConnectParams{
		ID:            "recv-" + t.Name(),
		ReceiveScript: `nd.message.payload = "tampered"; console.log(nd.message.payload); nd.vars.set("got", nd.message.payload)`,
	}
	events, _ := startScriptedTCP(t, params, func(conn net.Conn) {
		_, _ = conn.Write([]byte("one"))
		<-second
		_, _ = conn.Write([]byte("two"))
	})

	events.waitFor(t, "第一条消息的 script 事件", func(evs []SocketEvent) bool {
		return len(socketReports(evs)) >= 1
	})
	close(second)
	all := events.waitFor(t, "第二条消息的 script 事件", func(evs []SocketEvent) bool {
		return len(socketReports(evs)) >= 2
	})

	messages := receivedMessages(all)
	if len(messages) != 2 {
		t.Fatalf("received %d messages, want both", len(messages))
	}
	if got, want := messages[0].Payload, "one"; got != want {
		t.Errorf("first message = %q, want %q as it arrived", got, want)
	}
	// 顺序即到达顺序：每条消息先到页面，之后才是它的 script 事件。
	messageAt := indexOfSocketEvent(all, func(ev SocketEvent) bool { return ev.Type == "message" && ev.Direction == "receive" })
	scriptAt := indexOfSocketEvent(all, func(ev SocketEvent) bool { return ev.Type == "script" })
	if messageAt < 0 || scriptAt < 0 || messageAt > scriptAt {
		t.Fatalf("events are %s, want the message before its script report", describeSocketEvents(all))
	}

	reports := socketReports(all)
	if reports[0].Phase != socketPhaseReceive || reports[0].Error != "" {
		t.Fatalf("report = %#v, want a successful receive report", reports[0])
	}
	// payload 只读：脚本读到的、留给下一个钩子的都是原值。
	if reports[0].Vars["got"] != "one" {
		t.Errorf("vars = %v, want the message as it arrived", reports[0].Vars)
	}
	if len(reports[0].Logs) != 1 || !strings.Contains(reports[0].Logs[0].Text, "one") {
		t.Errorf("logs = %#v, want the value the script read", reports[0].Logs)
	}
}

// 接收脚本崩溃不影响消息本身：两条消息都到页面，两条 script 事件各自带错误。
func TestReceiveScriptFailureKeepsTheMessagesComing(t *testing.T) {
	second := make(chan struct{})
	params := SocketConnectParams{
		ID:            "recv-crash-" + t.Name(),
		ReceiveScript: `throw new Error("boom")`,
	}
	events, _ := startScriptedTCP(t, params, func(conn net.Conn) {
		_, _ = conn.Write([]byte("one"))
		<-second
		_, _ = conn.Write([]byte("two"))
	})

	events.waitFor(t, "第一条消息的 script 事件", func(evs []SocketEvent) bool {
		return len(socketReports(evs)) >= 1
	})
	close(second)
	all := events.waitFor(t, "第二条消息的 script 事件", func(evs []SocketEvent) bool {
		return len(socketReports(evs)) >= 2
	})

	if messages := receivedMessages(all); len(messages) != 2 {
		t.Fatalf("received %d messages, want both despite the crashing script", len(messages))
	}
	for _, report := range socketReports(all) {
		if report.Phase != socketPhaseReceive || !strings.Contains(report.Error, "接收脚本出错：") {
			t.Fatalf("report = %#v, want a failing receive report", report)
		}
	}
}

// 脚本之间变量延续：预连接脚本写下的变量，发送脚本读得到，并且每次都随
// script 事件全量下发。
func TestSocketScriptVarsCarryBetweenRuns(t *testing.T) {
	events := &pageEvents{}
	params := SocketConnectParams{
		ID:         "vars-" + t.Name(),
		Vars:       map[string]string{"seed": "1"},
		PreScript:  `nd.vars.set("token", "t-" + nd.vars.get("seed"))`,
		SendScript: `nd.message.payload = nd.vars.get("token") + ":" + nd.message.payload`,
	}
	host := newSocketScriptHost(context.Background(), params, events.emit)
	t.Cleanup(host.cancel)

	if err := host.preConnect(&params); err != nil {
		t.Fatalf("preConnect: %v", err)
	}
	send := SocketSendParams{ID: params.ID, Payload: "ping"}
	if err := host.beforeSend(&send); err != nil {
		t.Fatalf("beforeSend: %v", err)
	}
	if got, want := send.Payload, "t-1:ping"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}

	reports := socketReports(events.snapshot())
	if len(reports) != 2 {
		t.Fatalf("script reports = %d, want one per hook", len(reports))
	}
	if reports[0].Vars["token"] != "t-1" {
		t.Errorf("pre vars = %v, want the token the script set", reports[0].Vars)
	}
	if reports[1].Vars["token"] != "t-1" || reports[1].Vars["seed"] != "1" {
		t.Errorf("send vars = %v, want the pre run's variables carried over", reports[1].Vars)
	}
}

// 页面关掉通道后，script 事件只是丢掉：脚本的记账照旧，读循环不会因此停下。
func TestSocketScriptReportOnAClosedChannelIsDropped(t *testing.T) {
	closed := func(SocketEvent) error { return mygo.ErrChannelClosed }
	params := SocketConnectParams{
		ID:         "closed-" + t.Name(),
		SendScript: `nd.vars.set("ran", "yes")`,
	}
	host := newSocketScriptHost(context.Background(), params, closed)
	t.Cleanup(host.cancel)

	send := SocketSendParams{ID: params.ID, Payload: "ping"}
	if err := host.beforeSend(&send); err != nil {
		t.Fatalf("beforeSend: %v", err)
	}
	if got := host.varsSnapshot(nil)["ran"]; got != "yes" {
		t.Fatalf("vars = %v, want the script's writes kept despite the closed channel", got)
	}

	// 消息本身仍然按原来的规则报错：通道关了，读循环就该结束。
	message := SocketEvent{Type: "message", Direction: "receive", Payload: "x"}
	if err := deliverSocketEvent(message, closed, host); !errors.Is(err, mygo.ErrChannelClosed) {
		t.Fatalf("deliverSocketEvent = %v, want the channel's error", err)
	}
}

// 取消（页面关闭或断开）能停下花在 Go 里的脚本，并按既有的消息报告。
func TestReceiveScriptCancelIsReported(t *testing.T) {
	events := &pageEvents{}
	params := SocketConnectParams{
		ID: "cancel-" + t.Name(),
		// 10M 次迭代要跑好几秒：取消落在中间，才说明是它让脚本停下来的。
		ReceiveScript: `nd.crypto.pbkdf2("password", "salt", 10000000, 32, "sha256")`,
	}
	host := newSocketScriptHost(context.Background(), params, events.emit)
	t.Cleanup(host.cancel)

	go func() {
		time.Sleep(50 * time.Millisecond)
		host.cancel()
	}()

	started := time.Now()
	host.received(SocketEvent{Type: "message", Direction: "receive", Payload: "x"})
	elapsed := time.Since(started)
	if elapsed > scriptTimeout {
		t.Fatalf("cancelling took %s: the Go-side loop did not watch its context", elapsed)
	}

	reports := socketReports(events.snapshot())
	if len(reports) != 1 {
		t.Fatalf("script reports = %d, want 1", len(reports))
	}
	if !strings.Contains(reports[0].Error, "接收脚本已取消") {
		t.Fatalf("report error = %q, want the cancelled message", reports[0].Error)
	}
}

// 没有脚本的连接不注册，Send 照旧直达 protocol：脚本不改变原来的行为。
func TestConnectionWithoutScriptsRegistersNoHost(t *testing.T) {
	params := SocketConnectParams{ID: "none-" + t.Name(), Kind: "tcp"}
	host := registerSocketScripts(context.Background(), params, func(SocketEvent) error { return nil })
	if host != nil {
		t.Fatalf("host = %#v, want none for a connection without scripts", host)
	}
	err := (SocketService{}).Send(SocketSendParams{ID: params.ID, Payload: "ping"})
	if err == nil || !strings.Contains(err.Error(), "no connection") {
		t.Fatalf("Send = %v, want protocol's own no-connection error", err)
	}
}

// 三个钩子共用 scriptFailure 的既有几条消息，kind 名与 HTTP 的短语同一套写法。
func TestSocketScriptFailureMessages(t *testing.T) {
	if got := scriptFailure(socketPreScriptKind, &ndapi.AbortError{Canceled: true}).Error(); got != "预连接脚本已取消" {
		t.Fatalf("cancelled pre failure = %q, want the cancelled message", got)
	}
	got := scriptFailure(socketSendScriptKind, &ndapi.AbortError{}).Error()
	if !strings.Contains(got, "发送脚本超时") || !strings.Contains(got, scriptTimeout.String()) {
		t.Fatalf("timed-out send failure = %q, want the timeout message", got)
	}
}
