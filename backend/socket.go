package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/gorilla/websocket"
)

// Bounds on a live connection.
const (
	socketDialTimeout = 30 * time.Second
	socketWriteTicks  = 30 * time.Second // a write that blocks this long fails instead
	socketReadLimit   = 10 << 20         // the largest WebSocket message read
	socketTCPReadSize = 64 << 10         // one TCP read, one event
)

// socketSink is where a connection pushes its events; the bound method's
// *mygo.Channel[SocketEvent] is the one sink there is.
type socketSink interface {
	Send(SocketEvent) error
}

// socketConn is one live connection: the registry finds it by id, and every
// write goes through its lock, since a WebSocket or TCP stream takes one
// writer at a time. Reads run in the goroutine of Connect.
type socketConn struct {
	id     string
	kind   string
	stream socketSink
	cancel context.CancelFunc

	mu      sync.Mutex
	ws      *websocket.Conn
	tcp     net.Conn
	headers []KeyValue // the WebSocket handshake response headers
	shut    bool       // closed by Disconnect or by Connect's return
}

// SocketService opens WebSocket and TCP connections the page talks over.
// Every exported method here is bound to the frontend, so helpers stay unexported.
type SocketService struct{}

// Connect opens the connection params names and stays until it closes or the
// page disconnects it, pushing what happens on stream as it happens.
func (SocketService) Connect(ctx context.Context, params SocketConnectParams, stream *mygo.Channel[SocketEvent]) error {
	return connectSocket(ctx, params, stream)
}

// Send writes one message to the connection params.ID names.
func (SocketService) Send(params SocketSendParams) error {
	conn, err := lookupSocket(params.ID)
	if err != nil {
		return err
	}
	return conn.send(params)
}

// Disconnect closes the connection id names; an id that is already gone is not
// an error.
func (SocketService) Disconnect(id string) {
	conn, err := lookupSocket(id)
	if err != nil {
		return
	}
	conn.stop()
}

// connectSocket is Connect over any sink, so that a test can drive a
// connection without the IPC binder. The page's channel is the real sink.
func connectSocket(ctx context.Context, params SocketConnectParams, sink socketSink) error {
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return fmt.Errorf("a connection needs an id")
	}
	kind := strings.ToLower(strings.TrimSpace(params.Kind))
	switch kind {
	case "websocket", "tcp":
	default:
		return fmt.Errorf("unknown connection kind %q: use \"websocket\" or \"tcp\"", params.Kind)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The entry is registered before dialing, so that Disconnect reaches a
	// connection that is still being opened.
	conn := &socketConn{id: id, kind: kind, stream: sink, cancel: cancel}
	if err := registerSocket(conn); err != nil {
		return err
	}
	defer func() {
		unregisterSocket(conn)
		conn.stop()
	}()

	var code int
	var err error
	if kind == "websocket" {
		code, err = conn.dialWebSocket(ctx, params)
	} else {
		err = conn.dialTCP(ctx, params)
	}
	if err != nil {
		if ctx.Err() == nil && !conn.isShut() {
			_ = conn.stream.Send(SocketEvent{Type: "error", Code: code, Reason: err.Error(), At: time.Now().UnixMilli()})
		}
		return err
	}
	if conn.isShut() {
		// The page disconnected while the dial was in flight: stop() ran
		// before a transport existed, so close the one the dial produced.
		conn.stop()
		return nil
	}

	if err := conn.stream.Send(conn.openEvent()); err != nil {
		return err
	}
	if kind == "websocket" {
		return conn.readWebSocket()
	}
	return conn.readTCP()
}

// openEvent is what tells the page the connection is up.
func (c *socketConn) openEvent() SocketEvent {
	ev := SocketEvent{Type: "open", At: time.Now().UnixMilli()}
	if c.kind == "websocket" {
		ev.Headers = c.headers
		ev.Protocol = c.ws.Subprotocol()
	}
	return ev
}

