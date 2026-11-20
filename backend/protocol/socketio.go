package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Defaults the Engine.IO handshake falls back to when it does not say. The
// numbers are Socket.IO's own, so a server that omits them still gets a
// heartbeat it recognises.
const (
	sioDefaultPingInterval = 25 * time.Second
	sioDefaultPingTimeout  = 20 * time.Second
)

// sioState is one connection's Socket.IO session: the namespace the page named,
// the heartbeat the handshake asked for, and the acks waiting for an answer.
// socketConn.mu guards it, since the read loop and Send reach it from different
// goroutines.
type sioState struct {
	nsp          string
	eio3         bool // Socket.IO v2: the client pings, the server pongs
	connected    bool // the CONNECT was answered: the session is up
	pingInterval time.Duration
	pingTimeout  time.Duration
	nextAckID    int
	pending      map[string]string // ack id -> the event name that asked for it
	attachments  int               // binary frames the server still owes us
}

// sioPacket is one parsed Socket.IO packet: the packet type, the namespace it
// names, its ack id, and the data it carries.
type sioPacket struct {
	sioType byte // '0' connect | '1' disconnect | '2' event | '3' ack | '4' connect error | '5'/'6' binary
	hasType bool // false for the EIO3 error payload, which has no type digit
	nsp     string
	id      string
	payload string
}

// dialSocketIO opens the WebSocket the handshake URL names and runs the
// Engine.IO handshake and the Socket.IO CONNECT on it; a refused handshake
// reports its HTTP status alongside the error.
func (c *socketConn) dialSocketIO(ctx context.Context, params ConnectParams) (int, error) {
	target, nsp, err := socketIOURL(params)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.sio.nsp = nsp
	c.sio.eio3 = strings.TrimSpace(params.Version) == "3"
	c.mu.Unlock()

	// Socket.IO has no subprotocol of its own: sending one would be refused.
	header, dialer := socketDialer(params)
	header.Del("Sec-WebSocket-Protocol")
	dialer.Subprotocols = nil

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

	// The handshake runs under the dial timeout: a server that never speaks
	// fails the connect instead of hanging it.
	_ = ws.SetReadDeadline(time.Now().Add(socketDialTimeout))
	if err := c.sioHandshake(); err != nil {
		_ = ws.Close()
		return 0, err
	}

	c.mu.Lock()
	interval, timeout := c.sio.pingInterval, c.sio.pingTimeout
	eio3 := c.sio.eio3
	c.mu.Unlock()
	_ = ws.SetReadDeadline(time.Now().Add(interval + timeout))
	if eio3 {
		go c.pingLoop(ctx, interval)
	}
	return 0, nil
}

// sioHandshake reads the Engine.IO open packet, sends the Socket.IO CONNECT and
// waits for its answer.
func (c *socketConn) sioHandshake() error {
	ws := c.ws
	msgType, data, err := ws.ReadMessage()
	if err != nil {
		return fmt.Errorf("reading the Engine.IO handshake failed: %w", err)
	}
	if msgType != websocket.TextMessage || len(data) == 0 || data[0] != '0' {
		return fmt.Errorf("the server did not answer with an Engine.IO handshake")
	}
	var open struct {
		PingInterval int `json:"pingInterval"`
		PingTimeout  int `json:"pingTimeout"`
	}
	if err := json.Unmarshal(data[1:], &open); err != nil {
		return fmt.Errorf("parsing the Engine.IO handshake failed: %w", err)
	}

	c.mu.Lock()
	if open.PingInterval > 0 {
		c.sio.pingInterval = time.Duration(open.PingInterval) * time.Millisecond
	}
	if open.PingTimeout > 0 {
		c.sio.pingTimeout = time.Duration(open.PingTimeout) * time.Millisecond
	}
	err = c.writeSIOLocked(c.sioPrefix("0"))
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("sending the Socket.IO connect packet failed: %w", err)
	}

	// The CONNECT is answered with a CONNECT packet carrying the session id
	// (EIO4) or bare (EIO3), or with CONNECT_ERROR; pings can arrive in
	// between, and so can another namespace's packets.
	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("waiting for the Socket.IO connect failed: %w", err)
		}
		if msgType == websocket.BinaryMessage {
			continue
		}
		text := string(data)
		switch text {
		case "1":
			return errors.New("the server closed the connection during the Socket.IO handshake")
		case "2": // an engine.io ping: answer it and keep waiting
			if err := c.writeSIO("3"); err != nil {
				return err
			}
			continue
		case "3":
			continue
		}

		packet, ok := parseSIO(text)
		if !ok {
			continue
		}
		if packet.nsp != "" && packet.nsp != c.sio.nsp {
			continue
		}
		if !packet.hasType {
			return fmt.Errorf("the Socket.IO server refused the connection: %s", sioErrorMessage(packet.payload))
		}
		switch packet.sioType {
		case '0':
			c.mu.Lock()
			c.sio.connected = true
			c.mu.Unlock()
			return nil
		case '4':
			return fmt.Errorf("the Socket.IO server refused the connection: %s", sioErrorMessage(packet.payload))
		}
	}
}

