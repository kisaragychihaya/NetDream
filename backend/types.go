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

// SocketConnectParams 发起一条 WebSocket（ws:// / wss://）、TCP 或 Socket.IO 连接。
type SocketConnectParams struct {
	ID       string     `json:"id"`       // 前端生成的连接 id（uuid）；Send/Disconnect 凭它定位连接
	Kind     string     `json:"kind"`     // "websocket" | "tcp" | "socketio"
	URL      string     `json:"url"`      // websocket：ws:// 或 wss:// 地址；socketio：http(s)://host[:port][/命名空间]；tcp：host:port
	Params   []KeyValue `json:"params"`   // 查询参数（WS/Socket.IO 使用）：拼到 URL 或握手地址（已有查询串则追加；socketio 不覆盖 EIO/transport）
	Headers  []KeyValue `json:"headers"`  // WS/Socket.IO 握手请求头（TCP 忽略）；后端须过滤 gorilla 托管头
	Protocol string     `json:"protocol"` // WS 子协议（Sec-WebSocket-Protocol），逗号分隔多个；socketio 忽略
	// Path 是 Socket.IO 的 Engine.IO 握手路径，空 = "/socket.io/"（缺首尾斜杠由后端补齐）；仅 socketio 使用。
	Path string `json:"path"`
	// Version 是 Socket.IO 协议版本："4"（默认 = EIO4 / Socket.IO v3+）|
	// "3"（= EIO3 / Socket.IO v2，心跳方向相反）；仅 socketio 使用。
	Version string `json:"version"`
}

// SocketSendParams 在已建立的连接上发送一条消息。
type SocketSendParams struct {
	ID       string `json:"id"`
	Payload  string `json:"payload"` // 文本 = 原文；二进制 = base64
	IsBinary bool   `json:"isBinary"`
	// Event 是 Socket.IO 事件名（空 = "message"）；ws/tcp 忽略。
	Event string `json:"event"`
	// Ack 要求 Socket.IO 服务器对这条事件回 ack（连接内自增 id）；ws/tcp 忽略。
	Ack bool `json:"ack"`
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
	// Event 是 Socket.IO 的事件名（收发 message 与 ack 回答都带）；ws/tcp 为空。
	Event string `json:"event,omitempty"`
	// IsAck 标记这条 message 是 Socket.IO 的 ack 回答（Event 为原事件名，Payload 为 ack 参数）。
	IsAck bool `json:"isAck,omitempty"`
}

// GrpcListParams 问目标提供哪些服务与方法：方法描述来自服务器反射，
// ProtoFiles 非空时改读本地 .proto。
type GrpcListParams struct {
	Target        string     `json:"target"`        // host:port
	Plaintext     bool       `json:"plaintext"`     // true = 明文 h2c；false = TLS
	TLSSkipVerify bool       `json:"tlsSkipVerify"` // 跳过证书校验
	ServerName    string     `json:"serverName"`    // TLS server name 覆盖
	Metadata      []KeyValue `json:"metadata"`      // 调用元数据（反射请求同样携带）
	ProtoFiles    []string   `json:"protoFiles"`    // 本地 .proto 绝对路径；空 = 服务器反射
	ImportPaths   []string   `json:"importPaths"`   // proto 解析的 import 路径（各 proto 文件目录自动加入）
	Timeout       int        `json:"timeout"`       // 秒；0 = 默认 30（List 与一元调用生效，流式不限时）
}

// GrpcConnectParams 发起一次调用。连接参数与 GrpcListParams 同义，逐字段扁平
// 内联（不嵌入）：mygo 生成 TS 类型不展开嵌入结构体。
type GrpcConnectParams struct {
	ID            string     `json:"id"`            // 前端生成的调用 id（uuid）；Send/CloseSend/Disconnect 凭它定位
	Target        string     `json:"target"`        // host:port
	Plaintext     bool       `json:"plaintext"`     // true = 明文 h2c；false = TLS
	TLSSkipVerify bool       `json:"tlsSkipVerify"` // 跳过证书校验
	ServerName    string     `json:"serverName"`    // TLS server name 覆盖
	Metadata      []KeyValue `json:"metadata"`      // 调用元数据（反射请求同样携带）
	ProtoFiles    []string   `json:"protoFiles"`    // 本地 .proto 绝对路径；空 = 服务器反射
	ImportPaths   []string   `json:"importPaths"`   // proto 解析的 import 路径（各 proto 文件目录自动加入）
	Timeout       int        `json:"timeout"`       // 秒；0 = 默认 30（List 与一元调用生效，流式不限时）
	Method        string     `json:"method"`        // "pkg.Service/Method"（也接受点号分隔）
	// Payload 是一元/服务端流式的唯一请求消息（JSON）；客户端/双向流式忽略，经 Send 逐条发。
	Payload string `json:"payload"`
}

