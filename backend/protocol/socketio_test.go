package protocol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeSIO is a hand-written Engine.IO v4 / Socket.IO v5 server: just enough of
// the protocol to drive one client, with no Socket.IO dependency of its own.
type fakeSIO struct {
	t      *testing.T
	refuse bool // answer the CONNECT with CONNECT_ERROR
	eio3   bool // Socket.IO v2: the client pings, the CONNECT has no session id
	// The heartbeat the handshake offers, in milliseconds; 0 keeps the numbers
	// Socket.IO ships with.
	pingInterval int
	pingTimeout  int
	// onEvent answers an EVENT the client sent; it runs on the server's
	// goroutine.
	onEvent func(packet sioPacket)

	server *httptest.Server

	mu      sync.Mutex
	ws      *websocket.Conn
	path    string
	query   string
	connect string

	connected chan struct{} // closed once the CONNECT was answered
	pongs     chan string   // the heartbeats the client sent
	events    chan string   // the raw EVENT packets the client sent
}

// newFakeSIO serves srv and stops it when the test ends.
func newFakeSIO(t *testing.T, srv *fakeSIO) *fakeSIO {
	t.Helper()
	srv.t = t
	srv.connected = make(chan struct{})
	srv.pongs = make(chan string, 64)
	srv.events = make(chan string, 64)

	upgrader := websocket.Upgrader{}
	srv.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading the Socket.IO connection failed: %v", err)
			return
		}
		srv.mu.Lock()
		srv.path, srv.query = r.URL.Path, r.URL.RawQuery
		srv.mu.Unlock()
		srv.serve(conn)
	}))
	t.Cleanup(srv.server.Close)
	return srv
}

// serve speaks the server side of one connection.
func (s *fakeSIO) serve(conn *websocket.Conn) {
	s.mu.Lock()
	s.ws = conn
	s.mu.Unlock()
	defer conn.Close()

	interval, timeout := s.pingInterval, s.pingTimeout
	if interval == 0 {
		interval = 25000
	}
	if timeout == 0 {
		timeout = 5000
	}
	s.write(fmt.Sprintf(`0{"sid":"srv","upgrades":[],"pingInterval":%d,"pingTimeout":%d}`, interval, timeout))

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		text := string(data)
		switch {
		case text == "2": // an EIO3 client pings
			s.signal(s.pongs, text)
			s.write("3")
		case text == "3": // an EIO4 client pongs
			s.signal(s.pongs, text)
		case strings.HasPrefix(text, "40"):
			s.mu.Lock()
			s.connect = text
			s.mu.Unlock()
			if s.refuse {
				if s.eio3 {
					// Socket.IO v2 answers a bad namespace without a type digit.
					s.write(`4{"message":"Invalid namespace"}`)
				} else {
					s.write(`44{"message":"Invalid namespace"}`)
				}
				return
			}
			if s.eio3 {
				s.write("40")
			} else {
				s.write(`40{"sid":"sess"}`)
			}
			close(s.connected)
		case strings.HasPrefix(text, "41"):
			return
		case strings.HasPrefix(text, "42"):
			s.signal(s.events, text)
			if s.onEvent != nil {
				packet, ok := parseSIO(text)
				if !ok {
					return
				}
				s.onEvent(packet)
			}
		}
	}
}

// write sends one text packet; the test goroutine may call it too.
func (s *fakeSIO) write(text string) {
	s.writeFrame(websocket.TextMessage, []byte(text))
}

// writeBinary sends one binary frame.
func (s *fakeSIO) writeBinary(data []byte) {
	s.writeFrame(websocket.BinaryMessage, data)
}

// writeFrame sends one WebSocket frame, whoever calls it.
func (s *fakeSIO) writeFrame(messageType int, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws == nil {
		return
	}
	_ = s.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := s.ws.WriteMessage(messageType, data); err != nil {
		s.t.Logf("the fake server failed to write %q: %v", data, err)
	}
}

