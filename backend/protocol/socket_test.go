package protocol

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// socketLog is the sink these tests read events from; unlike eventLog it never
// blocks, since a cancelled connection may still be sending its close.
type socketLog struct {
	mu     sync.Mutex
	events []SocketEvent
	opened chan struct{}
	once   sync.Once
}

func newSocketLog() *socketLog {
	return &socketLog{opened: make(chan struct{})}
}

func (l *socketLog) Send(ev SocketEvent) error {
	l.mu.Lock()
	l.events = append(l.events, ev)
	l.mu.Unlock()
	if ev.Type == "open" {
		l.once.Do(func() { close(l.opened) })
	}
	return nil
}

func (l *socketLog) types() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.events))
	for _, ev := range l.events {
		out = append(out, ev.Type)
	}
	return out
}

// waitOpen blocks until the connection reported itself open.
func (l *socketLog) waitOpen(t *testing.T) {
	t.Helper()
	select {
	case <-l.opened:
	case <-time.After(5 * time.Second):
		t.Fatalf("connection never opened (events: %v)", l.types())
	}
}

// connectAsync runs Connect with ctx and returns a channel carrying its result,
// so a cancel that never takes effect shows up as a timeout instead of a hang.
func connectAsync(ctx context.Context, params ConnectParams, sink Sink) <-chan error {
	done := make(chan error, 1)
	go func() { done <- Connect(ctx, params, sink) }()
	return done
}

// waitConnect returns what Connect answered within a few seconds.
func waitConnect(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return after its context was cancelled")
		return nil
	}
}

// echoServer is a WebSocket server that upgrades and then stays silent: the
// client's read has nothing to wake it but a closed transport.
func silentWebSocket(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebSocketCancelEndsConnect is the regression for a cancel that used to
// leave ReadMessage waiting on a socket nobody read: cancelling has to close the
// transport so Connect returns.
func TestWebSocketCancelEndsConnect(t *testing.T) {
	srv := silentWebSocket(t)
	sink := newSocketLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	params := ConnectParams{ID: t.Name(), Kind: "websocket", URL: "ws" + strings.TrimPrefix(srv.URL, "http")}
	done := connectAsync(ctx, params, sink)
	sink.waitOpen(t)

	cancel()
	if err := waitConnect(t, done); err != nil {
		t.Fatalf("Connect returned %v after a cancel, want nil", err)
	}
	// The connection is gone from the registry, and a second disconnect is a
	// no-op rather than a panic or a stale entry.
	if _, err := lookupConn(params.ID); err == nil {
		t.Error("connection is still registered after Connect returned")
	}
	Disconnect(params.ID)
	// A cancel is the caller's own doing: no close event claiming the other end
	// hung up.
	for _, typ := range sink.types() {
		if typ == "close" || typ == "error" {
			t.Errorf("events after a cancel are %v, want no close/error", sink.types())
		}
	}
}

// TestTCPCancelEndsConnect is the same regression for the plain TCP read loop.
func TestTCPCancelEndsConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening failed: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Hold the connection open without sending: only a closed transport
		// ends the client's Read.
		_, _ = conn.Read(make([]byte, 1))
		time.Sleep(time.Second)
	}()

	sink := newSocketLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	params := ConnectParams{ID: t.Name(), Kind: "tcp", URL: listener.Addr().String()}
	done := connectAsync(ctx, params, sink)
	sink.waitOpen(t)

	cancel()
	if err := waitConnect(t, done); err != nil {
		t.Fatalf("Connect returned %v after a cancel, want nil", err)
	}
	if _, err := lookupConn(params.ID); err == nil {
		t.Error("connection is still registered after Connect returned")
	}
}

// TestCancelDuringDial covers the other half: a cancel while the handshake is
// still in flight has to end Connect too, and report the cancel rather than
// pretending the connection opened.
func TestCancelDuringDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening failed: %v", err)
	}
	defer listener.Close()
	go func() {
		// Accept the TCP connection, then never answer the handshake.
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(2 * time.Second)
	}()

	sink := newSocketLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	params := ConnectParams{ID: t.Name(), Kind: "websocket", URL: "ws://" + listener.Addr().String()}
	done := connectAsync(ctx, params, sink)

	// Give the dial a moment to reach the server, then cancel mid-handshake.
	time.Sleep(100 * time.Millisecond)
	cancel()

	err = waitConnect(t, done)
	if err == nil {
		t.Fatal("Connect returned nil after a cancel during the dial")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		t.Fatalf("Connect returned %v, want a context cancellation", err)
	}
	if _, err := lookupConn(params.ID); err == nil {
		t.Error("connection is still registered after Connect returned")
	}
	for _, typ := range sink.types() {
		if typ == "open" {
			t.Errorf("events are %v, want no open event", sink.types())
			break
		}
	}
}

// TestDisconnectEndsConnect keeps the explicit path covered too: the page's
// disconnect has to end the read loop just like a cancel does.
func TestDisconnectEndsConnect(t *testing.T) {
	srv := silentWebSocket(t)
	sink := newSocketLog()
	params := ConnectParams{ID: t.Name(), Kind: "websocket", URL: "ws" + strings.TrimPrefix(srv.URL, "http")}
	done := connectAsync(context.Background(), params, sink)
	sink.waitOpen(t)

	Disconnect(params.ID)
	Disconnect(params.ID) // a connection already gone is not an error
	if err := waitConnect(t, done); err != nil {
		t.Fatalf("Connect returned %v after Disconnect, want nil", err)
	}
}