// GrpcMethodInfo 是目标的一个可调用方法。
type GrpcMethodInfo struct {
	Service         string `json:"service"`         // 所属服务的全限定名
	Name            string `json:"name"`            // 方法名
	FullName        string `json:"fullName"`        // "pkg.Service.Method"
	ClientStreaming bool   `json:"clientStreaming"` // 客户端流式（含双向）
	ServerStreaming bool   `json:"serverStreaming"` // 服务端流式（含双向）
	InputType       string `json:"inputType"`       // 请求消息的全限定名
	OutputType      string `json:"outputType"`      // 响应消息的全限定名
	InputTemplate   string `json:"inputTemplate"`   // 请求消息的 JSON 模板（默认值填充）
}

// GrpcServiceInfo 是目标提供的一个服务。
type GrpcServiceInfo struct {
	Name    string           `json:"name"` // 服务全限定名
	Methods []GrpcMethodInfo `json:"methods"`
}

// GrpcEvent 是调用生命周期事件，Connect 存续期间经 stream 推送。
type GrpcEvent struct {
	Type      string     `json:"type"`      // "open" | "message" | "error" | "close"
	Direction string     `json:"direction"` // message 专属："send" | "receive"
	Payload   string     `json:"payload"`   // message 的 JSON
	At        int64      `json:"at"`        // Unix 毫秒
	Code      string     `json:"code"`      // close：gRPC 状态码名（"OK" / "NOT_FOUND" ...）
	Reason    string     `json:"reason"`    // 状态描述/错误原因
	Headers   []KeyValue `json:"headers"`   // open：响应元数据
	Trailers  []KeyValue `json:"trailers"`  // close：trailer 元数据
}

type SavedAssertion struct { // 前端 AssertionRule 的持久化形态
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	Value   string `json:"value"`
}

type SavedMessage struct { // WS/TCP/Socket.IO 的已存消息模板（发送消息页左侧列表）
	ID      string `json:"id"`
	Name    string `json:"name"`
	MsgType string `json:"msgType"` // "text" | "json" | "xml" | "html" | "binary"
	Payload string `json:"payload"`
	// Event 是 Socket.IO 模板的事件名，空 = "message"；ws/tcp 为空。
	Event string `json:"event"`
}

type SavedSocketEvent struct { // Socket.IO 的事件定义（发送消息页的事件列表）
	ID          string `json:"id"`
	Name        string `json:"name"`        // 事件名：发送时作为 EVENT 包的事件名
	Enabled     bool   `json:"enabled"`     // 停用的事件不出现在发送消息页
	Description string `json:"description"` // 备注，不发送
}