// dialWebSocket opens a WebSocket connection; a refused handshake reports its
// HTTP status alongside the error.
func (c *socketConn) dialWebSocket(ctx context.Context, params SocketConnectParams) (int, error) {
	target, err := socketURL(params)
	if err != nil {
		return 0, err
	}

	header := http.Header{}
	for _, h := range params.Headers {
		if h.Key == "" || socketManagedHeader(h.Key) {
			continue
		}
		header.Add(h.Key, h.Value)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: socketDialTimeout,
		// 环境变量优先、系统代理兜底，与 HTTP 请求同一套规则。
		Proxy: proxyForRequest,
	}
	if protocols := socketProtocols(params.Protocol); len(protocols) > 0 {
		// The dialer writes this header itself, and refuses a copy of it.
		header.Del("Sec-WebSocket-Protocol")
		dialer.Subprotocols = protocols
	}

	ws, resp, err := dialer.DialContext(ctx, target, header)
	if err != nil {
		if resp != nil {
			return resp.StatusCode, fmt.Errorf("connecting to %s failed: %w", target, err)
		}
		return 0, fmt.Errorf("connecting to %s failed: %w", target, err)
	}
	ws.SetReadLimit(socketReadLimit)

	c.mu.Lock()
	c.ws = ws
	if resp != nil {
		c.headers = flattenHeaders(resp.Header)
	}
	c.mu.Unlock()
	return 0, nil
}

// dialTCP opens the TCP connection the page's host:port names.
func (c *socketConn) dialTCP(ctx context.Context, params SocketConnectParams) error {
	addr := strings.TrimSpace(params.URL)
	if addr == "" {
		return fmt.Errorf("a TCP connection needs a host and a port, like 127.0.0.1:8125")
	}
	dialer := net.Dialer{Timeout: socketDialTimeout}
	tcp, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connecting to %s failed: %w", addr, err)
	}

	c.mu.Lock()
	c.tcp = tcp
	c.mu.Unlock()
	return nil
}

// readWebSocket pushes every message until the connection ends, and tells the
// page how it ended unless the page ended it.
func (c *socketConn) readWebSocket() error {
	ws := c.ws
	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			if !c.isShut() {
				code, reason := 0, err.Error()
				var closed *websocket.CloseError
				if errors.As(err, &closed) {
					code, reason = closed.Code, closed.Text
				}
				c.closeEvent(code, reason)
			}
			return nil
		}

		ev := SocketEvent{Type: "message", Direction: "receive", At: time.Now().UnixMilli()}
		if msgType == websocket.BinaryMessage {
			ev.Payload = base64.StdEncoding.EncodeToString(data)
			ev.IsBinary = true
		} else {
			ev.Payload = string(data)
		}
		if err := c.stream.Send(ev); err != nil {
			return err
		}
	}
}

// readTCP pushes what the connection sends, one event per read.
func (c *socketConn) readTCP() error {
	buf := make([]byte, socketTCPReadSize)
	for {
		n, err := c.tcp.Read(buf)
		if n > 0 {
			ev := SocketEvent{Type: "message", Direction: "receive", At: time.Now().UnixMilli()}
			if utf8.Valid(buf[:n]) {
				ev.Payload = string(buf[:n])
			} else {
				ev.Payload = base64.StdEncoding.EncodeToString(buf[:n])
				ev.IsBinary = true
			}
			if err := c.stream.Send(ev); err != nil {
				return err
			}
		}
		if err != nil {
			if !c.isShut() {
				reason := ""
				if !errors.Is(err, io.EOF) {
					reason = err.Error()
				}
				c.closeEvent(0, reason)
			}
			return nil
		}
	}
}