// pingLoop sends the heartbeat an EIO3 server expects: the client pings and the
// server pongs, the reverse of EIO4. It ends with the connection.
func (c *socketConn) pingLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = sioDefaultPingInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.writeSIO("2"); err != nil {
				return
			}
		}
	}
}

// readSocketIO pushes what the server sends until the connection ends. The read
// deadline is the heartbeat the handshake promised: a server that goes quiet
// for longer than that ends here instead of hanging the page.
func (c *socketConn) readSocketIO() error {
	ws := c.ws
	for {
		c.mu.Lock()
		_ = ws.SetReadDeadline(time.Now().Add(c.sio.pingInterval + c.sio.pingTimeout))
		c.mu.Unlock()

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
		if msgType == websocket.BinaryMessage {
			c.receiveSIOBinaryFrame(data)
			continue
		}

		done, err := c.handleSIOText(string(data))
		if err != nil || done {
			return err
		}
	}
}

// handleSIOText dispatches one Engine.IO packet and reports whether it ended
// the connection.
func (c *socketConn) handleSIOText(text string) (bool, error) {
	if text == "" {
		return false, nil
	}
	switch text[0] {
	case '0':
		return false, nil // a repeated Engine.IO open: nothing to do
	case '1': // engine.io close
		c.closeEvent(0, "")
		return true, nil
	case '2': // ping: pong right away, which is what keeps the server happy
		return false, c.writeSIO("3" + text[1:])
	case '3', '5', '6': // pong, and the Engine.IO types Socket.IO never uses
		return false, nil
	case '4':
	default:
		return false, nil
	}

	packet, ok := parseSIO(text)
	if !ok {
		return false, nil
	}
	if packet.nsp != "" && packet.nsp != c.sio.nsp {
		return false, nil
	}
	switch packet.sioType {
	case '1': // Socket.IO DISCONNECT
		c.closeEvent(0, "")
		return true, nil
	case '2':
		return false, c.receiveSIOEvent(packet)
	case '3':
		return false, c.receiveSIOAck(packet)
	case '4':
		if err := c.sink.Send(SocketEvent{
			Type:   "error",
			Reason: sioErrorMessage(packet.payload),
			At:     time.Now().UnixMilli(),
		}); err != nil {
			return false, err
		}
		return true, nil
	case '5', '6':
		return false, c.receiveSIOBinaryPacket(packet)
	}
	return false, nil
}

// receiveSIOEvent pushes one event the server sent. An event that asks for an
// ack gets an empty one: we are a debugger, not the handler the server expects,
// so the server should not wait on us.
func (c *socketConn) receiveSIOEvent(packet sioPacket) error {
	ev := SocketEvent{
		Type:      "message",
		Direction: "receive",
		Payload:   packet.payload,
		At:        time.Now().UnixMilli(),
	}
	if name, args, ok := sioEventArgs(packet.payload); ok {
		ev.Event = name
		ev.Payload = args
	}
	if err := c.sink.Send(ev); err != nil {
		return err
	}
	if packet.id == "" {
		return nil
	}
	return c.writeSIO(c.sioPrefix("3") + packet.id + "[]")
}