// SavedExample 是响应示例：一次响应的快照，随请求条目落盘。
// 列表结构为将来多示例扩展预留，目前界面只保存一条（新示例覆盖旧的）。
type SavedExample struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`    // 展示名；空 = 界面按保存时间给
	SavedAt  int64       `json:"savedAt"` // Unix 毫秒
	Response APIResponse `json:"response"`
}

type SavedRequest struct { // RequestState 的快照 + 展示名
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "http" | "websocket" | "tcp" | "socketio" | "markdown"；空 = http（兼容旧 .band）
	// Markdown 是 kind="markdown" 的文档内容；其余 kind 恒为空串。
	Markdown        string             `json:"markdown"`
	Method          string             `json:"method"`
	URL             string             `json:"url"`
	Params          []KeyValue         `json:"params"`
	Headers         []KeyValue         `json:"headers"`
	BodyType        string             `json:"bodyType"`
	FormData        []FormField        `json:"formData"`
	URLEncoded      []KeyValue         `json:"urlEncoded"`
	Raw             string             `json:"raw"`
	RawType         string             `json:"rawType"`
	BinaryPath      string             `json:"binaryPath"`
	Auth            AuthInfo           `json:"auth"`
	Cookies         []KeyValue         `json:"cookies"`
	Timeout         int                `json:"timeout"`
	HTTP2           bool               `json:"http2"`
	PreScript       string             `json:"preScript"`  // 预执行脚本；空 = 无
	PostScript      string             `json:"postScript"` // 后执行脚本；空 = 无
	Assertions      []SavedAssertion   `json:"assertions"`
	Messages        []SavedMessage     `json:"messages"`        // WS/TCP/Socket.IO 使用；nil 落盘补 []（生成 TS 类型要求）
	SocketProtocol  string             `json:"socketProtocol"`  // WS 子协议（Sec-WebSocket-Protocol），逗号分隔；仅 WS 使用
	SocketEvents    []SavedSocketEvent `json:"socketEvents"`    // Socket.IO 事件定义；nil 落盘补 []（生成 TS 类型要求）
	SocketIOVersion string             `json:"socketIOVersion"` // Socket.IO 协议版本 "4"（默认）| "3"；仅 Socket.IO 使用
	SocketIOPath    string             `json:"socketIOPath"`    // Engine.IO 握手路径（空 = /socket.io/）；仅 Socket.IO 使用
	// gRPC 调用配置；仅 kind="grpc" 使用，其余 kind 保持零值。
	GrpcMethod        string   `json:"grpcMethod"`        // 选中的方法全名 "pkg.Service/Method"
	GrpcPlaintext     bool     `json:"grpcPlaintext"`     // true = 明文 h2c；false = TLS
	GrpcTLSSkipVerify bool     `json:"grpcTlsSkipVerify"` // 跳过证书校验
	GrpcServerName    string   `json:"grpcServerName"`    // TLS server name 覆盖
	GrpcProtoFiles    []string `json:"grpcProtoFiles"`    // nil 落盘补 []（生成 TS 类型要求）；空 = 服务器反射
	GrpcImportPaths   []string `json:"grpcImportPaths"`   // proto 解析的 import 路径；nil 落盘补 []
	// Examples 是响应示例（响应快照）；列表结构为将来扩展预留，目前只保存一条。nil 落盘补 []（生成 TS 类型要求）
	Examples []SavedExample `json:"examples"`
}

// SavedFolder 是工作区请求树里的一个文件夹；parentId 空 = 根目录。
// requests 按显示顺序存请求 ID；不在任何文件夹的请求平铺在根目录。
type SavedFolder struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ParentID string   `json:"parentId"`
	Requests []string `json:"requests"`
}

type WorkspaceData struct {
	Name     string            `json:"name"`
	Requests []SavedRequest    `json:"requests"`
	Vars     map[string]string `json:"vars"`    // 脚本变量；读写为 zip 内独立 vars.json
	Folders  []SavedFolder     `json:"folders"` // 请求树的分组；nil 落盘补 []（生成 TS 类型要求）
}

type WorkspaceFile struct {
	Path string        `json:"path"`
	Data WorkspaceData `json:"data"`
	// Encrypted 标记该 band 以加密信封保存；前端保存弹窗据此默认勾选
	// 「沿用原收件人」。v1 与明文 v2 恒为 false。
	Encrypted bool `json:"encrypted"`
	// SignatureTrusted 为 true 表示这是带有效签名的 v2 容器，且签名者在载入时已
	// 获得信任（持久信任或「信任一次」都算）——脚本无需再过脚本信任弹窗。
	// v1 与未签名 v2 恒为 false，弹窗照旧。
	SignatureTrusted bool `json:"signatureTrusted"`
}

// WorkspaceOpen 是 Open/OpenPath 的返回：File 与 Trust 至多一个非空。
// 用户取消对话框时两个都为空（等价旧的 WorkspaceFile{}）。
// File 必须是指针：struct 上的 omitempty 不会省略零值结构体，
// 非指针会让 file 永远出现在 JSON 里，前端把信任弹窗当成空文件静默吞掉。
type WorkspaceOpen struct {
	File  *WorkspaceFile `json:"file,omitempty"`
	Trust *TrustPrompt   `json:"trust,omitempty"`
}

// TrustPrompt 是 v2 容器里未知身份的签名者信息；信任弹窗据此展示。
type TrustPrompt struct {
	Path        string `json:"path"`
	ID          string `json:"id"`
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"` // sha256(ed25519 公钥) hex，展示取前 16 位
}

// SaveOptions 控制 Save 的加密行为；见「保存加密流程」。
type SaveOptions struct {
	Encrypt    bool     `json:"encrypt"`
	Recipients []string `json:"recipients"` // base64 X25519 公钥；空 + 已加密 band = 沿用原信封
}

// IdentityInfo 是本机身份（私钥永不出 Go 侧）。
type IdentityInfo struct {
	Status      string `json:"status"` // "unset" | "ready"
	ID          string `json:"id"`
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
	Ed25519     string `json:"ed25519"` // base64 公钥
	ECDH        string `json:"ecdh"`    // base64 公钥
	CreatedAt   int64  `json:"createdAt"`
}

// IdentityCandidate 是加密保存的收件人候选（只含公钥与展示信息）。
type IdentityCandidate struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
	ECDH        string `json:"ecdh"`
}

// TrustedIdentity 是 trusted.json 的行，也是信任列表的展示形态。
type TrustedIdentity struct {
	Fingerprint string `json:"fingerprint"`
	ID          string `json:"id"`
	Email       string `json:"email"`
	Ed25519     string `json:"ed25519"`
	ECDH        string `json:"ecdh"`
	AddedAt     int64  `json:"addedAt"`
}
