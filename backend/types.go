package main

// KeyValue is one key/value pair, e.g. a query parameter, a header or a cookie.
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FormField is one field of a multipart/form-data body.
type FormField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// ContentType overrides the Content-Type of this field's part; empty
	// keeps the default.
	ContentType string `json:"contentType"`
	// Description is a note for the user, not sent.
	Description string `json:"description"`
	// Kind 是字段类型："text"（默认，空同义）| "file"。
	Kind string `json:"kind"`
	// FilePath 是 Kind="file" 时文件的绝对路径；发送时读盘作为文件 part 上传
	//（单文件上限同 binary 的 100MB）。
	FilePath string `json:"filePath"`
}

// AuthInfo is the authentication a request carries.
type AuthInfo struct {
	Type     string `json:"type"` // "none" | "basic" | "bearer"
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// APIRequest is one HTTP request built in the frontend.
type APIRequest struct {
	Method     string      `json:"method"`
	URL        string      `json:"url"`
	Params     []KeyValue  `json:"params"`
	Headers    []KeyValue  `json:"headers"`
	BodyType   string      `json:"bodyType"` // "none" | "formData" | "urlencoded" | "raw" | "binary"
	FormData   []FormField `json:"formData"`
	URLEncoded []KeyValue  `json:"urlEncoded"`
	Raw        string      `json:"raw"`
	RawType    string      `json:"rawType"` // "json" | "text" | "xml" | "html"
	BinaryPath string      `json:"binaryPath"`
	Auth       AuthInfo    `json:"auth"`
	Cookies    []KeyValue  `json:"cookies"`
	Timeout    int         `json:"timeout"` // seconds; 0 uses the default
	HTTP2      bool        `json:"http2"`   // h2c：对 http:// 明文强制 HTTP/2（https 始终自动协商，忽略此项）
	// 预执行脚本（JS，goja 运行）；空 = 不执行。在 {{var}} 替换与请求组装之前运行。
	PreScript string `json:"preScript"`
	// 后执行脚本（JS，goja 运行）；空 = 不执行。拿到响应后运行。
	PostScript string `json:"postScript"`
	// 变量快照：{{var}} 替换与脚本 nd.vars 的数据源；脚本写回后经 APIResponse.Vars 返回。
	Vars map[string]string `json:"vars"`
}

// ScriptLog is one console.log/warn/error call from a pre/post script.
type ScriptLog struct {
	Level string `json:"level"` // "log" | "warn" | "error"
	Text  string `json:"text"`  // arguments serialized to JSON, joined with spaces
	At    int64  `json:"at"`    // Unix 毫秒
}

// ScriptTest is one nd.test(name, fn) result from a pre/post script.
type ScriptTest struct {
	Name  string `json:"name"`
	Pass  bool   `json:"pass"`
	Error string `json:"error"` // failure message; empty when passed
}

// ResponseCookie is one cookie the response set.
type ResponseCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path"`
	Domain   string `json:"domain"`
	Expires  string `json:"expires"` // RFC3339; empty means a session cookie
	Secure   bool   `json:"secure"`
	HttpOnly bool   `json:"httpOnly"`
}

// APIResponse is what Send hands back for one request.
type APIResponse struct {
	Status     int              `json:"status"`
	StatusText string           `json:"statusText"`
	Proto      string           `json:"proto"` // 实际协议版本，如 "HTTP/2.0"、"HTTP/1.1"
	Headers    []KeyValue       `json:"headers"`
	Cookies    []ResponseCookie `json:"cookies"`
	Body       string           `json:"body"`
	TimeMS     int64            `json:"timeMS"`
	SizeBytes  int              `json:"sizeBytes"`
	Truncated  bool             `json:"truncated"`
	// What was actually sent, for the page's "actual request" tab.
	FinalURL    string     `json:"finalURL"`
	SentHeaders []KeyValue `json:"sentHeaders"`
	SentBody    string     `json:"sentBody"` // text preview of the body, cut at 64KiB
	// 脚本产物：Vars 是脚本写回后的变量全量快照（无脚本时原样返回）；
	// ScriptLogs/ScriptTests 按产生顺序合并前后脚本。
	Vars        map[string]string `json:"vars"`
	ScriptLogs  []ScriptLog       `json:"scriptLogs"`
	ScriptTests []ScriptTest      `json:"scriptTests"`
	// 预脚本出错时非空（此时请求未发送，其余响应字段为零值）；
	// 后脚本出错不影响已拿到的响应。
	PreScriptError  string `json:"preScriptError"`
	PostScriptError string `json:"postScriptError"`
}

// SSE：流式响应（Content-Type: text/event-stream）。Send 先推一个 Open，
// 然后逐条推 Event；Send 返回即流结束。
type SSEOpen struct {
	Status     int        `json:"status"`
	StatusText string     `json:"statusText"`
	Proto      string     `json:"proto"`
	Headers    []KeyValue `json:"headers"`
}