// receiveSIOAck pushes the answer to an event we sent with Ack set.
func (c *socketConn) receiveSIOAck(packet sioPacket) error {
	c.mu.Lock()
	name := c.sio.pending[packet.id]
	delete(c.sio.pending, packet.id)
	c.mu.Unlock()
	return c.sink.Send(SocketEvent{
		Type:      "message",
		Direction: "receive",
		Event:     name,
		IsAck:     true,
		Payload:   packet.payload,
		At:        time.Now().UnixMilli(),
	})
}

// receiveSIOBinaryPacket shows that a binary packet arrived without
// reassembling it: the attachment frames the server sends next are skipped, and
// the event carries a placeholder instead of the bytes.
func (c *socketConn) receiveSIOBinaryPacket(packet sioPacket) error {
	count, rest := 0, packet.payload
	if i := strings.IndexByte(rest, '-'); i > 0 {
		if n, err := strconv.Atoi(rest[:i]); err == nil {
			count, rest = n, rest[i+1:]
		}
	}
	if count > 0 {
		c.mu.Lock()
		c.sio.attachments += count
		c.mu.Unlock()
	}

	name, _, _ := sioEventArgs(rest)
	placeholder := fmt.Sprintf(`{"binary":true,"attachments":%d,"note":"binary attachments are not reassembled"}`, count)
	return c.sink.Send(SocketEvent{
		Type:      "message",
		Direction: "receive",
		Event:     name,
		IsAck:     packet.sioType == '6',
		Payload:   placeholder,
		At:        time.Now().UnixMilli(),
	})
}

// receiveSIOBinaryFrame consumes one binary frame: the attachments of a binary
// packet are skipped, and anything else shows up as the base64 message it is.
func (c *socketConn) receiveSIOBinaryFrame(data []byte) {
	c.mu.Lock()
	skipping := c.sio.attachments > 0
	if skipping {
		c.sio.attachments--
	}
	c.mu.Unlock()
	if skipping {
		return
	}
	// Engine.IO prefixes a binary message with its packet type byte.
	if len(data) > 1 && data[0] == 4 {
		data = data[1:]
	}
	_ = c.sink.Send(SocketEvent{
		Type:      "message",
		Direction: "receive",
		Payload:   base64.StdEncoding.EncodeToString(data),
		IsBinary:  true,
		At:        time.Now().UnixMilli(),
	})
}

// sendSocketIO writes one event; with Ack set it also waits for the server's
// answer, which receiveSIOAck pushes when it comes.
func (c *socketConn) sendSocketIO(params SendParams) error {
	name := strings.TrimSpace(params.Event)
	if name == "" {
		name = "message"
	}
	data, err := sioEventData(name, params.Payload)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.shut {
		c.mu.Unlock()
		return fmt.Errorf("connection %s is closed", c.id)
	}
	if !c.sio.connected {
		// The CONNECT is not answered yet: an event would be dropped by the
		// server, so say why instead.
		c.mu.Unlock()
		return fmt.Errorf("connection %s is still connecting", c.id)
	}
	packet := c.sioPrefix("2")
	if params.Ack {
		c.sio.nextAckID++
		id := strconv.Itoa(c.sio.nextAckID)
		c.sio.pending[id] = name
		packet += id
	}
	packet += data
	err = c.writeSIOLocked(packet)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("sending on connection %s failed: %w", c.id, err)
	}

	return c.sink.Send(SocketEvent{
		Type:      "message",
		Direction: "send",
		Event:     name,
		Payload:   params.Payload,
		IsBinary:  params.IsBinary,
		At:        time.Now().UnixMilli(),
	})
}

// sioPrefix starts a Socket.IO packet of type sioType: the Engine.IO message
// type, the Socket.IO type, then the namespace when the connection has one.
func (c *socketConn) sioPrefix(sioType string) string {
	packet := "4" + sioType
	if c.sio != nil && c.sio.nsp != "" {
		packet += c.sio.nsp + ","
	}
	return packet
}

// writeSIOLocked writes one text packet; c.mu must be held.
func (c *socketConn) writeSIOLocked(packet string) error {
	if c.ws == nil {
		return fmt.Errorf("connection %s is not open", c.id)
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(socketWriteTicks))
	return c.ws.WriteMessage(websocket.TextMessage, []byte(packet))
}

