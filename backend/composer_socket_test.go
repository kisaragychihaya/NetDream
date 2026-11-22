package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// waitFor 等到一次运行的事件满足 cond：连接都在后台，等的是事件而不是墙钟。
func (p *composerEvents) waitFor(t *testing.T, what string, cond func([]ComposerEvent) bool) []ComposerEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events := p.snapshot()
		if cond(events) {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待%s超时，已有事件 %s", what, describeComposerEvents(events))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// wsSeen 记下一条 WebSocket 握手带来的查询串和请求头：脚本给的 params 与 headers
// 只有服务端看得见。
type wsSeen struct {
	mu      sync.Mutex
	query   string
	headers http.Header
}

func (s *wsSeen) set(query string, header http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query, s.headers = query, header.Clone()
}

func (s *wsSeen) snapshot() (string, http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headers == nil {
		return s.query, nil
	}
	return s.query, s.headers.Clone()
}

// wsEchoServer 起一个协商 sub 子协议、把收到的每条消息原样送回的服务。
func wsEchoServer(t *testing.T, sub string) (*httptest.Server, *wsSeen) {
	t.Helper()
	seen := &wsSeen{}
	upgrader := websocket.Upgrader{Subprotocols: []string{sub}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		seen.set(r.URL.RawQuery, r.Header)
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// wsURL turns a test server's http address into the ws one a script opens.
func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

// tcpEchoServer 起一个把收到的字节原样送回的 TCP 服务，返回 host:port。
func tcpEchoServer(t *testing.T) string {
	t.Helper()
	return listenTCP(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})
}

// silentTCPServer 起一个只收不发的 TCP 服务：连上之后永远没有消息，用来验证
// receive 的超时与取消。closed 在对端断开时收到一次信号。
func silentTCPServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{}, 1)
	addr := listenTCP(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
		select {
		case closed <- struct{}{}:
		default:
		}
	})
	return addr, closed
}

// handshakeSilentServer 接受 TCP 连接但从不回 WebSocket 握手：拨号会一直等下去，
// 正好用来证明 open 不等它。
func handshakeSilentServer(t *testing.T) string {
	t.Helper()
	addr, _ := silentTCPServer(t)
	return addr
}