type SSEEvent struct {
	N     int    `json:"n"`     // 从 1 开始的序号
	ID    string `json:"id"`    // 事件 id（服务器未给为空）
	Event string `json:"event"` // 事件名（默认 "message"）
	Data  string `json:"data"`  // data 字段，多行以 \n 拼接
}

// SSEMessage 是通道消息的联合：Open 非空 = 响应头到达；Event 非空 = 一条事件。
type SSEMessage struct {
	Open  *SSEOpen  `json:"open,omitempty"`
	Event *SSEEvent `json:"event,omitempty"`
}

// SocketConnectParams 发起一条 WebSocket（ws:// / wss://）或 TCP 连接。
type SocketConnectParams struct {
	ID       string     `json:"id"`       // 前端生成的连接 id（uuid）；Send/Disconnect 凭它定位连接
	Kind     string     `json:"kind"`     // "websocket" | "tcp"
	URL      string     `json:"url"`      // websocket：ws:// 或 wss:// 地址；tcp：host:port
	Params   []KeyValue `json:"params"`   // 查询参数（仅 WS 使用）：拼到 URL（已有查询串则追加）
	Headers  []KeyValue `json:"headers"`  // WS 握手请求头（TCP 忽略）；后端须过滤 gorilla 托管头
	Protocol string     `json:"protocol"` // WS 子协议（Sec-WebSocket-Protocol），逗号分隔多个
}

// SocketSendParams 在已建立的连接上发送一条消息。
type SocketSendParams struct {
	ID       string `json:"id"`
	Payload  string `json:"payload"` // 文本 = 原文；二进制 = base64
	IsBinary bool   `json:"isBinary"`
}

// SocketEvent 是连接生命周期事件，Connect 存续期间经 stream 推送。
type SocketEvent struct {
	Type      string     `json:"type"`                // "open" | "message" | "error" | "close"
	Direction string     `json:"direction,omitempty"` // message 专属："send" | "receive"
	Payload   string     `json:"payload,omitempty"`   // message 内容；二进制为 base64
	IsBinary  bool       `json:"isBinary,omitempty"`
	At        int64      `json:"at"`                 // Unix 毫秒
	Code      int        `json:"code,omitempty"`     // WS 关闭码；握手失败时为 HTTP 状态码
	Reason    string     `json:"reason,omitempty"`   // 错误/关闭原因
	Headers   []KeyValue `json:"headers,omitempty"`  // open：WS 握手响应头（按 key 排序、同名合并）
	Protocol  string     `json:"protocol,omitempty"` // open：实际协商出的子协议
}

type SavedAssertion struct { // 前端 AssertionRule 的持久化形态
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	Value   string `json:"value"`
}

type SavedMessage struct { // WS/TCP 的已存消息模板（发送消息页左侧列表）
	ID      string `json:"id"`
	Name    string `json:"name"`
	MsgType string `json:"msgType"` // "text" | "json" | "xml" | "html" | "binary"
	Payload string `json:"payload"`
}

type SavedRequest struct { // RequestState 的快照 + 展示名
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Kind           string           `json:"kind"` // "http" | "websocket" | "tcp"；空 = http（兼容旧 .band）
	Method         string           `json:"method"`
	URL            string           `json:"url"`
	Params         []KeyValue       `json:"params"`
	Headers        []KeyValue       `json:"headers"`
	BodyType       string           `json:"bodyType"`
	FormData       []FormField      `json:"formData"`
	URLEncoded     []KeyValue       `json:"urlEncoded"`
	Raw            string           `json:"raw"`
	RawType        string           `json:"rawType"`
	BinaryPath     string           `json:"binaryPath"`
	Auth           AuthInfo         `json:"auth"`
	Cookies        []KeyValue       `json:"cookies"`
	Timeout        int              `json:"timeout"`
	HTTP2          bool             `json:"http2"`
	PreScript      string           `json:"preScript"`  // 预执行脚本；空 = 无
	PostScript     string           `json:"postScript"` // 后执行脚本；空 = 无
	Assertions     []SavedAssertion `json:"assertions"`
	Messages       []SavedMessage   `json:"messages"`       // 仅 WS/TCP 使用；nil 落盘补 []（生成 TS 类型要求）
	SocketProtocol string           `json:"socketProtocol"` // WS 子协议（Sec-WebSocket-Protocol），逗号分隔；仅 WS 使用
}

type WorkspaceData struct {
	Name     string            `json:"name"`
	Requests []SavedRequest    `json:"requests"`
	Vars     map[string]string `json:"vars"` // 脚本变量；读写为 zip 内独立 vars.json
}

type WorkspaceFile struct {
	Path string        `json:"path"`
	Data WorkspaceData `json:"data"`
}