// send writes one message, then reports it: the page logs what the backend
// confirmed rather than what it asked for.
func (c *socketConn) send(params SocketSendParams) error {
	payload := []byte(params.Payload)
	if params.IsBinary {
		data, err := base64.StdEncoding.DecodeString(params.Payload)
		if err != nil {
			return fmt.Errorf("decoding the base64 payload failed: %w", err)
		}
		payload = data
	}

	c.mu.Lock()
	if c.shut {
		c.mu.Unlock()
		return fmt.Errorf("connection %s is closed", c.id)
	}
	var err error
	switch c.kind {
	case "websocket":
		msgType := websocket.TextMessage
		if params.IsBinary {
			msgType = websocket.BinaryMessage
		}
		_ = c.ws.SetWriteDeadline(time.Now().Add(socketWriteTicks))
		err = c.ws.WriteMessage(msgType, payload)
	default:
		_ = c.tcp.SetWriteDeadline(time.Now().Add(socketWriteTicks))
		_, err = c.tcp.Write(payload)
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("sending on connection %s failed: %w", c.id, err)
	}

	return c.stream.Send(SocketEvent{
		Type:      "message",
		Direction: "send",
		Payload:   params.Payload,
		IsBinary:  params.IsBinary,
		At:        time.Now().UnixMilli(),
	})
}

// closeEvent tells the page the other end ended the connection.
func (c *socketConn) closeEvent(code int, reason string) {
	_ = c.stream.Send(SocketEvent{Type: "close", Code: code, Reason: reason, At: time.Now().UnixMilli()})
}

// stop closes the connection: a WebSocket says goodbye first, and closing the
// transport ends the read loop. It is safe to call repeatedly, and a call
// after the dial won a race with an earlier one still closes the transport
// the dial produced.
func (c *socketConn) stop() {
	c.mu.Lock()
	first := !c.shut
	c.shut = true
	ws, tcp := c.ws, c.tcp
	c.mu.Unlock()
	if ws != nil {
		if first {
			msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			_ = ws.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		}
		_ = ws.Close()
	}
	if tcp != nil {
		_ = tcp.Close()
	}
	c.cancel()
}

// isShut reports whether the page or Connect itself closed the connection.
func (c *socketConn) isShut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shut
}

// socketURL puts the query parameters on the WebSocket URL the page typed.
func socketURL(params SocketConnectParams) (string, error) {
	raw := strings.TrimSpace(params.URL)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss":
	default:
		return "", fmt.Errorf("invalid URL %q: it needs ws:// or wss://", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid URL %q: it needs a host", raw)
	}

	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", fmt.Errorf("invalid query string: %w", err)
	}
	for _, p := range params.Params {
		if p.Key == "" {
			continue
		}
		q.Add(p.Key, p.Value)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// socketProtocols splits the page's subprotocol list, one entry per protocol.
func socketProtocols(protocol string) []string {
	var out []string
	for _, p := range strings.Split(protocol, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// socketManagedHeader reports whether the dialer writes the header itself, so
// the page's copy must not reach it.
func socketManagedHeader(key string) bool {
	switch {
	case strings.EqualFold(key, "Host"),
		strings.EqualFold(key, "Upgrade"),
		strings.EqualFold(key, "Connection"),
		strings.EqualFold(key, "Sec-WebSocket-Key"),
		strings.EqualFold(key, "Sec-WebSocket-Version"),
		strings.EqualFold(key, "Sec-WebSocket-Extensions"):
		return true
	}
	return false
}

var (
	socketMu    sync.Mutex
	socketConns = map[string]*socketConn{}
)

// registerSocket takes id for conn, refusing one already in use.
func registerSocket(conn *socketConn) error {
	socketMu.Lock()
	defer socketMu.Unlock()
	if _, ok := socketConns[conn.id]; ok {
		return fmt.Errorf("a connection with id %s is already open", conn.id)
	}
	socketConns[conn.id] = conn
	return nil
}

// unregisterSocket forgets conn, unless its id has come to name another one.
func unregisterSocket(conn *socketConn) {
	socketMu.Lock()
	defer socketMu.Unlock()
	if socketConns[conn.id] == conn {
		delete(socketConns, conn.id)
	}
}

// lookupSocket finds the connection id names.
func lookupSocket(id string) (*socketConn, error) {
	id = strings.TrimSpace(id)
	socketMu.Lock()
	defer socketMu.Unlock()
	conn, ok := socketConns[id]
	if !ok {
		return nil, fmt.Errorf("no connection with id %q: connect first", id)
	}
	return conn, nil
}