// signal records one packet the client sent, dropping it when the test is not
// reading (the assertions look for one packet at a time).
func (s *fakeSIO) signal(ch chan string, text string) {
	select {
	case ch <- text:
	default:
	}
}

// handshake reports what the upgrade request carried and the CONNECT packet the
// client sent.
func (s *fakeSIO) handshake() (path, query, connect string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path, s.query, s.connect
}

// eventLog is the sink a test reads events from.
type eventLog struct {
	events chan SocketEvent
}

func newEventLog() *eventLog {
	return &eventLog{events: make(chan SocketEvent, 64)}
}

func (l *eventLog) Send(ev SocketEvent) error {
	l.events <- ev
	return nil
}

// next waits for the next event, failing the test when none arrives.
func (l *eventLog) next(t *testing.T) SocketEvent {
	t.Helper()
	select {
	case ev := <-l.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no socket event arrived")
		return SocketEvent{}
	}
}

// connector runs Connect in the background, since it stays for the life of the
// connection.
type connector struct {
	done chan struct{}
	err  error
}

// startConnect opens the connection params names and closes it when the test
// ends.
func startConnect(t *testing.T, params ConnectParams, sink Sink) *connector {
	t.Helper()
	c := &connector{done: make(chan struct{})}
	go func() {
		c.err = Connect(context.Background(), params, sink)
		close(c.done)
	}()
	t.Cleanup(func() {
		Disconnect(params.ID)
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("Connect did not return after Disconnect")
		}
	})
	return c
}

// wait returns what Connect answered, failing the test when it never returned.
func (c *connector) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-c.done:
		return c.err
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return")
		return nil
	}
}

// openSocketIO connects params and waits for the open event.
func openSocketIO(t *testing.T, srv *fakeSIO, params ConnectParams, sink *eventLog) *connector {
	t.Helper()
	c := startConnect(t, params, sink)
	ev := sink.next(t)
	if ev.Type != "open" {
		t.Fatalf("first event is %q (%s), want open", ev.Type, ev.Reason)
	}
	return c
}

func TestSocketIOConnect(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	sink := newEventLog()
	c := openSocketIO(t, srv, ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}, sink)

	<-srv.connected
	path, query, connect := srv.handshake()
	if path != "/socket.io/" {
		t.Errorf("handshake path is %q, want /socket.io/", path)
	}
	for _, want := range []string{"EIO=4", "transport=websocket"} {
		if !strings.Contains(query, want) {
			t.Errorf("handshake query %q does not carry %s", query, want)
		}
	}
	if connect != "40" {
		t.Errorf("CONNECT packet is %q, want 40", connect)
	}

	Disconnect(t.Name())
	if err := c.wait(t); err != nil {
		t.Fatalf("Connect returned %v after Disconnect, want nil", err)
	}
}

func TestSocketIONamespaceAndPath(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	sink := newEventLog()
	params := ConnectParams{
		ID:     t.Name(),
		Kind:   "socketio",
		URL:    srv.server.URL + "/admin/",
		Path:   "custom",
		Params: []KeyValue{{Key: "token", Value: "abc"}, {Key: "EIO", Value: "9"}},
	}
	openSocketIO(t, srv, params, sink)

	<-srv.connected
	path, query, connect := srv.handshake()
	if path != "/custom/" {
		t.Errorf("handshake path is %q, want /custom/", path)
	}
	if connect != "40/admin," {
		t.Errorf("CONNECT packet is %q, want 40/admin,", connect)
	}
	if !strings.Contains(query, "token=abc") {
		t.Errorf("handshake query %q does not carry the page's parameter", query)
	}
	// The page's own EIO cannot replace the one the version setting asks for.
	if !strings.Contains(query, "EIO=4") || strings.Contains(query, "EIO=9") {
		t.Errorf("handshake query %q does not pin EIO=4", query)
	}
}