// listenTCP 在 127.0.0.1 的随机端口上起一个服务，每条连接交给 serve 处理。
func listenTCP(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				serve(conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// sioSeen 记下 Socket.IO 握手请求的路径与查询串。
type sioSeen struct {
	mu    sync.Mutex
	path  string
	query string
}

func (s *sioSeen) snapshot() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path, s.query
}

// socketioEchoServer 是一个最小的 Socket.IO v4 服务：Engine.IO 握手、命名空间
// CONNECT、一次事件回显和它对应的 ack。够验证 composer 的 socketio 收发。
func socketioEchoServer(t *testing.T) (*httptest.Server, *sioSeen) {
	t.Helper()
	seen := &sioSeen{}
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		seen.mu.Lock()
		seen.path, seen.query = r.URL.Path, r.URL.RawQuery
		seen.mu.Unlock()

		write := func(text string) bool {
			return ws.WriteMessage(websocket.TextMessage, []byte(text)) == nil
		}
		if !write(`0{"sid":"srv","upgrades":[],"pingInterval":25000,"pingTimeout":5000}`) {
			return
		}
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			text := string(data)
			switch {
			case text == "40/admin,":
				if !write(`40/admin,{"sid":"sess"}`) {
					return
				}
			case strings.HasPrefix(text, "42/admin,"):
				// 客户端事件带可选的自增 ack id：先答事件，再答 ack。
				rest := strings.TrimPrefix(text, "42/admin,")
				i := 0
				for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
					i++
				}
				id := rest[:i]
				if !write(`42/admin,["chat","echo"]`) {
					return
				}
				if id != "" && !write(`43/admin,`+id+`["ack"]`) {
					return
				}
			case text == "41/admin,":
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// 脚本用 open 拿句柄、用 receive 取事件、用 send 发消息：WS 的握手响应头与协商出的
// 子协议在 open 事件里，自己发出去的消息不会被 poll 取回。
func TestComposerWebSocketLoopback(t *testing.T) {
	srv, seen := wsEchoServer(t, "sub")

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "ws-" + t.Name(),
		Script: `
			var c = nd.composer.ws.open("` + wsURL(srv, "/echo") + `", {
				params: {room: "7"},
				headers: {"X-Token": "t"},
				protocol: "sub"
			});
			nd.vars.set("kind", c.kind);
			nd.vars.set("idOk", String(c.id.indexOf("composer-") === 0));
			var open = c.receive(5000);
			nd.vars.set("openType", open.type);
			nd.vars.set("openProtocol", open.protocol);
			nd.vars.set("hasHeaders", String(open.headers.length > 0));
			nd.vars.set("headerShape", String(typeof open.headers[0].key === "string" && typeof open.headers[0].value === "string"));
			c.send("ping");
			var echo = c.receive(5000);
			nd.vars.set("echoType", echo.type);
			nd.vars.set("echo", echo.payload);
			nd.vars.set("pollEmpty", String(c.poll() === null));
			c.close();
			c.close();
			nd.vars.set("closedTwice", "yes");
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("result = %#v, want no reason", res)
	}

	all := events.snapshot()
	if last := all[len(all)-1]; last.Type != "done" {
		t.Fatalf("last event = %s, want done（事件流 %s）", last.Type, describeComposerEvents(all))
	}
	vars := composerDone(t, all).Vars
	for name, want := range map[string]string{
		"kind":         composerKindWS,
		"idOk":         "true",
		"openType":     "open",
		"openProtocol": "sub",
		"hasHeaders":   "true",
		"headerShape":  "true",
		"echoType":     "message",
		"echo":         "ping",
		"pollEmpty":    "true",
		"closedTwice":  "yes",
	} {
		if got := vars[name]; got != want {
			t.Errorf("vars[%q] = %q, want %q（事件流 %s）", name, got, want, describeComposerEvents(all))
		}
	}

	// 连接的生命周期进运行日志，消息不进：脚本 receive 到的才算消息。
	if !hasComposerLog(all, "["+composerKindWS+"] open ") {
		t.Errorf("logs = %v, want the ws lifecycle line", composerLogLines(all))
	}
	for _, line := range composerLogLines(all) {
		if strings.Contains(line, "ping") {
			t.Errorf("log line %q carries a message: only lifecycle events belong in the run's log", line)
		}
	}

	query, header := seen.snapshot()
	if header == nil {
		t.Fatal("服务端没收到握手")
	}
	if !strings.Contains(query, "room=7") {
		t.Errorf("handshake query = %q, want the script's param", query)
	}
	if got, want := header.Get("X-Token"), "t"; got != want {
		t.Errorf("handshake X-Token = %q, want %q", got, want)
	}
	if got, want := header.Get("Sec-WebSocket-Protocol"), "sub"; got != want {
		t.Errorf("handshake subprotocol = %q, want %q", got, want)
	}
}

// open 不等拨号：服务端连握手都不回，脚本照样立刻拿回句柄，没有事件可取就拿到
// null，而不是把运行卡到超时。
func TestComposerSocketOpenDoesNotBlock(t *testing.T) {
	addr := handshakeSilentServer(t)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:      "open-" + t.Name(),
		Timeout: 5,
		Script: `
			var started = Date.now();
			var c = nd.composer.ws.open("ws://` + addr + `/x");
			nd.vars.set("pollEmpty", String(c.poll() === null));
			nd.vars.set("timedOut", String(c.receive(100) === null));
			nd.vars.set("quick", String(Date.now() - started < 4000));
			c.close();
		`,
	}

	began := time.Now()
	res, err := runComposer(context.Background(), params, events.emit)
	elapsed := time.Since(began)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	// open 阻塞的话运行会在 5s 的运行超时上结束，Reason 就不是空的。
	if res.Reason != "" {
		t.Fatalf("result = %#v, want no reason（open 阻塞了？）", res)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("运行耗时 %s：open 一直在等拨号", elapsed)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if vars["pollEmpty"] != "true" || vars["timedOut"] != "true" || vars["quick"] != "true" {
		t.Fatalf("vars = %v, want an empty buffer and a quick return", vars)
	}
}

// receive 的超时到点就返回 null，连接还开着：它是这次拉取的边界，不是连接的状态。
// 脚本自己不 close，运行结束时也一定要把连接关掉。
func TestComposerSocketReceiveTimesOut(t *testing.T) {
	addr, closed := silentTCPServer(t)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "timeout-" + t.Name(),
		Script: `
			var t = nd.composer.tcp.open("` + addr + `");
			var open = t.receive(5000);
			nd.vars.set("openType", open.type);
			nd.vars.set("kind", t.kind);
			nd.vars.set("idIndex", String(t.id.indexOf("composer-")));
			nd.vars.set("nothing", String(t.receive(100) === null));
			nd.vars.set("pollAfter", String(t.poll() === null));
		`,
	}

	if _, err := runComposer(context.Background(), params, events.emit); err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if vars["openType"] != "open" {
		t.Fatalf("first event = %q, want open", vars["openType"])
	}
	if vars["nothing"] != "true" || vars["pollAfter"] != "true" {
		t.Fatalf("vars = %v, want null from a silent connection", vars)
	}
	if got, want := vars["kind"], composerKindTCP; got != want {
		t.Errorf("handle kind = %q, want %q", got, want)
	}
	if got, want := vars["idIndex"], "0"; got != want {
		t.Errorf("handle id index = %q, want a composer-scoped id (%q)", got, want)
	}
	// 脚本没有 close：连接归这次运行所有，Run 返回前必须已经关掉。
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("运行结束后连接还开着")
	}
}

// TCP 与 WS 是同一套句柄：send 之后 receive 到的是对端的回显，自己发的那条不会被
// poll 取回。
func TestComposerTCPSendEcho(t *testing.T) {
	addr := tcpEchoServer(t)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "tcp-echo-" + t.Name(),
		Script: `
			var t = nd.composer.tcp.open("` + addr + `");
			nd.vars.set("openType", t.receive(5000).type);
			t.send("ping");
			var echo = t.receive(5000);
			nd.vars.set("echoType", echo.type);
			nd.vars.set("echo", echo.payload);
			nd.vars.set("pollEmpty", String(t.poll() === null));
			t.close();
		`,
	}

	if _, err := runComposer(context.Background(), params, events.emit); err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	vars := composerDone(t, events.snapshot()).Vars
	if vars["openType"] != "open" || vars["echoType"] != "message" {
		t.Fatalf("events = %v, want open then a message", vars)
	}
	if got, want := vars["echo"], "ping"; got != want {
		t.Errorf("echo = %q, want %q", got, want)
	}
	if vars["pollEmpty"] != "true" {
		t.Error("poll 取回了自己发出去的消息")
	}
}

// 拨号失败不抛脚本错误：它和别的生命周期事件一样进缓冲，脚本的 receive 读到一条
// error，运行自己的日志用 warn 记下来。
func TestComposerSocketDialFailureIsAnEvent(t *testing.T) {
	// 先占一个端口再放掉，拿到一个确定没人听的地址。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "dial-" + t.Name(),
		Script: `
			var c = nd.composer.ws.open("ws://` + addr + `/x");
			var ev = c.receive(5000);
			nd.vars.set("type", ev.type);
			nd.vars.set("hasReason", String(ev.reason.length > 0));
			nd.vars.set("after", String(c.receive(100) === null));
			c.close();
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("result = %#v, want the failure to stay out of the script's way", res)
	}
	all := events.snapshot()
	vars := composerDone(t, all).Vars
	if got, want := vars["type"], "error"; got != want {
		t.Fatalf("first event = %q, want %q (%v)", got, want, vars)
	}
	if vars["hasReason"] != "true" {
		t.Error("error 事件没有原因")
	}
	if vars["after"] != "true" {
		t.Error("连接结束后 receive 还取到了事件")
	}
	var warned bool
	for _, ev := range all {
		if ev.Type == "log" && ev.Level == "warn" && strings.Contains(ev.Text, "["+composerKindWS+"] error: ") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("events = %s, want a warn line for the failed connection", describeComposerEvents(all))
	}
}

// 停掉运行要让阻塞在 receive 里的脚本立刻醒来：既不能等运行超时，也不能让脚本在
// 取消后继续往下跑。
func TestComposerStopWakesABlockedReceive(t *testing.T) {
	addr, _ := silentTCPServer(t)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:      "wake-" + t.Name(),
		Timeout: 30,
		Script: `
			var t = nd.composer.tcp.open("` + addr + `");
			t.receive(5000);
			t.receive();
			// 醒来之后这条 Go 侧的长调用也不该跑完：取消已经生效。
			nd.crypto.pbkdf2("password", "salt", 10000000, 32, "sha256");
			nd.vars.set("slow", "yes");
		`,
	}

	type outcome struct {
		res ComposerResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := runComposer(context.Background(), params, events.emit)
		done <- outcome{res: res, err: err}
	}()

	// 连接真的开了、阻塞的 receive 也已经在等：日志是它开好的证据。
	events.waitFor(t, "连接打开", func(list []ComposerEvent) bool {
		return hasComposerLog(list, "["+composerKindTCP+"] open ")
	})
	ComposerService{}.Stop(params.ID)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("runComposer: %v", got.err)
		}
		if !strings.Contains(got.res.Reason, "已取消") {
			t.Fatalf("result = %#v, want the cancelled reason", got.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 之后运行没有结束：阻塞的 receive 没有醒来")
	}
	if vars := composerDone(t, events.snapshot()).Vars; vars["slow"] == "yes" {
		t.Fatal("脚本在取消之后跑完了后续的耗时调用")
	}
}

