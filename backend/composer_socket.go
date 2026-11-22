package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"netdream/backend/ndapi"
	"netdream/backend/protocol"
)

// composerKindWS 等是 composer 打开的三类连接；同一个名字也是它们日志行的前缀。
const (
	composerKindWS       = "ws"
	composerKindTCP      = "tcp"
	composerKindSocketIO = "socketio"

	// composerSocketBuffer caps what one connection buffers for a script that is
	// not pulling: a chatty server must not grow the run's memory without bound.
	// Over the cap the newest events are dropped, and the run's log says so once.
	composerSocketBuffer = 1024
)

// composerSockets is the connections one run opened, in the order it opened them.
// The lock is real even while the pull model keeps the script on one goroutine:
// every connection's Connect runs on a goroutine of its own.
type composerSockets struct {
	mu    sync.Mutex
	next  int
	conns map[int]*composerSocket
}

// newComposerSockets is an empty registry; only Run's own host has one.
func newComposerSockets() *composerSockets {
	return &composerSockets{conns: map[int]*composerSocket{}}
}

// add registers handle and returns its number, which the connection's protocol id
// carries.
func (s *composerSockets) add(handle *composerSocket) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.conns[s.next] = handle
	return s.next
}

// all is the handles opened so far, in the order they were opened.
func (s *composerSockets) all() []*composerSocket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*composerSocket, 0, len(s.conns))
	for i := 1; i <= s.next; i++ {
		if handle := s.conns[i]; handle != nil {
			out = append(out, handle)
		}
	}
	return out
}

// closeAll ends every connection the run still has open.
func (s *composerSockets) closeAll() {
	for _, handle := range s.all() {
		handle.close()
	}
}

// composerSocket is one connection a run opened in the background: protocol's
// Connect pushes its events into a buffer, and the script pulls them out at its
// own pace. goja has no event loop, so a callback model would have nowhere to
// run — the script asks (receive/poll) instead, which is also why open can return
// at once while the dial is still in flight.
type composerSocket struct {
	host   *composerHost
	kind   string
	target string
	// id is the connection's id in protocol's registry.
	id string
	// ctx is the run's lifetime: a script blocked in receive has to wake up when
	// the run ends.
	ctx context.Context

	// mu guards the buffer: push runs on the connection's goroutine, the pull
	// methods run on the script's.
	mu      sync.Mutex
	events  []SocketEvent
	dropped int
	// done means no more events will arrive: close() ran, or Connect returned.
	// A receiver drains what is still buffered and then gets null.
	done bool
	// closed marks close() itself, so a repeated call is a no-op.
	closed bool
	// signal wakes a waiting receiver; one pending wake is enough, since the
	// receiver drains the buffer in a loop.
	signal chan struct{}
}

// socketGlobal is one of nd.composer.ws / .tcp / .socketio: opening a connection
// in the background, and the handle the script drives it with.
func (h *composerHost) socketGlobal(kind string) *goja.Object {
	api := h.vm.NewObject()
	_ = api.Set("open", func(call goja.FunctionCall) goja.Value {
		target := strings.TrimSpace(stringField(call.Argument(0)))
		if target == "" {
			h.throw("nd.composer.%s.open 需要%s", kind, composerTargetHint(kind))
		}
		opts, err := h.socketOptionsOf(kind, call.Argument(1))
		if err != nil {
			h.throw("%s", err.Error())
		}
		return h.openSocket(kind, target, opts).object()
	})
	return api
}

// composerTargetHint is what an open call's first argument has to be.
func composerTargetHint(kind string) string {
	switch kind {
	case composerKindWS:
		return "ws:// 或 wss:// 地址"
	case composerKindSocketIO:
		return "http(s):// 地址"
	default:
		return "host:port"
	}
}

// composerSocketOptions is what one open call carries: the connection settings the
// socket page would have filled in, and nothing else.
type composerSocketOptions struct {
	params   []KeyValue
	headers  []KeyValue
	protocol string // ws：子协议
	path     string // socketio：握手路径
	version  string // socketio：协议版本
}

// socketOptionsOf reads one open call's options; absent or null is the page's
// defaults.
func (h *composerHost) socketOptionsOf(kind string, value goja.Value) (composerSocketOptions, error) {
	opts := composerSocketOptions{}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return opts, nil
	}
	obj, ok := value.(*goja.Object)
	if !ok {
		return opts, fmt.Errorf("nd.composer.%s.open 的第二个参数需要是对象", kind)
	}
	err := h.vm.Try(func() {
		opts.params = h.keyValuePairs(obj.Get("params"))
		opts.headers = h.keyValuePairs(obj.Get("headers"))
		opts.protocol = stringField(obj.Get("protocol"))
		opts.path = stringField(obj.Get("path"))
		opts.version = stringField(obj.Get("version"))
	})
	if err != nil {
		return composerSocketOptions{}, fmt.Errorf("读取 nd.composer.%s.open 的 options 失败：%s", kind, ndapi.ScriptErrorText(err))
	}
	return opts, nil
}