func TestSocketIOReceivesEvent(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	sink := newEventLog()
	openSocketIO(t, srv, ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}, sink)
	<-srv.connected

	// An EIO4 server pings, and the client pongs right away.
	srv.write("2")
	select {
	case pong := <-srv.pongs:
		if pong != "3" {
			t.Errorf("client answered a ping with %q, want 3", pong)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not answer the server's ping")
	}

	srv.write(`42["greet","hi"]`)
	ev := sink.next(t)
	if ev.Type != "message" || ev.Direction != "receive" {
		t.Fatalf("event is %q/%q, want message/receive", ev.Type, ev.Direction)
	}
	if ev.Event != "greet" {
		t.Errorf("event name is %q, want greet", ev.Event)
	}
	if ev.Payload != `["hi"]` {
		t.Errorf("payload is %q, want [\"hi\"]", ev.Payload)
	}
}

func TestSocketIOSendWithAck(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	srv.onEvent = func(packet sioPacket) {
		srv.write("43" + packet.id + `["pong"]`)
	}
	sink := newEventLog()
	openSocketIO(t, srv, ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}, sink)
	<-srv.connected

	if err := Send(SendParams{ID: t.Name(), Event: "ping", Payload: `{"x":1}`, Ack: true}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	sent := sink.next(t)
	if sent.Type != "message" || sent.Direction != "send" || sent.Event != "ping" {
		t.Fatalf("send event is %+v, want message/send of ping", sent)
	}

	select {
	case raw := <-srv.events:
		if raw != `421["ping",{"x":1}]` {
			t.Errorf("event packet is %q, want 421[\"ping\",{\"x\":1}]", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server received no event")
	}

	ack := sink.next(t)
	if ack.Type != "message" || ack.Direction != "receive" || !ack.IsAck {
		t.Fatalf("ack event is %+v, want a receive ack", ack)
	}
	if ack.Event != "ping" || ack.Payload != `["pong"]` {
		t.Errorf("ack is %q with payload %q, want ping with [\"pong\"]", ack.Event, ack.Payload)
	}
}

func TestSocketIOServerDisconnect(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	sink := newEventLog()
	c := openSocketIO(t, srv, ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}, sink)
	<-srv.connected

	srv.write("41")
	ev := sink.next(t)
	if ev.Type != "close" {
		t.Fatalf("event is %q after the server's DISCONNECT, want close", ev.Type)
	}
	if err := c.wait(t); err != nil {
		t.Fatalf("Connect returned %v, want nil after a server disconnect", err)
	}
}

func TestSocketIOConnectError(t *testing.T) {
	for _, tc := range []struct {
		name string
		eio3 bool
	}{{"eio4", false}, {"eio3", true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeSIO(t, &fakeSIO{refuse: true, eio3: tc.eio3})
			sink := newEventLog()
			params := ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}
			if tc.eio3 {
				params.Version = "3"
			}
			c := startConnect(t, params, sink)

			ev := sink.next(t)
			if ev.Type != "error" {
				t.Fatalf("first event is %q, want error", ev.Type)
			}
			if !strings.Contains(ev.Reason, "Invalid namespace") {
				t.Errorf("error reason is %q, want the server's message", ev.Reason)
			}
			if err := c.wait(t); err == nil {
				t.Fatal("Connect returned nil, want the refused handshake as an error")
			}
		})
	}
}

func TestSocketIOEIO3(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{eio3: true, pingInterval: 100, pingTimeout: 2000})
	sink := newEventLog()
	params := ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL, Version: "3"}
	openSocketIO(t, srv, params, sink)

	<-srv.connected
	_, query, connect := srv.handshake()
	if !strings.Contains(query, "EIO=3") {
		t.Errorf("handshake query %q does not ask for EIO=3", query)
	}
	if connect != "40" {
		t.Errorf("CONNECT packet is %q, want a bare 40", connect)
	}

	// EIO3 reverses the heartbeat: the client pings on its own.
	select {
	case ping := <-srv.pongs:
		if ping != "2" {
			t.Errorf("client heartbeat is %q, want 2", ping)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the EIO3 client never pinged")
	}

	srv.write(`42["tick",1]`)
	ev := sink.next(t)
	if ev.Type != "message" || ev.Event != "tick" || ev.Payload != `[1]` {
		t.Fatalf("event is %+v, want message tick [1]", ev)
	}
}