// socketio 走命名空间与路径：open 的路径是握手路径，URL 的 path 是命名空间；事件的
// event / isAck 字段让脚本分得清服务器发的事件和它自己那次 send 的 ack。
func TestComposerSocketIOEventAndAck(t *testing.T) {
	srv, seen := socketioEchoServer(t)

	events := &composerEvents{}
	params := ComposerRunParams{
		ID: "sio-" + t.Name(),
		Script: `
			var s = nd.composer.socketio.open("` + srv.URL + `/admin", {path: "/socket.io/", version: "4"});
			var open = s.receive(5000);
			nd.vars.set("openType", open.type);
			s.send("hi", {event: "chat", ack: true});
			var event = s.receive(5000);
			nd.vars.set("event", event.event);
			nd.vars.set("eventIsAck", String(event.isAck));
			nd.vars.set("eventPayload", event.payload);
			var ack = s.receive(5000);
			nd.vars.set("ackIsAck", String(ack.isAck));
			nd.vars.set("ackEvent", ack.event);
			nd.vars.set("ackPayload", ack.payload);
			s.close();
		`,
	}

	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("result = %#v, want no reason", res)
	}

	vars := composerDone(t, events.snapshot()).Vars
	if vars["openType"] != "open" {
		t.Fatalf("first event = %q, want open（%v）", vars["openType"], vars)
	}
	if vars["event"] != "chat" || vars["eventIsAck"] != "false" {
		t.Errorf("first message = %q/%q, want the chat event", vars["event"], vars["eventIsAck"])
	}
	if !strings.Contains(vars["eventPayload"], "echo") {
		t.Errorf("event payload = %q, want the server's argument", vars["eventPayload"])
	}
	if vars["ackIsAck"] != "true" || vars["ackEvent"] != "chat" {
		t.Errorf("second message = %q/%q, want the ack for chat", vars["ackEvent"], vars["ackIsAck"])
	}
	if !strings.Contains(vars["ackPayload"], "ack") {
		t.Errorf("ack payload = %q, want the server's ack argument", vars["ackPayload"])
	}

	path, query := seen.snapshot()
	if !strings.Contains(path, "/socket.io/") {
		t.Errorf("handshake path = %q, want the path option", path)
	}
	if !strings.Contains(query, "EIO=4") || !strings.Contains(query, "transport=websocket") {
		t.Errorf("handshake query = %q, want the Engine.IO v4 parameters", query)
	}
}