// keyValuePairs turns one script object into ordered key/value pairs, the way a
// request carries its params and headers.
func (h *composerHost) keyValuePairs(value goja.Value) []KeyValue {
	obj, ok := value.(*goja.Object)
	if !ok {
		return nil
	}
	out := make([]KeyValue, 0, len(obj.Keys()))
	for _, key := range obj.Keys() {
		if key == "" {
			continue
		}
		item := obj.Get(key)
		if item == nil || goja.IsUndefined(item) || goja.IsNull(item) {
			continue
		}
		out = append(out, KeyValue{Key: key, Value: item.String()})
	}
	return out
}

// openSocket starts the connection and hands the script its handle at once: the
// dial runs in the background and its outcome arrives as an event, so a failure
// is something receive reports rather than a thrown script error.
func (h *composerHost) openSocket(kind, target string, opts composerSocketOptions) *composerSocket {
	handle := &composerSocket{
		host:   h,
		kind:   kind,
		target: target,
		ctx:    h.ctx,
		signal: make(chan struct{}, 1),
	}
	seq := h.sockets.add(handle)
	// 连接 id 归这次运行：运行 id 在登记表里唯一，序号在运行内唯一，protocol 的
	// 登记表就不会撞上别的连接。
	handle.id = fmt.Sprintf("composer-%s-%d", h.id, seq)

	go func() {
		defer handle.finish()
		_ = protocol.Connect(handle.ctx, composerSocketConnectParams(kind, handle.id, target, opts), composerSocketSink{handle: handle})
	}()
	return handle
}

// composerSocketConnectParams is one open call as the protocol package takes it.
func composerSocketConnectParams(kind, id, target string, opts composerSocketOptions) protocol.ConnectParams {
	params := protocol.ConnectParams{
		ID:      id,
		Kind:    composerProtocolKind(kind),
		URL:     target,
		Params:  protocolPairs(opts.params),
		Headers: protocolPairs(opts.headers),
	}
	switch kind {
	case composerKindWS:
		params.Protocol = opts.protocol
	case composerKindSocketIO:
		params.Path = opts.path
		params.Version = opts.version
	}
	return params
}

// composerProtocolKind is the composer's short name for a connection as the
// protocol package spells it.
func composerProtocolKind(kind string) string {
	if kind == composerKindWS {
		return "websocket"
	}
	return kind
}

// composerSocketSink is the page's channel replaced by the handle's buffer: the
// connection pushes from its own goroutine, the script pulls.
type composerSocketSink struct{ handle *composerSocket }

// Send buffers one event.
func (s composerSocketSink) Send(ev protocol.SocketEvent) error {
	return s.handle.push(ev)
}

// push buffers one event from the connection's goroutine and, for the lifecycle
// events, adds the log line a reader of the run's output expects.
func (s *composerSocket) push(ev protocol.SocketEvent) error {
	// 自己发出去的回执不算脚本要取的事件：脚本知道它发过什么，取的是对端的回应。
	if ev.Direction == "send" {
		return nil
	}
	event := socketEvent(ev)

	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	if len(s.events) >= composerSocketBuffer {
		s.dropped++
		first := s.dropped == 1
		s.mu.Unlock()
		if first {
			s.host.log(s.kind, "warn", fmt.Sprintf("事件缓冲已满（%d 条），其余事件丢弃", composerSocketBuffer), event.At)
		}
		return nil
	}
	s.events = append(s.events, event)
	s.mu.Unlock()
	s.wake()

	switch event.Type {
	case "open":
		s.host.log(s.kind, "log", "open "+s.target, event.At)
	case "error":
		s.host.log(s.kind, "warn", "error: "+event.Reason, event.At)
	case "close":
		s.host.log(s.kind, "log", "close"+closeLine(event), event.At)
	}
	return nil
}

// closeLine is a close event's code and reason as the tail of one log line.
func closeLine(ev SocketEvent) string {
	parts := make([]string, 0, 2)
	if ev.Code != 0 {
		parts = append(parts, strconv.Itoa(ev.Code))
	}
	if ev.Reason != "" {
		parts = append(parts, ev.Reason)
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, " ")
}