// writeSIO writes one packet from anywhere but under the lock.
func (c *socketConn) writeSIO(packet string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shut {
		return fmt.Errorf("connection %s is closed", c.id)
	}
	return c.writeSIOLocked(packet)
}

// socketIOURL builds the Engine.IO handshake URL and names the Socket.IO
// namespace the page asked for: the URL's path is the namespace, never part of
// the handshake address, which is the path setting.
func socketIOURL(params ConnectParams) (target, nsp string, err error) {
	raw := strings.TrimSpace(params.URL)
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}
	var scheme string
	switch strings.ToLower(u.Scheme) {
	case "http", "ws":
		scheme = "ws"
	case "https", "wss":
		scheme = "wss"
	default:
		return "", "", fmt.Errorf("invalid URL %q: it needs http:// or https://", raw)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("invalid URL %q: it needs a host", raw)
	}

	nsp = strings.TrimRight(u.Path, "/")
	if nsp != "" && !strings.HasPrefix(nsp, "/") {
		nsp = "/" + nsp
	}

	path := strings.TrimSpace(params.Path)
	if path == "" {
		path = "/socket.io/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	query := u.Query()
	for _, p := range params.Params {
		if p.Key == "" {
			continue
		}
		query.Add(p.Key, p.Value)
	}
	// These two are what the handshake needs: the page's parameters cannot
	// replace them.
	if strings.TrimSpace(params.Version) == "3" {
		query.Set("EIO", "3")
	} else {
		query.Set("EIO", "4")
	}
	query.Set("transport", "websocket")

	return scheme + "://" + u.Host + path + "?" + query.Encode(), nsp, nil
}

// parseSIO splits one Engine.IO/Socket.IO message: the type digits, the
// namespace, the ack id and the data, in the order the protocol writes them.
func parseSIO(text string) (packet sioPacket, ok bool) {
	if len(text) < 2 || text[0] != '4' {
		return sioPacket{}, false
	}
	rest := text[1:]
	if rest[0] >= '0' && rest[0] <= '9' {
		packet.sioType = rest[0]
		packet.hasType = true
		rest = rest[1:]
	}
	if strings.HasPrefix(rest, "/") {
		if i := strings.IndexByte(rest, ','); i >= 0 {
			packet.nsp, rest = rest[:i], rest[i+1:]
		} else {
			// An EIO3 namespace can arrive without its comma.
			packet.nsp, rest = rest, ""
		}
	}
	if packet.hasType && packet.sioType >= '5' {
		// A binary packet leads with its attachment count, which is data, not
		// an ack id: `45<count>-[...]`.
		packet.payload = rest
		return packet, true
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	packet.id = rest[:i]
	packet.payload = rest[i:]
	return packet, true
}

// sioEventArgs splits an EVENT packet's data into its name and the JSON text of
// its arguments, keeping the server's own spelling of them.
func sioEventArgs(data string) (name, args string, ok bool) {
	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(data), &parts); err != nil || len(parts) == 0 {
		return "", "", false
	}
	if err := json.Unmarshal(parts[0], &name); err != nil {
		return "", "", false
	}
	if len(parts) == 1 {
		return name, "[]", true
	}
	list := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		list = append(list, string(part))
	}
	return name, "[" + strings.Join(list, ",") + "]", true
}

// sioEventData encodes one EVENT packet's data: the event name, then the
// payload as the JSON it is, or as the string it is when it is not JSON.
func sioEventData(name, payload string) (string, error) {
	var arg any
	if err := json.Unmarshal([]byte(payload), &arg); err != nil {
		arg = payload
	}
	data, err := json.Marshal([]any{name, arg})
	if err != nil {
		return "", fmt.Errorf("encoding the Socket.IO event failed: %w", err)
	}
	return string(data), nil
}

// sioErrorMessage reads the message a CONNECT_ERROR carries, and reports the
// payload as it came when it is not the JSON object the protocol promises.
func sioErrorMessage(payload string) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err == nil && body.Message != "" {
		return body.Message
	}
	if strings.TrimSpace(payload) == "" {
		return "connection refused"
	}
	return payload
}
