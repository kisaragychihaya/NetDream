// Package protocol speaks the wire protocols the debug page drives: HTTP
// (event streams included), WebSocket, raw TCP and Socket.IO. It knows nothing
// about the IPC surface — the main package maps its own contract types onto the
// ones here, so a change to the page's contract never reaches this package.
package protocol

// KeyValue is one key/value pair: a query parameter, a header or a cookie.
type KeyValue struct {
	Key   string
	Value string
}

// FormField is one field of a multipart/form-data body.
type FormField struct {
	Key   string
	Value string
	// ContentType overrides the Content-Type of this field's part; empty keeps
	// the default.
	ContentType string
	// Description is a note for the user, not sent.
	Description string
	// Kind 是字段类型："text"（默认，空同义）| "file"。
	Kind string
	// FilePath 是 Kind="file" 时文件的绝对路径；发送时读盘作为文件 part 上传
	//（单文件上限同 binary 的 100MB）。
	FilePath string
}

// AuthInfo is the authentication a request carries.
type AuthInfo struct {
	Type     string
	Username string
	Password string
	Token    string
}

// HTTPRequest is one request to send.
type HTTPRequest struct {
	Method     string
	URL        string
	Params     []KeyValue
	Headers    []KeyValue
	BodyType   string // "none" | "formData" | "urlencoded" | "raw" | "binary"
	FormData   []FormField
	URLEncoded []KeyValue
	Raw        string
	RawType    string // "json" | "text" | "xml" | "html"
	BinaryPath string
	Auth       AuthInfo
	Cookies    []KeyValue
	Timeout    int  // seconds; 0 uses the default
	HTTP2      bool // h2c：对 http:// 明文强制 HTTP/2
}

// ResponseCookie is one cookie the response set.
type ResponseCookie struct {
	Name     string
	Value    string
	Path     string
	Domain   string
	Expires  string // RFC3339; empty means a session cookie
	Secure   bool
	HttpOnly bool
}

// HTTPResponse is what Do hands back for one request.
type HTTPResponse struct {
	Status     int
	StatusText string
	Proto      string
	Headers    []KeyValue
	Cookies    []ResponseCookie
	Body       string
	TimeMS     int64
	SizeBytes  int
	Truncated  bool
	// What was actually sent, for the page's "actual request" tab.
	FinalURL    string
	SentHeaders []KeyValue
	SentBody    string
}

// SSEOpen is the head of an event stream: what the response answered with.
type SSEOpen struct {
	Status     int
	StatusText string
	Proto      string
	Headers    []KeyValue
}

// SSEEvent is one dispatched event of an event stream.
type SSEEvent struct {
	N     int    // 从 1 开始的序号
	ID    string // 事件 id（服务器未给为空）
	Event string // 事件名（默认 "message"）
	Data  string // data 字段，多行以 \n 拼接
}

// SSEMessage is one message of the stream: Open is set when the response head
// arrived, Event when an event was dispatched.
type SSEMessage struct {
	Open  *SSEOpen
	Event *SSEEvent
}

// Emit is where an event stream pushes its messages, head first. It replaces
// the page's channel here, so that this package needs no IPC type; an error it
// returns ends the stream and is handed back unwrapped (the page closed its
// channel, it did not fail).
type Emit func(SSEMessage) error

// ConnectParams opens one connection.
type ConnectParams struct {
	ID       string
	Kind     string // "websocket" | "tcp" | "socketio"
	URL      string // websocket/socketio：ws(s):// 或 http(s):// 地址；tcp：host:port
	Params   []KeyValue
	Headers  []KeyValue
	Protocol string
	// Path 是 Socket.IO 的 Engine.IO 握手路径，空 = "/socket.io/"（仅 socketio）。
	Path string
	// Version 是 Socket.IO 协议版本："4"（默认，EIO4/Socket.IO v3+）| "3"
	//（EIO3/Socket.IO v2），仅 socketio。
	Version string
}

// SendParams writes one message on an open connection.
type SendParams struct {
	ID       string
	Payload  string
	IsBinary bool
	// Event 是 Socket.IO 事件名，空 = "message"（ws/tcp 忽略）。
	Event string
	// Ack 要求 Socket.IO 服务器回 ack（ws/tcp 忽略）。
	Ack bool
}

// SocketEvent is one connection lifecycle event.
type SocketEvent struct {
	Type      string // "open" | "message" | "error" | "close"
	Direction string // message 专属："send" | "receive"
	Payload   string // message 内容；二进制为 base64
	IsBinary  bool
	At        int64 // Unix 毫秒
	Code      int   // WS 关闭码；握手失败时为 HTTP 状态码
	Reason    string
	Headers   []KeyValue // open：握手响应头（按 key 排序、同名合并）
	Protocol  string     // open：实际协商出的子协议
	Event     string     // socketio：事件名
	IsAck     bool       // socketio：这条 message 是 ack 的回答
}

// Sink is where a connection pushes its events.
type Sink interface {
	Send(SocketEvent) error
}