// finish marks the connection as ended: Connect returned, so nothing else will be
// pushed.
func (s *composerSocket) finish() {
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	s.wake()
}

// close ends the connection. It is safe to call repeatedly, and a handle whose
// connection already ended keeps the events it had not pulled yet.
func (s *composerSocket) close() {
	s.mu.Lock()
	first := !s.closed
	s.closed = true
	s.done = true
	s.mu.Unlock()
	if !first {
		return
	}
	s.wake()
	protocol.Disconnect(s.id)
}

// wake tells a waiting receiver that something changed.
func (s *composerSocket) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

// pop takes the oldest buffered event.
func (s *composerSocket) pop() (SocketEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return SocketEvent{}, false
	}
	ev := s.events[0]
	s.events = s.events[1:]
	return ev, true
}

// ended reports whether no more events will arrive.
func (s *composerSocket) ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// poll is the non-blocking pull: the oldest event, or nil when there is none.
func (s *composerSocket) poll() *SocketEvent {
	if ev, ok := s.pop(); ok {
		return &ev
	}
	return nil
}

// next is the blocking pull. wait is how long the script is willing to wait (0 =
// as long as it takes). nil is the end of the wait: the deadline passed, the
// connection ended with nothing left, or the run ended.
func (s *composerSocket) next(wait time.Duration) *SocketEvent {
	var deadline <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		if ev, ok := s.pop(); ok {
			return &ev
		}
		if s.ended() {
			return nil
		}
		select {
		case <-s.signal:
		case <-deadline:
			return nil
		case <-s.ctx.Done():
			// 运行结束（Stop、超时或页面关闭）：阻塞在 receive 里的脚本要立刻醒来。
			return nil
		}
	}
}

// object is the handle as the script sees it: send, receive, poll and close, the
// same four for every kind of connection.
func (s *composerSocket) object() *goja.Object {
	vm := s.host.vm
	obj := vm.NewObject()
	_ = obj.Set("id", s.id)
	_ = obj.Set("kind", s.kind)

	_ = obj.Set("send", func(call goja.FunctionCall) goja.Value {
		params := protocol.SendParams{ID: s.id, Payload: stringField(call.Argument(0))}
		if opts := call.Argument(1); opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
			options, ok := opts.(*goja.Object)
			if !ok {
				s.host.throw("send 的第二个参数需要是对象")
			}
			_ = vm.Try(func() {
				if value := options.Get("isBinary"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
					params.IsBinary = value.ToBoolean()
				}
				if value := options.Get("event"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
					params.Event = value.String()
				}
				if value := options.Get("ack"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
					params.Ack = value.ToBoolean()
				}
			})
		}
		if err := protocol.Send(params); err != nil {
			s.host.throw("在 %s 连接上发送失败：%s", s.kind, err)
		}
		return goja.Undefined()
	})

	_ = obj.Set("receive", func(call goja.FunctionCall) goja.Value {
		var wait time.Duration
		if arg := call.Argument(0); arg != nil && !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			if ms := arg.ToInteger(); ms > 0 {
				wait = time.Duration(ms) * time.Millisecond
			}
		}
		if ev := s.next(wait); ev != nil {
			return s.eventObject(*ev)
		}
		return goja.Null()
	})

	_ = obj.Set("poll", func(goja.FunctionCall) goja.Value {
		if ev := s.poll(); ev != nil {
			return s.eventObject(*ev)
		}
		return goja.Null()
	})

	_ = obj.Set("close", func(goja.FunctionCall) goja.Value {
		s.close()
		return goja.Undefined()
	})
	return obj
}

// eventObject is one event as the script reads it: SocketEvent 的镜像，headers 按
// key/value 列表给（与工作区条目一致）。
func (s *composerSocket) eventObject(ev SocketEvent) goja.Value {
	vm := s.host.vm
	obj := vm.NewObject()
	_ = obj.Set("type", ev.Type)
	_ = obj.Set("payload", ev.Payload)
	_ = obj.Set("isBinary", ev.IsBinary)
	_ = obj.Set("event", ev.Event)
	_ = obj.Set("isAck", ev.IsAck)
	_ = obj.Set("code", ev.Code)
	_ = obj.Set("reason", ev.Reason)
	_ = obj.Set("protocol", ev.Protocol)
	_ = obj.Set("at", ev.At)

	headers := vm.NewArray()
	for i, kv := range ev.Headers {
		pair := vm.NewObject()
		_ = pair.Set("key", kv.Key)
		_ = pair.Set("value", kv.Value)
		_ = headers.Set(strconv.Itoa(i), pair)
	}
	_ = obj.Set("headers", headers)
	return obj
}