func TestSocketIOBinaryPacketPlaceholder(t *testing.T) {
	srv := newFakeSIO(t, &fakeSIO{})
	sink := newEventLog()
	openSocketIO(t, srv, ConnectParams{ID: t.Name(), Kind: "socketio", URL: srv.server.URL}, sink)
	<-srv.connected

	// A binary packet announces its attachments and never carries them here.
	srv.write(`451-["bin",{"_placeholder":true,"num":0}]`)
	ev := sink.next(t)
	if ev.Type != "message" || ev.Direction != "receive" || ev.Event != "bin" {
		t.Fatalf("event is %+v, want a message of the bin event", ev)
	}
	if !strings.Contains(ev.Payload, `"binary":true`) || !strings.Contains(ev.Payload, `"attachments":1`) {
		t.Errorf("payload is %q, want the placeholder JSON", ev.Payload)
	}

	// The attachment frame that follows is skipped, so the next event is the
	// one after it, not the bytes.
	srv.writeBinary(append([]byte{4}, []byte("raw-bytes")...))
	srv.write(`42["after","x"]`)
	next := sink.next(t)
	if next.Event != "after" {
		t.Fatalf("event after the binary frame is %+v, want the after event", next)
	}
}

func TestSIOEventData(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    string
	}{
		{`{"a":1}`, `["ev",{"a":1}]`},
		{`123`, `["ev",123]`},
		{`null`, `["ev",null]`},
		{`not json`, `["ev","not json"]`},
		{``, `["ev",""]`},
	} {
		got, err := sioEventData("ev", tc.payload)
		if err != nil {
			t.Fatalf("encoding %q failed: %v", tc.payload, err)
		}
		if got != tc.want {
			t.Errorf("encoding %q gives %s, want %s", tc.payload, got, tc.want)
		}
	}
}

func TestSIOEventArgs(t *testing.T) {
	name, args, ok := sioEventArgs(`["greet",{"a": 1}, [2]]`)
	if !ok {
		t.Fatal("parsing an EVENT payload failed")
	}
	if name != "greet" {
		t.Errorf("event name is %q, want greet", name)
	}
	// The arguments keep the server's own spelling of each value (the
	// separators between them are the parser's).
	if args != `[{"a": 1},[2]]` {
		t.Errorf("arguments are %q, want the JSON the server sent", args)
	}
	if name, args, ok = sioEventArgs(`["alone"]`); !ok || name != "alone" || args != "[]" {
		t.Errorf("a single-argument event reads as %q %q %v", name, args, ok)
	}
	if _, _, ok := sioEventArgs(`{"not":"an array"}`); ok {
		t.Error("an object parsed as an EVENT payload")
	}
}

func TestParseSIOPacket(t *testing.T) {
	packet, ok := parseSIO(`42/admin,7["x",1]`)
	if !ok {
		t.Fatal("parsing an event packet failed")
	}
	if packet.sioType != '2' || packet.nsp != "/admin" || packet.id != "7" || packet.payload != `["x",1]` {
		t.Errorf("packet is %+v, want type 2 in /admin with id 7", packet)
	}
	if packet, ok = parseSIO(`44{"message":"no"}`); !ok || packet.sioType != '4' || !packet.hasType {
		t.Errorf("CONNECT_ERROR packet is %+v", packet)
	}
	if packet, ok = parseSIO(`4{"message":"no"}`); !ok || packet.hasType {
		t.Errorf("EIO3 error packet is %+v, want no type digit", packet)
	}
	// A binary packet leads with its attachment count, which is not an ack id.
	if packet, ok = parseSIO(`451-["ev",{"_placeholder":true}]`); !ok || packet.sioType != '5' ||
		packet.id != "" || packet.payload != `1-["ev",{"_placeholder":true}]` {
		t.Errorf("binary packet is %+v", packet)
	}
	if _, ok = parseSIO(`2`); ok {
		t.Error("an Engine.IO ping parsed as a Socket.IO packet")
	}
}
