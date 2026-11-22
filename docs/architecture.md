# NetDream 架构与模块契约

仿 ApiPost「调试」页的桌面 API 调试工具（mygo：Go 后端 + 系统 webview 前端）。
本文件是前后端之间的**契约**，改动需同步更新。

## 目录结构

```
backend/
  main.go              # main()：Bind + 窗口（窗口常量）+ OnFileDrop 激活（拖入文件拿路径）+ guardWindow
  close.go             # 窗口关闭守卫：OnClose 否决 + workspace:closeRequested 事件 + SetDirty/CloseWindow 支撑
  types.go             # IPC 类型：KeyValue、FormField、AuthInfo、APIRequest、ResponseCookie、APIResponse、ScriptLog、ScriptTest、SSEOpen、SSEEvent、SSEMessage、SocketConnectParams、SocketSendParams、SocketEvent、SavedAssertion、SavedMessage、SavedSocketEvent、SavedRequest、SavedFolder、WorkspaceData、WorkspaceFile
  service.go           # RequestService：Send（脚本编排）、PickFile（仅有的两个导出方法）+ APIRequest↔protocol.HTTPRequest / APIResponse↔protocol.HTTPResponse 的机械映射
  socket.go            # SocketService 薄壳：Connect/Send/Disconnect 转调 protocol + *mygo.Channel[SocketEvent]→protocol.Sink 适配
  script.go            # 脚本引擎（goja）：VM 创建与 nd/console 注册、5s 超时与页面关闭中断、{{var}} 替换、预脚本写回请求
  script_test.go       # 脚本日志/测试结果的保留上限、按 rune 截断、Go 侧 pbkdf2 的可取消性
  ndapi/               # 脚本的 ND API（package ndapi）：nd.vars/request/response/test/assert/crypto/time、console，记录交主包 Recorder
  workspace.go         # WorkspaceService：Open/OpenPath/Save/SaveAs + .band（zip）读写 + 请求树归一化
  workspace_test.go    # .band 往返、旧格式兼容、folders 归一化（readBandBytes/writeBand）
  workspace_limits_test.go # .band 解压限额（单条/累计/归档本身）与 zip bomb 拒绝
  protocol/            # 网络协议实现（package protocol，不 import main；契约类型由 main 映射进来）
    types.go           # 自有的协议类型：KeyValue、FormField、AuthInfo、HTTPRequest/HTTPResponse、ResponseCookie、SSEOpen/SSEEvent/SSEMessage、Emit、ConnectParams、SendParams、SocketEvent、Sink
    http.go            # Do：URL/Body 构造、头与 Cookie、认证、超时、h2c、响应整形与截断（transport 进程级共享，代理按请求查；见 proxy*.go）
    sse.go             # SSE：Content-Type 判定、Open/Event 经 Emit 回调推送、事件流解析器（单行 1MiB、单事件 data 10MiB 限额）
    socket.go          # 连接注册表 + WS/TCP 读写循环 + Connect/Send/Disconnect
    socket_test.go     # Connect 的取消语义：WS/TCP 读中取消、握手中取消、Disconnect 结束读循环
    socketio.go        # Socket.IO（Socket.IO v5/EIO4 与 v2/EIO3）：握手、心跳、EVENT/ACK/CONNECT_ERROR、二进制占位
    proxy*.go          # 系统代理（Windows 注册表 / macOS scutil / 其他平台直连），build tag 跟文件走
frontend/
  index.html           # 静态骨架（id 见下）
  src/
    main.ts            # 入口：装配各模块；按编辑器 kind 切换 HTTP / Socket 视图
    monacoSetup.ts     # Monaco 按需装配：loadMonaco() 动态加载裁剪后的编辑器 + worker 注册 + nd API 补全库（extraLib）
    monacoContributions.ts # Monaco 保留的 contribution 清单（副作用导入，见「脚本编辑器」）
    state.ts           # 请求/响应/工作区/变量状态 + SSE 事件合帧与保留上限 + localStorage 持久化 + 事件
    socket.ts          # SocketSession（连接状态/日志）+ connect/send/disconnect 动作
    assert.ts          # 断言规则模型 + runAssertions 纯函数
    components/
      urlBar.ts        # 方法下拉 + URL + 发送按钮（kind 感知：Socket 显示 连接/断开）
      tabs.ts          # 通用选项卡
      kvTable.ts       # 通用键值编辑器（启用/键/值/描述/删除/添加）
      bodyEditor.ts    # Body 类型单选 + 五种子编辑器
      authEditor.ts    # 认证（none/basic/bearer）
      scriptEditor.ts  # Monaco 脚本编辑器（预/后执行脚本页，与 state 双向绑定）
      responseView.ts  # 响应区（选项卡 + 美化/原生/预览 + 头/Cookie/实际请求/控制台）
      socketComposer.ts  # Socket「发送消息」页：消息模板列表 + 编辑器 + 类型下拉（Socket.IO 另有事件名/Ack）+ 设置页（按 kind）
      socketEventsEditor.ts # Socket.IO「事件」页：事件名 + 监听开关 + 描述
      socketPanel.ts   # Socket 响应区：实时响应日志（发送/接收）+ 握手响应头
      assertionsPanel.ts # 断言与校验侧栏
      workspaceSidebar.ts # 工作区侧栏（新建/打开/保存/另存 + 新建请求菜单 + 变量区 + 请求列表）
      closeConfirm.ts  # 通用三键确认框（未保存守卫：窗口关闭 / 新建工作区 / 打开 / 拖入）
      scriptTrust.ts   # 脚本信任确认框（打开的 .band 带脚本时，首次执行脚本前问一次）
  src/mygo.ts          # `mygo generate` 生成，禁止手改
  style.css
scripts/
  sse-demo.ts          # 本地 SSE 测试服务器（http://localhost:8123/sse）
  socket-demo.ts       # 本地 WS/TCP 回显测试服务器（ws://localhost:8124，tcp://localhost:8125）
  socketio-demo.ts     # 本地 Socket.IO 演示服务器（http://localhost:6021/，命名空间 /admin）
resources/
  icon.png             # 应用图标（正方形 PNG）：mygo.config.ts 的 icon 指向它，
                       # 用于窗口标题栏/任务栏/打包产物；frontend/public/icon.png 是同一份拷贝（favicon 与设置页「关于」logo）
```

## IPC 契约（backend/types.go + backend/service.go + backend/socket.go + backend/workspace.go，唯一事实来源）

`mygo.Bind(RequestService{}, WorkspaceService{}, SocketService{})` 绑定。注意：**结构体的所有导出方法都会被绑定**，
各 Service 上只允许出现契约列出的导出方法（helper 必须小写或未导出）。
方法在自己的 goroutine 运行；第一个参数可为 `context.Context`（页面关闭自动取消）。
方法返回可以是 `error`、`(T, error)` 或无返回（mygo 反射均支持）。

**契约类型必须留在 main 包**：`mygo generate` 的文档注释提取只读绑定方法所在包的源码目录
（`backend/`），跨包类型会丢掉注释。网络协议实现放在 `backend/protocol`，它定义自己的一套
类型（`protocol.KeyValue`、`protocol.HTTPRequest` …），由 main 做机械映射；protocol 不 import main。

```go
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type FormField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ContentType string `json:"contentType"` // 覆盖该字段的 part Content-Type；空为默认
	Description string `json:"description"`
	// Kind 是字段类型："text"（默认，空同义）| "file"。
	Kind string `json:"kind"`
	// FilePath 是 Kind="file" 时文件的绝对路径；发送时读盘作为文件 part 上传
	//（单文件上限同 binary 的 100MB）。
	FilePath string `json:"filePath"`
}

type AuthInfo struct {
	Type     string `json:"type"` // "none" | "basic" | "bearer"
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

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
	Timeout    int         `json:"timeout"` // 秒；0 = 默认 30s
	HTTP2      bool        `json:"http2"`   // h2c：对 http:// 明文强制 HTTP/2（https 始终自动协商，忽略此项）
	// 预执行脚本（JS，goja 运行）；空 = 不执行。在 {{var}} 替换与请求组装之前运行。
	PreScript string `json:"preScript"`
	// 后执行脚本（JS，goja 运行）；空 = 不执行。拿到响应后运行。
	PostScript string `json:"postScript"`
	// 变量快照：{{var}} 替换与脚本 nd.vars 的数据源；脚本写回后经 APIResponse.Vars 返回。
	Vars map[string]string `json:"vars"`
}

type ResponseCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path"`
	Domain   string `json:"domain"`
	Expires  string `json:"expires"` // RFC3339；空 = 会话 Cookie
	Secure   bool   `json:"secure"`
	HttpOnly bool   `json:"httpOnly"`
}

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
	// 实际请求回显（「实际请求」页）
	FinalURL    string     `json:"finalURL"`
	SentHeaders []KeyValue `json:"sentHeaders"`
	SentBody    string     `json:"sentBody"` // 发送体的文本预览（截断至 64KiB）
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

// ScriptLog 是脚本里 console.log/warn/error 的一条输出。
type ScriptLog struct {
	Level string `json:"level"` // "log" | "warn" | "error"
	Text  string `json:"text"`  // 参数逐个 JSON 序列化后以空格拼接
	At    int64  `json:"at"`    // Unix 毫秒
}

// ScriptTest 是 nd.test(name, fn) 的一条断言结果。
type ScriptTest struct {
	Name  string `json:"name"`
	Pass  bool   `json:"pass"`
	Error string `json:"error"` // 失败原因；通过为空
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
	At        int64      `json:"at"`                  // Unix 毫秒
	Code      int        `json:"code,omitempty"`      // WS 关闭码；握手失败时为 HTTP 状态码
	Reason    string     `json:"reason,omitempty"`    // 错误/关闭原因
	Headers   []KeyValue `json:"headers,omitempty"`   // open：WS 握手响应头（按 key 排序、同名合并）
	Protocol  string     `json:"protocol,omitempty"`  // open：实际协商出的子协议
	// Event 是 Socket.IO 的事件名（收发 message 与 ack 回答都带）；ws/tcp 为空。
	Event string `json:"event,omitempty"`
	// IsAck 标记这条 message 是 Socket.IO 的 ack 回答（Event 为原事件名，Payload 为 ack 参数）。
	IsAck bool `json:"isAck,omitempty"`
}
```

方法：

- `Send(ctx context.Context, req APIRequest, stream *mygo.Channel[SSEMessage]) (APIResponse, error)` — 发送请求。
  实现在 `backend/protocol/http.go`（`Do`）+ `sse.go`：main 只做脚本编排与类型映射，
  SSE 的 `stream` 由 main 适配成 `protocol.Emit` 回调（`func(protocol.SSEMessage) error`），
  protocol 不依赖 IPC 的 Channel。
  - 普通响应：stream 不使用（Send 返回时自动关闭），行为同前；
  - SSE 响应（去参数后的 Content-Type 是 `text/event-stream`）：经 stream 先推一个
    `SSEMessage{Open}`（状态/协议/响应头），随后每条事件推 `SSEMessage{Event}`；
    不套用 30s 超时（只靠页面 close 通道或窗口关闭取消）；流正常结束返回
    APIResponse（meta，Body 为空，TimeMS 为全程耗时）；页面 close 导致 ctx 取消时
    返回 error，前端将其视为「用户停止」吞掉。
  - SSE 解析按规范：空行分派事件；`data:` 多行以 \n 拼接；`event:`/`id:` 透传；
    `:` 开头注释与 `retry:` 忽略。
  - SSE 两条限额，超出一条就报错结束流（前端显示这条错误，已收到的事件保留）：
    单行 >1MiB 视为不是可解析的事件流；**单事件**的 `data:` 累计 >10MiB 同样拒绝
    （一行一条的上限挡不住一个事件用成千上万行慢慢堆到吃光内存）。
  - Query 参数拼到 URL；BodyType 语义：
    - `formData` → `multipart/form-data`（文本字段直写；`Kind="file"` 的字段把
      `FilePath` 读盘作为文件 part 上传：filename=basename，Content-Type 取字段显式值
      否则 Peek 512 字节嗅探、兜底 octet-stream，单文件 ≤100MB；空 FilePath 报错。
      流式读盘不整块进内存。预览串无文件时 `[multipart/form-data]`，有文件时
      `[multipart/form-data: N 字段，含文件 a.png (123 bytes)]`）
    - `urlencoded` → `application/x-www-form-urlencoded`
    - `raw` → 按 RawType 设 Content-Type（json/text/xml/html），无自定义 Content-Type 头时生效
    - `binary` → 读 BinaryPath 文件（≤100MB），默认 `application/octet-stream`
    - `none`/其他 → 无 body
  - 用户显式设置了同名头时不覆盖（Content-Type / Authorization / Cookie 除外按下方规则）。
  - Auth：basic → `SetBasicAuth`；bearer → `Authorization: Bearer <token>`；
    仅当用户未显式提供 Authorization 头时设置。
  - Cookies：把启用项拼成 `Cookie` 头（`k=v; k2=v2`）；用户显式 Cookie 头优先。
  - 响应头按 key 排序、同名合并为多行；响应体 ≤10MiB，超出 Truncated=true。
  - 错误用 `fmt.Errorf("...: %w", err)` 包装后返回（前端会显示 message）；
    出错返回也携带 `Vars/ScriptLogs/ScriptTests`（生成的 TS 类型不可为 null）。
  - 预执行脚本（PreScript 非空）：在 {{var}} 替换与请求组装之前经 goja（backend/script.go）执行。
    出错则**不发送请求**，返回 `APIResponse{Vars, ScriptLogs, ScriptTests, PreScriptError}`
    且 error 为 nil（前端按失败展示，不当普通响应）。
  - `{{var}}` 替换：预脚本之后、组装之前生效，目标为 URL、Params/Headers/Cookies/
    URLEncoded 各键值、FormData 值、Raw、Auth 三项；变量名 TrimSpace，未定义保留 `{{var}}` 原样。
    无脚本但 Vars 非空时同样执行。
  - 后执行脚本（PostScript 非空）：拿到响应后执行（SSE 在流正常结束后，Body 为空）；
    出错只记 `PostScriptError`，已拿到的响应保留。
  - 脚本 API（同步；单次 5s 超时 + 页面关闭取消）：
    - 两条停止路径共用同一个 `context.WithTimeout(ctx, 5s)`：停在 JS 里的（死循环、
      `nd.test` 内的死循环）由 `vm.Interrupt` 打断；纯 Go 里烧时间的
      `nd.crypto.pbkdf2`（10M 轮上限，能跑满好几秒）在循环里每 256 轮看一眼这个
      context，自己抛 `ndapi.AbortError` 提前退出（`vm.Interrupt` 到不了 Go 循环）。
      两者最终都报既有的「超时：超过 5s 未执行完，已中断」/「已取消」两条消息。
    - `nd.vars`：get/set/unset/has/toObject（快照进、全量出，写回经 APIResponse.Vars）
    - `nd.request`：`{method,url,headers,query,body}`；预脚本可写并回写 DTO
      （headers/query 大小写不敏感 diff：消失或 null 删除、新写法覆盖；body 改了则
      BodyType=raw、RawType 空补 text），后脚本为只读快照
    - `nd.response`（仅后脚本）：`{status,statusText,headers,body,timeMS,sizeBytes}` + `json()`
      （`json()` 只调一次 `JSON.parse`：后端不再用 Go 预解析一遍，解析错误带
      `nd.response.json():` 前缀，脚本可以 try/catch 自己处理）
    - `nd.test(name, fn)`（fn 抛异常 = 失败，不中断脚本）、`nd.assert(cond, msg)`
    - `nd.crypto`：md5/sha1/sha256/sha512（encoding 默认 hex）、hmac、base64(+url)、
      hex、aesEncrypt/aesDecrypt（cbc 默认 PKCS#7 / gcm，key、iv 为 base64）、
      randomBytes/randomHex；字符串进出，非法参数一律抛 Error（backend/ndapi/crypto.go）
    - `nd.time`：`now()`/`unix()`（毫秒/秒时间戳）、`format(ms, layout, timeZone?)`
      （Go 布局 + IANA 时区，省略按本地）、`parse(s, layout, timeZone?)`（严格解析，
      失败返回 null 而非抛异常）、`offset()`/`zone()`（本地偏移分钟数，符号同
      Date.getTimezoneOffset()）——Date 的解析与 toLocale* 在 goja 里不可靠，
      时区相关一律走这里（backend/ndapi/time.go）
    - `console.log/warn/error(...)` → ScriptLogs（「控制台」页签），
      保留上限 1000 条、单条 64KiB（按 `utf8` 边界截断）；超出部分丢弃，
      并在末尾追加一条 warn 说明丢了多少条（`nd.test` 结果同理，上限 1000 条）。
      这只限制后端保留的量，不限制脚本自己的 JS 堆。
- `PickFile() (string, error)` — 原生打开文件对话框（`mygo.Dialog.Open`），
  用户取消时返回 `("", nil)`。

### SocketService（backend/socket.go + backend/protocol）

WebSocket / TCP / Socket.IO 流式调试。契约类型（`SocketConnectParams` 等）留在 main 包，
`SocketService` 只是薄壳：`Connect/Send/Disconnect` 转调 `netdream/backend/protocol`，
并把 main 的 `*mygo.Channel[SocketEvent]` 适配成 `protocol.Sink`（`Send(protocol.SocketEvent) error`）；
协议实现在 `backend/protocol`（`socket.go` 连接管理、`socketio.go` Socket.IO）。
依赖 `github.com/gorilla/websocket`。所有连接登记在该包的包级注册表
（`map[string]*socketConn` + `sync.Mutex`）中，支持多条并发连接；
`*socketConn` 内再有写互斥（WS/TCP 写不能并发）。helper（注册表、query 拼接、
TCP 文本判定、Socket.IO 包编解码）一律未导出。

- `Connect(ctx context.Context, params SocketConnectParams, stream *mygo.Channel[SocketEvent]) error`
  — 建立连接，方法在连接存续期间不返回（等同 SSE 的 Send）。
  - 注册表条目在 dial 前就位（含 cancel），因此 **Disconnect 在 dial 进行中也能生效**；
    id 重复返回 error。kind 只认 `"websocket"` / `"tcp"` / `"socketio"`。
  - WS：URL 必须是 `ws://` 或 `wss://`（其他 scheme 报错）；Params 拼到查询串；
    Headers 过滤 gorilla 托管头（大小写不敏感）：`Host`、`Upgrade`、`Connection`、
    `Sec-Websocket-Key`、`Sec-Websocket-Version`、`Sec-Websocket-Extensions`，
    其余原样传递（用户可用 `Sec-WebSocket-Protocol` 头自定义子协议，
    与 params.Protocol 二选一，params.Protocol 优先写入 dialer.Subprotocols）；
    `websocket.Dialer{HandshakeTimeout: 30s}` + `ReadLimit(10MiB)`，
    默认 ping/pong/close 控制帧处理。
  - TCP：`net.DialTimeout("tcp", url, 30s)`，URL 即 `host:port`。
  - 成功 → 推 `open`（WS/Socket.IO 带握手响应头——按 key 排序、同名合并，WS 还带协商子协议；
    TCP 无头），随后进入读循环：WS 每条消息推一条 `message`（text 原文 /
    binary base64+isBinary）；TCP 每次 `Read`（≤64KiB）推一条 `message`，
    `utf8.Valid` 视为文本，否则 base64+isBinary。
  - `Send` 成功写入后由 Connect 推 `message{direction:"send"}`（以后端确认准），
    前端不本地伪造发送日志。
  - 读循环因对端关闭/错误退出 → 推 `close`（WS 带 Code/Reason；TCP EOF 无码）后返回 nil。
  - 用户 Disconnect / 页面关闭（ctx 取消）→ 不再推事件，清理注册表并返回
    （error 由前端按「用户停止」吞掉，语义同 SSE）。
  - **取消要关传输，不只是标志位**：`ReadMessage` / `Read` 只会在套接字关闭时返回，
    所以 `Connect` 用 `context.AfterFunc(ctx, conn.stop)` 在取消时关掉传输（返回前
    `stopOnCancel()` 摘掉回调，避免回调泄漏；`stop` 幂等，重复调用安全）。
    WS 握手中取消原本要等满 30s `HandshakeTimeout`：dialer 的 `NetDialContext` 把
    拨号产生的原始 `net.Conn` 记在 `socketConn` 上，`stop` 一并关闭它。取消导致的
    dial 失败返回 `ctx.Err()`（前端在「用户停止」时不显示），而不是底层连接被重置的报错。
- `Send(params SocketSendParams) error` — 注册表定位连接；不存在/已关闭 → error；
  IsBinary 时 base64 解码失败 → error；WS 写 Text/Binary 消息，TCP 写原始字节；
  写失败返回 error（读循环随后以 close 收尾）。
- `Disconnect(id string)` — 定位并关闭连接（WS 先写 CloseMessage 再 Close），
  幂等（不存在则静默返回）；注册表在 Connect 的 defer 中移除。
- `Channel.Send` 自带互斥（mygo 实现保证并发推送有序），读循环与 Send
  从各自 goroutine 共推一个通道是安全的。

#### Socket.IO 支持（backend/protocol/socketio.go）

直连 WebSocket 传输的 Socket.IO 客户端（Socket.IO v5 协议 / Engine.IO v4，以及
Version="3" 时的 Socket.IO v2 / Engine.IO v3）；**不实现 long-polling，也不实现二进制附件重组**。

- 地址解析：URL 形如 `http(s)://host[:port][/命名空间]`（也接受 ws/wss）。握手地址 =
  `Path`（默认 `/socket.io/`，缺首尾斜杠自动补齐）+ `?EIO=<4|3>&transport=websocket`
  + 用户 Params（追加；`EIO`/`transport` 由后端 `Set`，用户参数不能覆盖）。
  scheme 映射 http→ws、https→wss；URL 的 path 非空且非 `/` 时作为 Socket.IO 命名空间
  （去掉尾部 `/`，如 `http://h/admin/` → `/admin`），命名空间不进握手地址，
  只出现在 CONNECT/EVENT/ACK/DISCONNECT 包里（`40/admin,`、`42/admin,[...]`）。
- 握手：读 `0{...}` Engine.IO open（取 `pingInterval`/`pingTimeout`，毫秒，缺失按
  Socket.IO 默认 25s/20s）→ 发 CONNECT（有命名空间则 `40/<nsp>,`）→ 等 `40{"sid":...}`
  （EIO3 是裸 `40`）即成功；`44...`（EIO4）或 `4{"message":...}`（EIO3）视为 CONNECT_ERROR，
  推 `error` 事件并让 Connect 返回 error。握手阶段用 30s 读截止。
- 心跳：EIO4 由服务器发 `2`，客户端立即回 `3`（带 payload 则原样带回）；读截止 =
  `pingInterval + pingTimeout`，每收到一个包就刷新，静默超时按连接死亡推 `close`。
  EIO3 方向相反：客户端按 `pingInterval` 起 ticker 发 `2`，服务器回 `3`，ticker 随
  ctx（连接关闭）退出。
- 收发事件：`42["event",arg]`（带命名空间 `42/admin,[...]`，带 ack id `42/admin,<id>[...]`）；
  发送时 Payload 先试 `json.Unmarshal`，成功则作为 JSON 值、失败则作为字符串，
  Event 空 = `"message"`。`Ack=true` 时分配连接内自增 id 并记入 pending 表；
  收到 `43<id>[...]` 推 `message{event: 原事件名, isAck: true, payload: ack 参数的 JSON}`，
  pending 表在连接关闭时清空（不做 ack 超时）。
- 接收 EVENT：解析事件名与参数，推 `message{direction:"receive", event, payload: 参数数组的 JSON 原文}`；
  入站事件带 ack id 时立即回空 ack（`43<id>[]`）——调试器不是服务端处理器，不让服务器白等。
- 类型 5/6（二进制事件/ack）：不重组附件，推一条 payload 为占位 JSON 的 `message`
  （含 attachments 数量），随后按数量跳过二进制帧；意料之外的二进制帧按 base64 推
  `message{isBinary:true}`。
- 断开：收到 `41`/Engine.IO `1` → 推 `close` 并结束；主动断开先发 `41`（有命名空间则
  `41/<nsp>,`）再关 WebSocket（不走 WS CloseMessage）。

前端目前已把 Socket.IO 接入为与 WS/TCP 平级的编辑器 kind（`REQUEST_KINDS` 含 `"socketio"`）。
后端通路保持不变，前端行为见「行为细节」节：

- **URL 与入口**：URL 栏接受 `http(s)://host[:port][/命名空间]`（本地校验：必须以
  `http://` / `https://` 开头且 host 非空，否则提示 `socket.urlNeedsSocketIO`）；
  侧栏「＋ 新建请求」有「Socket.IO 连接」一项，列表徽标 `IO`（`.m-badge.m-sio`）；
  自动命名为 `SIO host/path`。
- **请求页签**：发送消息 / 事件 / 请求头 / 请求参数 / 设置（`tabs.events`）。
  「请求头」「请求参数」与 WS 共用同一套编辑器（握手请求头、握手查询参数）。
- **事件过滤（Apipost 语义）**：「事件」页维护 `socketEvents`（事件名 + 监听开关 + 描述），
  后端推送全部入站事件，**过滤在 socket.ts 收事件时做**：`isAck` 为 true 的回答恒进日志，
  其余只有事件名在 `socketEvents` 里且 `enabled` 才进，否则丢弃。开关随时生效，
  不需要重连；一个都没勾时 open 之后追加一条灰色提示（`socket.noListeners`），
  避免日志一直空着被误认为连接坏了。
- **发送**：发送区多一行「事件名 + Ack」控件（ws/tcp 隐藏）。事件名留空 = 发 `"message"`；
  勾 Ack 时后端分配 ack id，回答以 `isAck:true` 的 receive 行显示并带 `ack` 标记。
  该 kind 下类型下拉不提供 Binary（后端未实现二进制附件重组），存成 Binary 的模板回落为文本。
  模板（`SavedMessage`）带上 `event`，ws/tcp 下恒为空。
- **设置**：客户端版本（`socketIOVersion`：`"4"` = Socket.IO v3/v4/v5 / EIO4，默认；
  `"3"` = Socket.IO v2 / EIO3）与握手路径（`socketIOPath`，空 = `/socket.io/`）。
  设置页按 kind 重画：WS 显示子协议，Socket.IO 显示这两项，TCP 无设置项。
- **日志展示**：行内事件名 chip（`.sock-event`）、ack 标记、二进制 `base64` 标记；
  「复制全部」的文本同样带 `[事件名 ack base64]`。计数 tooltip 按 kind 区分
  （`socket.wsConnection` / `socket.sioConnection` / `socket.tcpConnection`）。

### 工作区（.band）

`.band` 有两种容器，按文件头 sniff（前 4 字节）区分：

- **`PK\x03\x04` 开头 = v1**：裸 zip 压缩包，行为与历史版本完全一致（见下）。
- **`NDB2` magic 开头 = v2（当前写出版本）**：自定义容器
  `| "NDB2" | u32le 头长度 | header JSON（明文，≤1MiB） | zip 二进制区域 |`。
  压缩**先于**加密：zip 先 deflate，密文不经过压缩器，压缩率无损。

**zip 二进制区域**内部结构两种容器相同（`archive/zip`）：

```
workspace.json          # {"version": 1, "name": "工作区名", "folders": [...]}（版本字段留在 1，容器头接管版本）
engine/vars.json        # 脚本变量（Record<string,string>）；缺失/解析失败 = 空
requests/<id>.json      # 每个已保存请求 / Markdown 文档一个文件，JSON（SavedRequest，缩进格式）
```

**v2 header JSON（明文）**：

```json
{
  "format": "ndband", "version": 2,
  "identity": {"id", "email", "ed25519", "ecdh", "fingerprint"},
  "encrypted": true,
  "ephPub": "...", "keys": [{"hash", "wrapped"}],
  "cipher": {"algo": "AES-128-GCM-STREAM", "chunkSize": 4194304, "noncePrefix": "8B b64"},
  "zip": {"size": 12345, "digest": "sha256(zip 明文全部字节) hex"},
  "signedAt": 1735689600000,
  "signature": "base64(ed25519 签名)"
}
```

- 加密时 zip 区域 = **分块 AEAD 密文**（见「加密信封」），`workspace.json` 等条目
  不再单独加密；不加密则区域是明文 zip，`zip.digest` 照旧覆盖（防篡改）。
- `requests/*.json` 与 `engine/vars.json` 解析失败的条目跳过（vars 失败 = 空变量表，
  不报错）——两种容器一致。
- v1 归档照常读取，视为未签名、不弹信任窗。

**解压限额**（`readBand`/`readBandBytes`）：`.band` 是不可信输入，手工造一个 zip bomb
就能把内存吃光，所以读之前先设上限——归档本身 128MiB（先 `os.Stat` 再看是否读）、
单个条目 32MiB、累计解压 256MiB。限额留足正常内容（后端最大响应体 10MiB、Markdown
文档）的余量；判定既看条目声明的大小，也看实际读出的字节（声明值可以造假）。**超限与
"坏条目跳过"语义不同**：整个归档报错（`errBandLimit`，错误里点名是哪个条目/哪种限额），
不会安静地丢条目——用户会以为内容存下来了。

**请求分组（请求树）**：`folders` 是 `SavedFolder[]`，`[{id, name, parentId, requests}]`——
`parentId` 空串 = 根目录，`requests` 按显示顺序存请求 ID。层级只存在这一个字段里
（`requests/*.json` 不带归属信息），所以换工具打开时请求本身不受影响。

- **根目录平铺**：不在任何 `folders[].requests` 里的请求平铺在根目录。归属是
  「文件夹 → 请求 ID」单向引用，一个请求最多属于一个文件夹。
- **字段缺失 = 空结构**：旧 .band 的 `workspace.json` 没有 `folders` → 读出空数组，
  所有请求在根目录；落盘时**总是**写 `folders`（空也写 `[]`），否则旧归档里的树
  会留在原地。`WorkspaceData.folders` 的 nil 同样按 `[]` 处理（同 `filled` 的惯例）。
- **读侧归一化**（`normalizeFolders`，写读都经它，页面可直接信任）：
  - 文件夹的 `requests` 缺失 → `[]`；指向不存在的请求 ID 剔除；
  - 同一请求 ID 出现在多个文件夹 → 只有第一次出现生效，后面的剔除；
  - `parentId` 指向不存在的文件夹 → 提到根目录（`parentId` 置 `""`）；
  - 空 id / 重复 id 的文件夹丢弃（引用不出唯一节点）；
  - 父子成环（A 的父是 B、B 的父是 A）→ 环上每个成员都提到根目录，
    否则从根走不到、整棵子树在页面上画不出来。
- 写读顺序都保持：`folders` 数组顺序即同级顺序，`requests` 数组顺序即文件夹内顺序。

#### 身份与信任

应用持有**一组身份密钥**（backend/identity.go）：ed25519 签名密钥对 + X25519(ECDH)
密钥对，首次运行生成，存 `os.UserConfigDir()/NetDream/identity.json`（0600）；
`trusted.json`（同目录）存持久信任列表（含全公钥，为加密收件人准备），另有进程内
`trustedOnce` map 承载「信任一次」。每次成功载入或导入 band 都把签发者身份记入
`identities.json` 缓存（去重按指纹，上限 200 条），供加密保存的收件人选择。
**信任锚是公钥指纹**（ed25519 公钥的 sha256 hex，界面取前 16 位），id/email 只是
可改的展示信息，改 id 不影响旧签名包的验证。

签名覆盖 header 中除 `signature` 外全部字段（含 identity 与 zip.digest）：验证侧
重新 marshal 比对，任何未知字段/改动都令签名失效（fail closed）。**保存者永远用
自己的身份重签**——编辑别人的 band 再保存，签名与 identity 自然变成编辑者的。

打开顺序（v2）：sniff → 解头 → 加密则先查 keys 里有没有给自己的包裹（没有 =
「不是给你的」报错）→ 验签（身份在但签名不成立 = 硬错误；加密但无签名 = 拒绝）→
身份未知 → 挂起并返回 TrustPrompt（只含 header 信息，零解密成本）；确认后经
ResolveTrust 才做解密/直读 + 摘要比对 + 解 zip 条目。信任弹窗显示 ID + 邮箱 +
指纹，三键：**信任一次 / 信任公钥 / 取消**（取消丢弃挂起数据）。v1 无身份，不弹窗。

#### 加密信封与分块流式加密

zip 密钥 = 随机 16 字节（AES-128）。**加密对象是整个 zip 二进制区域**，分块 AEAD
（STREAM 构造）：4MiB 一块，每块 `nonce(12B) || AES-128-GCM(zip密钥, 块明文)`，
`nonce = noncePrefix(8B 文件级随机) || BE32(块序号)`——可流式、可逐块验 tag
（截断/篡改/错钥块粒度即败）、且块 nonce 由序号决定所以支持按块随机访问解密。
摘要 `zip.digest` 对**明文**流计算（写盘时边压缩边算）。

信封：临时 X25519 密钥对，`shared = ECDH(临时私钥, 收方公钥)`，
`wrapKey = HKDF-SHA256(salt=ephPub, info="netdream-band-wrap-v1", 16B)`，
`wrapped = nonce(12B) || AES-128-GCM(wrapKey, zip密钥)`；每个收方一个
`keys[] = {hash: sha256(收方 ecdh 公钥) hex, wrapped}`，明文 meta 里同时放 `ephPub`。

读：按 `sha256(本机 ecdh 公钥)` 找包裹 → 私钥解出 zip 密钥 → 区域流式解密。
限额按明文判定；归档本身仍受 128MiB 上限（头 ≤1MiB，区域明文 ≤128MiB）。

**重保存沿用原信封**：加密 band 保存时若未另选收件人，原样复用 zipKey + ephPub +
keys[]（能解密就必然能原样再加密，收件人不变；noncePrefix 每次重写都换新的，
(zipKey, nonce) 对不复用）。keys 只存收方公钥的 hash，旧收件人公钥不可恢复，
所以重保存不允许「追加收件人」——要换收件人群只能另存新文件并重新勾选（原发件人
公钥可从身份缓存拿到，其余旧收件人若公钥不在缓存则会失去访问权，界面不阻止但
文档注明）。

#### 保存加密流程（前端）

原生保存对话框（`AskSavePath`）→ 前端 modal「加密保存」勾选框 → 勾选后出现
收件人复选列表：**自己 + trusted.json + identities.json 缓存 + 从 .band 文件
导入公钥**（解析目标 band 的 header identity，加密 band 也可导入——头与身份恒为
明文）→ `Save(path, data, {encrypt, recipients})`。当前 band 已加密时勾选框默认
选中并注明「沿用原收件人」：一个都不勾 = 原信封重写；新勾选了人 = 全新信封
（只写给勾选的）。

绑定 `WorkspaceService`（导出方法）：

- `Open() (WorkspaceOpen, error)` — 打开文件对话框（过滤 `.band`），v2 走容器管线；
  返回 `{file}`（直接可加载，身份已信任或无签名）或 `{trust: TrustPrompt}`（身份未知，
  数据尚未解析，等用户决定）；用户取消返回空 `WorkspaceOpen`。
- `OpenPath(path string) (WorkspaceOpen, error)` — 不经对话框直接读指定 `.band` 路径
  （拖入打开用）；扩展名必须是 `.band`（大小写不敏感），否则报错。
- `ResolveTrust(fingerprint string, remember bool) (WorkspaceFile, error)` — 用户在信任
  弹窗选了「信任一次」（remember=false，进进程内 trustedOnce）或「信任公钥」
  （remember=true，追加 trusted.json 并返回 `{path, data}`。挂起槽单次，不匹配或过期报错。
- `DismissTrust()` — 「取消」：丢弃挂起数据。
- `AskSavePath(name string) (string, error)` — 原生保存对话框（默认文件名
  `name.band`，缺后缀自动补），只返回路径；取消返回 `("", nil)`。加密选择在后续的
  Save 参数里。
- `Save(path string, data WorkspaceData, opts SaveOptions) error` — 覆盖写。opts.encrypt
  + recipients 非空 = 新信封写给勾选的人；opts.encrypt + recipients 空 + 该 path
  当前持有加密信封 = 沿用原信封重写；其余情况按明文写。保存者永远用自己的身份签名。
- `SetDirty(dirty bool)` — 前端推送工作区脏状态；脏时关闭窗口会先问过页面。
- `CloseWindow()` — 页面用户选择「不保存」后调用：强制关闭窗口（一次性 force，
  见 backend/close.go）。
- 写盘（v2）：zip 先流进**区域临时文件**（需要加密则套分块加密写滤器，同时对明文跑
  sha256 得 zip.digest），完后用自己的身份签 header（含 digest），再写最终临时文件
  = magic + 头长 + header + 区域内容拷贝，rename 替换。仍然是同目录唯一临时文件 +
  原子 rename + 包级写锁（见 workspace.go），失败路径先关后删（Windows 限制）。响应
  示例每条可带 ≤10MiB 响应体，全程 O(块) 内存。读盘：v2 区域统一流式 spool 到临时
  文件（解密或直读都边读边算 digest），再 `zip.NewReader` 解条目——两种 v2 只读一遍
  原文件，且 zip 侧保持 seekable。

绑定 `IdentityService`（backend/identity.go）：

- `Get() IdentityInfo` — 当前身份；`status: "unset" | "ready"`（unset = 首次运行还没建）。
- `Setup(email string) (IdentityInfo, error)` — 首次运行创建：ed25519 + X25519 各一对；
  email 为空则随机「形容词+名词@主机名」。生成毫秒级，直接同步做。
- `Update(email, id string) (IdentityInfo, error)` — 改展示信息；指纹与密钥不变。
- `Candidates() []IdentityCandidate` — 加密保存收件人候选 = 自己 + trusted + 身份缓存。
- `ImportFromBand() (IdentityCandidate, error)` — 弹 .band 对话框，读其 header 的
  identity（加密 band 也可读），同时写入身份缓存；取消返回空。
- `Trusted() []TrustedIdentity` / `Untrust(fingerprint string)` — 信任列表查看/移除。

窗口关闭守卫（backend/close.go）：`Window.OnClose` 里 `force || !dirty` 才放行，
否则 `PreventDefault()` 并向页面发事件 `workspace:closeRequested`（bool 负载），
页面弹「保存 / 不保存 / 取消」；保存成功走 `SetDirty(false)`+`CloseWindow()`，
放弃直接 `CloseWindow()`。force 在放行时复位（一次一用）。

类型：

```go
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

type SavedRequest struct { // RequestState 的快照 + 展示名
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind"` // "http" | "websocket" | "tcp" | "socketio" | "markdown"；空 = http（兼容旧 .band）
	Markdown   string           `json:"markdown"` // kind="markdown" 的文档内容；其余 kind 恒为空串
	Method     string           `json:"method"`
	URL        string           `json:"url"`
	Params     []KeyValue       `json:"params"`
	Headers    []KeyValue       `json:"headers"`
	BodyType   string           `json:"bodyType"`
	FormData   []FormField      `json:"formData"`
	URLEncoded []KeyValue       `json:"urlEncoded"`
	Raw        string           `json:"raw"`
	RawType    string           `json:"rawType"`
	BinaryPath string           `json:"binaryPath"`
	Auth       AuthInfo         `json:"auth"`
	Cookies    []KeyValue       `json:"cookies"`
	Timeout    int              `json:"timeout"`
	HTTP2      bool             `json:"http2"`
	PreScript  string           `json:"preScript"`  // 预执行脚本；空 = 无
	PostScript string           `json:"postScript"` // 后执行脚本；空 = 无
	Assertions []SavedAssertion `json:"assertions"`
	Messages   []SavedMessage   `json:"messages"`       // WS/TCP/Socket.IO 使用；nil 落盘补 []（生成 TS 类型要求）
	SocketProtocol string       `json:"socketProtocol"` // WS 子协议（Sec-WebSocket-Protocol），逗号分隔；仅 WS 使用
	SocketEvents   []SavedSocketEvent `json:"socketEvents"` // Socket.IO 事件定义；nil 落盘补 []（生成 TS 类型要求）
	SocketIOVersion string      `json:"socketIOVersion"` // Socket.IO 协议版本 "4"（默认）| "3"；仅 Socket.IO 使用
	SocketIOPath    string      `json:"socketIOPath"`    // Engine.IO 握手路径（空 = /socket.io/）；仅 Socket.IO 使用
	Examples        []SavedExample `json:"examples"`    // 响应示例（响应快照）；列表结构为将来扩展预留，目前只保存一条；nil 落盘补 []
}

type WorkspaceData struct {
	Name     string            `json:"name"`
	Requests []SavedRequest    `json:"requests"`
	Vars     map[string]string `json:"vars"` // 脚本变量；读写为 zip 内 engine/vars.json
	Folders  []SavedFolder     `json:"folders"` // 请求树的分组；读写为 zip 内 workspace.json，nil 落盘补 []
}

// SavedFolder 是工作区请求树里的一个文件夹；parentId 空 = 根目录。
// requests 按显示顺序存请求 ID；不在任何文件夹的请求平铺在根目录。
type SavedFolder struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ParentID string   `json:"parentId"`
	Requests []string `json:"requests"`
}

type WorkspaceFile struct {
	Path string        `json:"path"`
	Data WorkspaceData `json:"data"`
	// SignatureTrusted：v2 已验签且签名者载入时已获信任（持久或「信任一次」）才
	// 为 true——前端据此跳过脚本信任弹窗。v1 与未签名 v2 恒为 false。
	SignatureTrusted bool `json:"signatureTrusted"`
}

// WorkspaceOpen 是 Open/OpenPath 的返回：File 与 Trust 至多一个非空（File 是
// 指针，struct 的 omitempty 省略不了零值，非指针会让空 file 永远出现在 JSON 里）。
// 用户取消对话框时两个都为空（等价旧的 WorkspaceFile{}）。
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
```

H2 说明：`https://` 始终经 ALPN 自动协商 HTTP/2（Proto 字段可见结果）；
`http://` 默认 HTTP/1.1，`APIRequest.HTTP2=true` 时改用
`golang.org/x/net/http2` 的 `Transport{AllowHTTP: true}` 走 h2c prior-knowledge。
h2c 经代理时由 `dialViaProxy` 手写 CONNECT：这条隧道握手带 30s 期限（代理可能接了
连接却一直不回应答），隧道建成后清掉期限，之后的读写交给请求自己的 ctx。

Transport 复用：普通请求与 h2c 各只有一个包级共享的 transport（`sync.Once` 建一次），
不再每次请求克隆一份 `http.DefaultTransport`——连接池与 TLS 会话因此跨请求复用。
`Proxy` 仍是 `proxyForRequest` 回调，所以「环境变量优先、系统代理兜底」照旧按请求
解析，改代理不用重启；`http.Transport` 与 `http2.Transport` 本身并发安全。

窗口：`Title "NetDream"`，1280×800（最小 960×600）。

## 前端契约

### index.html 骨架（id 固定，TS 挂载）

```html
<main id="app">
  <aside id="workspace-sidebar"></aside>   <!-- workspaceSidebar.ts -->
  <div id="main-area">
    <header id="topbar"></header>           <!-- urlBar.ts（kind 感知） -->
    <nav id="req-tabs"></nav>               <!-- tabs.ts：按 kind 切换页签集 -->
    <section id="req-panel"></section>      <!-- 各请求编辑器挂载点 -->
    <section id="response-pane">
      <!-- HTTP 响应区（kind=http 时可见） -->
      <nav id="res-tabs"></nav>
      <div id="res-content"></div>
      <aside id="assert-panel"></aside>    <!-- assertionsPanel.ts -->
      <!-- Socket 响应区（kind=websocket/socketio/tcp 时可见；#res-* 隐藏） -->
      <nav id="socket-res-tabs"></nav>
      <div id="socket-res-content"></div>
    </section>
  </div>
</main>
```

视图切换：编辑器 `kind` 变化时 main.ts 切两组响应区的 `hidden`——http 显示
`#res-tabs/#res-content/#assert-panel`、隐藏 `#socket-res-*`；socket 反之；
**markdown 把 `#req-tabs` 与整个 `#response-pane` 一起收起**（`[hidden]` 在各元素
的 display 规则里有 `display: none` 覆盖，UA 默认样式压不过），`#req-panel` 里
只显示 markdown 视图，`overflow` 放开为 `visible`（工具栏气泡向上弹，不能被面板裁剪）。
请求页签集同样按 kind 切换（见「行为细节」）。

### state.ts

- `RequestState`：method/url/params/headers/pathParams/bodyType/formData/urlEncoded/raw/rawType/
  binaryPath/auth/cookies/timeout/http2/preScript/postScript + assertion 规则数组；**WS/TCP/Socket.IO 追加**：
  `kind: "http" | "websocket" | "tcp" | "socketio" | "markdown"`（默认 "http"）、`messages: SavedMessage[]`
  （已存消息模板，Socket.IO 模板带 `event`）、`socketProtocol: string`（WS 子协议，设置页编辑）、
  `socketEvents: SavedSocketEvent[]`（Socket.IO 事件定义）、`socketIOVersion: string`
  （"4" 默认 / "3"，映射 `SocketConnectParams.version`）、`socketIOPath: string`
  （Engine.IO 握手路径，映射 `SocketConnectParams.path`）、`markdown: string`
  （kind="markdown" 的文档内容，其余 kind 恒为空串）。messages/socketEvents 与三个字符串字段
  随 .band 持久化并原样回读。
- 变量 store（应用级，随工作区文件保存）：`getVars/setVars/updateVars/subscribeVars`，
  **纯内存、不写 localStorage**（关闭即清空，避免凭据外泄）；`loadWorkspace` 用
  `file.data.vars` 整体替换（走不置脏的 replaceVars 安静路径），保存时
  `WorkspaceData.vars = getVars()` 写入 .band；`buildRequest()` 附带
  `preScript/postScript/vars`；发送返回时 `response.vars` 写回 store（带脚本时无条件下发，
  无脚本时后端原样返回故为空操作）。
- 工作区脏跟踪：`isWorkspaceDirty()` / `setWorkspaceDirty(dirty)`（置位时同步推送
  `WorkspaceService.setDirty`，驱动窗口关闭守卫）+ `getWorkspaceRevision()`。
  **所有置脏都走 `markWorkspaceDirty()`**（推进 revision 再置脏，已经脏的时候也推进，
  否则保存期间的新改动会被旧的保存当成「没变」）：`upsertEntry`、`deleteEntry`、
  `renameEntry`、`renameWorkspace`、`setVars`（内容真变时）、`addFolder`、`renameFolder`、
  `deleteFolder`、`moveRequest`、`newMarkdownDocument`，以及 **`updateRequest`——当
  `currentId` 指向已入册条目时**（URL/Header/Body/Markdown/响应示例的编辑都在工作区
  数据上改东西）；新建且尚未入册的请求（`currentId` 为 null）保持原来「不置脏」的
  语义。`applyRequest`（载入条目/工作区/本地快照）不走 `updateRequest`，因此载入
  不会误置脏。清脏都走 `clearWorkspaceDirty()`（`loadWorkspace`、`newWorkspace`，
  同样推进 revision——之前发起的保存在完成时能认出工作区已经换了）。
- 工作区身份号：`getWorkspaceGeneration()`，只在 `loadWorkspace` / `newWorkspace`
  推进，普通编辑不推进。异步回调（脚本信任确认）凭它判断「还是不是当时那份工作区」；
  不能拿 revision 当身份，敲一个字它就变了。
- 脚本信任（纯内存、不落盘）：`scriptsTrusted` + `scriptsTrustedInWorkspace()` /
  `trustWorkspaceScripts()`。初始与 `newWorkspace()` 恒可信（本地会话里的脚本是用户
  自己写的），`loadWorkspace()` 时 `scriptsTrusted = !workspaceHasScripts() ||
  file.signatureTrusted`——打开/拖入的 .band 只要任一条目带前后脚本就先标不可信，等
  用户在执行前确认；**例外**：v2 已验签且签名者已获信任（含「信任一次」）的工作区
  身份已密码学确认，`signatureTrusted=true`（后端 WorkspaceFile 字段），脚本直接放行。
- `ResponseState` 追加脚本产物（不持久化）：`scriptLogs/scriptTests/preScriptError/postScriptError`。
- 订阅-通知（简单 emitter）；请求字段变更即写入 `localStorage["netdream.request"]`（防抖 300ms）。
  快照**不含 `examples`**：响应示例可能带 ≤10MiB 的响应体，一条就能把配额撑满、把快照写坏；
  示例只随工作区落进 .band。读取仍按旧格式解析——老快照里带着 examples 也认得。
- 响应与断言结果**不**持久化；脚本产物（日志/测试/错误）同样不持久化；
  Socket 会话（日志/连接状态）也不持久化。
- `newRequest(kind)` 按类型重置编辑器；`newMarkdownDocument()` 新建未命名
  Markdown 文档**并入册**（currentId 指向新条目、置脏——文档没有「发送」动作，
  沿用请求的「执行后才收编」规则会无处可改名）；`snapshotCurrent()` 快照带
  kind/messages/socketEvents/markdown，命名规则：HTTP `METHOD host/path`；
  WS `WS host/path`；Socket.IO `SIO host/path`；TCP `TCP host:port`（截断 40 字符）；
  **markdown 返回空串**（无 URL 可推导，界面按当前语言给「未命名文档」，
  与工作区空名的显示约定一致）。
- `renameCurrentMarkdown(name)`：给当前文档改名；currentId 为 null 时（重启后
  会话恢复、内存工作区为空）先 `upsertEntry(snapshotCurrent())` 收编成新条目再改，
  名字不静默丢失。

### socket.ts

- `SocketSession`：`{ connId, kind, status: "idle"|"connecting"|"open"|"closed",
  log: SocketLogEntry[], handshakeHeaders: KeyValue[] | null, stoppedByUser: boolean }`，
  单例（应用单编辑器，同时只保一条连接）；`SocketLogEntry` =
  `{ id, at, direction: "send"|"receive"|"system", payload, isBinary, event?, isAck?, tone? }`，
  **上限 500 条**。
- `connectSocket()`：校验 URL（WS 须 `ws://`/`wss://` 开头；Socket.IO 须 `http://`/`https://`
  开头且 host 非空；TCP 须 `host:port`），不合法直接 `updateResponse({error})` 提示并返回；
  `connId = crypto.randomUUID()`；构造 `Channel<SocketEvent>` 调
  `SocketService.connect(params, channel)`（Socket.IO 额外带 `path`/`version`）：
  - `open` → status=open、记录握手头、**立即入册内存工作区**（同 SSE 语义）；
    Socket.IO 且没有任何启用的监听事件时追加一条灰色提示；
  - `message` → 追加日志（带事件名）；**Socket.IO 的入站事件在这里过滤**：`isAck` 恒显示，
    其余按 `socketEvents` 的 `enabled` 判定，未勾选的直接丢弃（不重连、即时生效）；
  - `error`/`close` → 日志记系统行（红/灰）并置 status=closed；
  - channel onclose → 清理 in-flight 状态；Connect 返回的拒绝若 `stoppedByUser` 则吞掉。
- `sendSocketMessage({payload, isBinary, event, ack})` → `SocketService.send`（发送日志由后端
  open 连接推回的 `direction:"send"` 事件产生，带事件名）；失败 updateResponse error。
- `disconnectSocket()`：置 stoppedByUser → `SocketService.disconnect(connId)`。
- **切换编辑器 kind 或载入其他工作区条目时，若连接开着先断开**（自动，不询问）。

### assert.ts（纯函数，便于测试）

```ts
export type AssertionType = "statusEquals" | "bodyContains" | "bodyNotContains" | "responseTimeLt";
export interface AssertionRule { id: string; enabled: boolean; type: AssertionType; value: string; }
export interface AssertionResult { rule: AssertionRule; pass: boolean; actual: string; }
export function runAssertions(rules: AssertionRule[], res: APIResponse): AssertionResult[]
```

- statusEquals：状态码 == Number(value)
- bodyContains / bodyNotContains：res.body 包含 / 不包含 value
- responseTimeLt：res.timeMS < Number(value)
- 禁用的规则跳过（不出现在结果里）；Number 解析失败 → pass=false，actual 提示格式错误。

### 行为细节

- 编辑器按 `kind` 四态渲染：
  - **urlBar**（kind 感知，订阅 kind 变化重渲染）：HTTP 不变（方法下拉 + 发送 + h2c）；
    WS/TCP/Socket.IO 隐藏方法下拉与 h2c，占位符 WS「请输入以 ws:// 或 wss:// 开头的地址」、
    TCP「请输入主机:端口，如 127.0.0.1:7000」、Socket.IO「http://localhost:6021」；
    右侧按钮随会话状态在 连接/断开 间切换（connecting/open 显示 断开）。
  - **请求页签集**：HTTP = 现有 8 页（预执行脚本 / 后执行脚本页挂 Monaco 编辑器，
    见 scriptEditor.ts；语法补全来自 monacoSetup.ts 的 nd API extraLib）；
    WS = 发送消息 / 请求头 / 请求参数 / 设置（设置 = 子协议输入框，映射 `socketProtocol`）；
    **Socket.IO = 发送消息 / 事件 / 请求头 / 请求参数 / 设置**（设置 = 客户端版本 +
    握手路径，映射 `socketIOVersion` / `socketIOPath`）；TCP = 发送消息。同样用 tabs.ts。
    - **编辑器按需加载**：Monaco 与 Vditor 都是首次切到对应页/条目时才 `import()`。
      `monacoSetup.ts` 导出 `loadMonaco(): Promise<MonacoApi>`，`scriptEditor.ts` 的
      `ensure()` 第一次被调用才建实例；`markdownEditor.ts` 同样（Vditor 与它的 CSS
      一起动态加载）。加载失败会清掉缓存的 promise，下次交互可重试。
    - **加载失败不吞**：宿主里插一块 `.editor-error`（`editor.loadFailed` 文案 +
      「重试」按钮）并 `console.error` 记一条；点重试重走同一次 ensure，成功后
      自动清掉提示。Vditor 另有构建超时（watchdog）走 `editor.loadTimeout`。
    - **Monaco 只打包用得上的部分**：入口是 `monaco-editor/editor/editor.api.js`，
      加上 `monacoContributions.ts` 里列的 contribution（查找/替换、折叠、多光标、
      注释、格式化、补全、悬浮、参数提示、括号匹配等）与 JavaScript/TypeScript
      语言注册；CSS/HTML/JSON 的 worker 与其它语言的语法包都不进产物——因此
      `.css/.html/.json` 文件没有高亮，其它语言按纯文本显示（脚本只写 JS）。
  - **发送消息页**（socketComposer.ts）：左侧消息模板列表（点击载入编辑器、双击名称
    行内重命名、行尾 × 删除、「+ 新增」把当前编辑器内容存为模板并选中）；右侧编辑器 =
    名称 + 类型下拉（Text/JSON/XML/HTML/Binary，映射 `SavedMessage.msgType`）+
    内容 textarea（Binary 时 placeholder 提示粘贴 base64）+ [发送]（未连接禁用）+
    [存为消息]；Socket.IO 在名称行下方多一行 事件名输入 + Ack 勾选（映射
    `sendSocketMessage({event, ack})`，事件名同时随模板存进 `SavedMessage.event`），
    且类型下拉不提供 Binary。
  - **事件页**（socketEventsEditor.ts，仅 Socket.IO）：四列表格 = 监听勾选 / 事件名 /
    描述 / ×（复用 kvTable 的 `kv-table` 栅格与输入样式，列宽由 `.sock-events-table` 覆盖）
    +「+ 添加事件」；直接读写 `getRequest().socketEvents`。勾选只影响日志过滤，不影响握手。
  - **Socket 响应区**（socketPanel.ts）：页签 = 实时响应 / 响应头(n)（TCP 隐藏响应头页）。
    实时响应 = 日志列表（时间 + 发送/接收徽章 + 事件名 chip + ack/二进制标记 + 转义
    `<pre>` 内容，error/close 系统行红/灰着色），自动滚底，工具栏 清空 / 复制全部；
    响应头页 = 握手响应头表格（来自 open 事件，n 为数量）。
- Path 页：从 URL 提取 `{placeholder}` 生成可编辑行；发送前把 `{k}` 替换为
  `encodeURIComponent(v)`（前端完成，不进 IPC）。
- 发送流程：构造 `new Channel<SSEMessage>(handler)` 传给 `RequestService.send(req, channel)`；
  handler 收到 `open` 切到流式视图（展示状态/协议/响应头），收到 `event` 追加事件行；
  点击「停止」调 `channel.close()`（Go 侧 ctx 取消、请求中断），用户主动停止时
  吞掉 Send 返回的取消错误。SSE 视图 = 事件表格（N/ID/事件名/Data）+ 自动滚底 + 停止按钮。
  - 事件**按帧合并**：`pushStreamEvent` 只入队，`requestAnimationFrame`（没有则 16ms
    定时器）到期才并进 `runtime.events` 并通知一次——一帧里来几十条事件只画一次。
    流结束/停止/切请求都走 `updateResponse`，它会**先落地排队的事件**再打补丁，
    所以最后一帧不会丢。
  - **保留上限**：最多 5000 条、Data 合计 32MiB，超出从最旧的丢；后台标签页里 rAF
    暂停，队列本身也按同样上限丢（`updateResponse` 会立刻把它们落地）。
    界面显示「事件 N 条」+ 丢弃了多少条（橙色提示）。
  - **事件是增量画的**：`responseView` 用事件序号 `n`（同一条流里连续递增）对齐
    DOM——条数到顶后长度不再变化，靠 `getStreamRevision()` 知道有新事件；先按
    `n` 差值删掉头部已被淘汰的行（差值大于现有行数时夹紧，因为排队里被丢掉的事件
    从未画过），再从数组对应位置追加新行，只更新计数与「已丢弃」文案。整表重画只留给
    数组被清空/换掉、或序号倒退这种对不上的情况。
  返回后：`response.vars` 写回变量 store（带脚本时无条件下发后端快照）；
  `preScriptError` 非空按失败展示（错误条「预执行脚本出错」，response 置空）；
  脚本日志/测试/错误随响应落 ResponseState。
- **执行隔离（main.ts 的 `Execution`）**：每次发送建一份不可变身份——执行号、
  发送时的编辑器对象引用、请求快照（`buildRequest()`，含 vars）、条目快照
  （`snapshotCurrent()`）、断言快照、是否带脚本。所有异步落点（channel handler、
  `channel.onclose`、Send 的 resolve/reject）先过 `stillCurrent(execution)`：
  `inFlight.id === execution.id && getRequest() === execution.editor`（换条目/载入工作区/
  新建请求都会换掉编辑器对象）。归属不符就整段丢弃——响应、错误、`response.vars`、
  断言结果都不写进当前编辑器，也**不写任何工作区**（入册已在发送开始时做过）。
  `syncEditor` 发现编辑器被换掉时解除在途句柄并清空瞬态响应（发送/流式/事件/结果/
  脚本产物），停止按钮因此只作用于当前编辑器的那次执行（在途旧请求不会被强行取消，
  只是结果不再回到界面）。发送期间全局仍只允许一次发送；切换条目后允许发下一条，
  两条执行各自隔离。
- URL 栏右侧一个「h2c」小复选框（强制 HTTP/2 明文），映射 `APIRequest.http2`。
- 响应工具栏在状态徽章旁显示协议版本（`response.proto`，如 HTTP/2.0）。
- Body 类型单选：none / form-data / urlencoded / binary / raw；raw 选中时出现子类型
  下拉（JSON/Text/XML/HTML）。
- 响应区选项卡：实时响应 / 响应头(n) / Cookie / 实际请求 / 响应示例 / 控制台
  （n 为数量）。响应示例页 = 已存示例的只读展示（状态徽章 + 保存时间 + 美化/原生/
  预览/复制 + 大叉删除），无示例时给引导文案；示例存编辑器状态 `examples`
  （SavedExample 列表，目前只存一条，为多示例扩展预留），随请求快照进 .band；
  实时响应工具栏有「存为响应示例」图标按钮（无响应时禁用）。控制台页 = 脚本 console 输出
  （时间 + 级别徽标 log 灰/warn 橙/error 红 + 等宽文本）与两条脚本错误行，
  空态「暂无日志」。
- 实时响应工具栏：美化 | 原生 | 预览（iframe `sandbox=allow-scripts` srcdoc，注入 `<base href=请求URL>`，相对资源可加载）、存为响应示例（图标按钮）、复制按钮；
  美化 = JSON 可解析时 pretty+高亮；否则识别 HTML 时格式化缩进+标签/属性/注释高亮；再否则转义文本；状态徽章按时态着色（2xx 绿/3xx 蓝/4xx 橙/5xx 红）。
- 断言面板：开关折叠；规则列表（启用框、类型下拉、期望值输入、删除）；
  发送后逐条显示 ✓ 绿 / ✗ 红 + actual 值。规则下方「脚本测试 n / m」分组
  （有 scriptTests 时才显示）：✓/✗ + 名称，失败附 error 文本。

### 工作区（前端）

- `WorkspaceState`（state.ts，**不**进 localStorage 编辑器存档）：
  `name: string`、`path: string | null`、`entries: SavedRequest[]`（生成类型）、
  `folders: SavedFolder[]`（请求树的分组，生成类型）、
  `currentId: string | null`（编辑器当前对应的条目 id；null = 未保存的新请求）、
  `encrypted: boolean`（当前 .band 是否加密保存；载入、另存、保存成功后按
  `WorkspaceFile.encrypted` / `SaveOptions.encrypt` 写回，v1 与明文恒为 false）。
- 请求分组（folders）：归属只存在 `folders[].requests` 里（文件夹 → 请求 ID），
  没被任何文件夹引用的请求平铺在根目录；`parentId` 空串 = 根。
  归一化（孤儿请求、重复归属、悬空 parentId、环）由 Go 侧读取 .band 时做完，
  前端直接信任；`loadWorkspace` 整块替换、`newWorkspace` 清空。
  动作（都置脏）：`addFolder(parentId: string | null)`（追加到同级末尾，名字留空
  由界面进改名态）、`renameFolder(id, name)`、`deleteFolder(id)`（**内容上移一级**：
  请求并进父文件夹、父是根就回根平铺；子文件夹 re-parent 到它的父级）、
  `moveRequest(requestId, folderId: string | null)`（从原文件夹摘掉、追加到目标
  末尾；null = 移回根目录）。`deleteEntry` 同步把该请求从所有 `folders[].requests`
  剔除；`upsertEntry` 不动 folders，已有请求的归属因此保持不变。
- 动作：`loadWorkspace(file: WorkspaceFile)`（整体载入——含 vars 整体替换、folders
  整块替换，并加载第一条请求进编辑器）、`newWorkspace()`（重置为空工作区「未命名工作区」：
  path=null、entries=[]、folders=[]、currentId=null、变量清空、编辑器重置为空 HTTP 请求、清脏）、
  `newRequest(kind)`（按类型重置编辑器、currentId=null）、`loadEntry(id)`、
  `deleteEntry(id)`（删的是当前条目则 currentId=null）、`renameEntry(id, name)`、
  `renameWorkspace(name)`、`snapshotCurrent(): SavedRequest`（编辑器 → 快照；命名规则
  见 state.ts 节：HTTP `METHOD host/path`、WS `WS host/path`、TCP `TCP host:port`，
  截断 40 字符）。
- state.ts 需导出 `applyRequest(next: RequestState)`：整体替换编辑器状态 +
  syncPathParams + scheduleSave + notify（loadSavedRequest / loadEntry 共用）；kind 切换
  由 main.ts 联动（响应区两组 hidden、页签集、urlBar）并调 socket.ts 的「切换先断开」。
- 自动入册：**发送一开始就把发送时的条目快照 `upsertEntry` 一次**（`snapshotCurrent()`
  与 `buildRequest()` 同时取），之后 open、流事件、完成、失败、手动停止都**不再碰工作区**
  ——等待期间的编辑由编辑器与保存快照负责，晚到的完成也绝不会覆盖它们，更不会把
  旧工作区的请求写进后来打开的工作区。预脚本失败也算执行过（与「无论成败都收」
  一致）；新建请求执行一次后即以自动名出现在列表。**WS/TCP/Socket.IO 在 connect 的
  open 事件时按当前编辑器入册，到达 closed（断开、被拒、失败）再刷新一次**——失败也收；
  切换 kind / 载入其它条目先断开，那时编辑器 URL 与连接对不上，不会误收。
  **派生新条目规则泛化为：HTTP 比较 method+URL、WS/TCP/Socket.IO 比较 URL——与已入册
  条目不一致即视为新请求，执行后派生新条目，原条目原样保留**；未变的编辑按 id 原地
  刷新，**改过名的条目保留别名**。
- 打开流程（main.ts，`WorkspaceOpen` 两种形态）：`Open` / `OpenPath` 都返回
  `{file}`（可直接加载）、`{trust}`（签发者未知，后端只解到 header）或空（对话框取消）。
  两个入口（侧栏「打开」、拖入 .band）都过 `handleOpenResult`：`file` → `guardUnsaved`
  后 `loadWorkspace`；`trust` → `components/bandTrust.ts` 三键框（「信任一次」/
  「信任公钥」/「取消」，展示 ID、邮箱与指纹前 16 位，默认焦点在取消）→
  `resolveTrust(fingerprint, remember)` 拿回 `WorkspaceFile` 再走同一条加载路径；
  取消走 `dismissTrust()` 丢弃挂起数据。指纹对不上或挂起槽已过期按错误条展示并中止
  加载。信任弹窗挂着期间新的打开/拖入触发被忽略（后端挂起槽只留一份读盘），
  ID/邮箱一律 textContent 写入。
- 保存流程（main.ts）：内存即真相——「保存」把 `WorkspaceState` 整块
  `{name, entries, folders}` + 当前变量表（`vars: getVars()`）写进 .band。路径先行：
  有 path 直接用，无 path（或「另存为」）先 `AskSavePath(name)`，返回空串 = 取消。
  拿到路径后**总是**弹 `components/saveDialog.ts`（`{encrypt, recipients}` 的 Promise）：
  「加密保存」勾选框 + 收件人复选列表（`IdentityService.candidates()`，可「从 .band
  导入公钥…」= `importFromBand()` 后自动勾选该条）；已加密的 band 默认勾选并注明
  「未勾选任何收件人 = 沿用原收件人」，但只有写回当前文件才谈得上沿用（后端按路径记
  信封），否则一个都没勾时前端就地报「请至少选择一个收件人」、不发请求。确认后
  `Save(path, data, opts)`；成功后写回 path 与 `encrypted = opts.encrypt`。取消保存选项
  = 没写盘。保存失败在响应区错误条显示。
  两次保存**串行排队**（`saveChain`），后一次等前一次写完再取快照，连点不会旧数据覆盖新数据。
  清脏带**版本号守卫**：`performSave` 先 `upsertEntry(snapshotCurrent())`、再记下
  `getWorkspaceRevision()` 与 `getWorkspaceGeneration()`，写盘返回后只有 revision 没变
  才 `setWorkspaceDirty(false)` 并返回 true。**revision 变过（期间又改了东西、或工作区
  已被换掉）时返回 false**：脏状态保留，未保存守卫（关窗 / 打开 / 新建的确认框）因此
  不会继续，避免把保存期间的新改动丢掉；用户再点一次保存即可（第二次快照已含新改动）。
  「另存为」的路径回写按 generation 判断（工作区被换掉就不绑到旧文件）。普通保存写盘
  成功但仍有新改动是正常状态——写进去的是保存开始时的快照。返回值同时给守卫用：
  saveAs 被取消、写盘失败、revision 变过都是 false。
- 未保存守卫（components/closeConfirm.ts 通用三键确认框，单例）：脏时弹
  「保存 / 继续不保存 / 取消当前操作」。四个触发点——窗口 X（Go 否决关闭并发
  `workspace:closeRequested` 事件，直接弹框，文案「保存 / 不保存 / 取消」）、
  新建工作区（「保存 / 取消保存 / 取消新建」）、打开 .band、拖入 .band
  （后三个走 main.ts `guardUnsaved`，**文件选定/读取成功后才弹框**；保存成功才继续，
  saveAs 取消则留在应用）。Esc / 点遮罩 = 取消；保存中三键禁用。
- 身份（`components/firstRun.ts`、`components/settings.ts`）：启动时
  `IdentityService.get()`，`status === "unset"` 弹首次运行窗（说明 + 邮箱输入 +
  「跳过」/「确定」；跳过 = `setup("")` 由后端生成随机「形容词名词@主机名」）。
  该窗**不可绕过**：不注册 Esc、点遮罩也不关，二选一都会真的建立身份；失败留在框内
  显示。已有身份永不弹。设置页「身份」分区：邮箱/ID 可编辑（各一个「保存」→
  `update(email, id)`，空串 = 该项不动）、指纹前 16 位、ed25519/ecdh 只读公钥 +
  「复制」（`navigator.clipboard`，失败退回选中文本）、信任公钥列表（id/email/指纹
  前 8 位 + 「移除」→ `untrust`，空列表给空态文案）。进页与打开设置各重取一次身份，
  取到的身份缓存在 state.ts（`getCachedIdentity` / `setCachedIdentity`）。
- 新组件文件：`components/firstRun.ts`（首次运行身份窗）、`components/bandTrust.ts`
  （打开时的签名者信任窗）、`components/saveDialog.ts`（保存选项：加密 + 收件人）。
- **脚本信任边界**（不可信 .band 的预脚本能外带数据）：打开的 .band 里的预脚本能读
  `nd.vars`、请求头、请求体，改写 URL/请求，再由后端附上 Basic/Bearer 发出——一份
  文件就足以把变量里的凭据送到任意地址。所以 `loadWorkspace` 时 `scriptsTrusted =
  !workspaceHasScripts() || file.signatureTrusted`（**本地恢复的会话与 `newWorkspace` 恒
  可信**，没有脚本的工作区不受影响；**v2 已验签且签名者已获信任——含「信任一次」——的
  工作区也直接可信**，身份已经密码学确认过，v1 与未签名 v2 不在其列），`send()` 在带
  脚本的请求真正发出前检查 `scriptsTrustedInWorkspace()`，
  不可信就只弹 `components/scriptTrust.ts` 的两键框（「信任并运行 / 不运行」，默认焦点
  在不运行），**不发送**；弹框时记下 `{generation, editor}`，确认时**只有两者都还匹配**
  才 `trustWorkspaceScripts()` 并重新走一遍 `send()`，否则什么都不做——弹框期间拖入/打开
  另一份工作区不会被这次确认放过。信任只对本次载入有效：重新打开 .band、拖入另一个
  或新建工作区都会重置（generation 推进），切换条目也绕不过（逐次发送都校验）。
  文案在 `dialog.scriptTrust*`（三语）。
- UI（components/workspaceSidebar.ts）：index.html 结构改为
  `<main id="app">` 两列网格——左 `<aside id="workspace-sidebar">`，原全部内容包进
  `<div id="main-area">`（**原有 id 一律不动**，组件无感）。
  侧栏内容：工作区名（双击行内重命名，title 显示文件路径）；按钮行「打开 / 保存 / 另存 / 新建」
  （两字一组，title 给全名；「新建」= 新建工作区，脏时先过未保存守卫）；
  「＋ 新建请求」为下拉菜单（HTTP 请求 / WebSocket 连接 / TCP 连接 / Socket.IO 连接 /
  Markdown 文件，末行分隔线后是「新建文件夹」，选中调 `newRequest(kind)` /
  `newMarkdownDocument()` / `addFolder(null)`）；
  可折叠「变量」区（kvTable 编辑全局 vars，无启用/描述列；
  脚本 setVars 写回时表格随订阅重建）；请求列表（徽章按 kind：HTTP = 方法徽章按方法着色（现状）、
  WS = 绿色 `WS` 徽章、TCP = 紫色 `TCP` 徽章、Markdown = 蓝色 `MD` 徽章 + 名称 + 行尾 × 删除，
  × 需 window.confirm 确认；双击名称行内重命名）；点击行加载；当前条目高亮；
  空工作区显示引导文案。
- 请求树（同一组件）：列表是树——文件夹行（折叠箭头 + 名称 + hover 时的
  「＋ 新建子文件夹」「× 删除文件夹」）+ 缩进的内容容器；同一级「文件夹在前、
  请求在后」，同级顺序 = `folders` 数组顺序，缩进靠行上的 `--ws-depth`。
  文件夹内容可多级嵌套；**折叠状态是组件内 `Set`，不落盘**（`hidden` 切内容容器，
  不重画树；重画后仍保留，换工作区时剪掉已不存在的文件夹 id）。双击文件夹名
  行内改名（同请求行）；新建文件夹后直接进改名态，取消则保留「未命名文件夹」。
  删除文件夹要 confirm（文案说明内容上移一级）。
- 拖拽移动：请求行 `draggable`（dragstart 记下请求 id，dragover 读不到 payload 所以
  存在组件状态里）；文件夹行是 drop 落点（dragover preventDefault + `.drag-over`
  高亮，drop 调 `moveRequest(id, folderId)`）；列表根部（空白处或**根目录的请求行**）
  也是落点，调 `moveRequest(id, null)`——文件夹内部的请求行不当作根落点，避免在
  文件夹里拖动时被误判成移回根目录。页面内拖拽不触发 `.band` 拖入的 `ws-drop` 虚线框。
- 拖入打开：`.band` 文件拖到**窗口任意位置**即可打开。机制是 mygo 原生文件
  拖放而非 webview 的 File API：`main.go` 在窗口上注册 `Window.OnFileDrop`
  监听器激活路径投递（至少要有一个 Go 侧监听器，原生层才会接受未被页面消费的
  文件拖放），前端经 mygo-runtime 的 `onFileDrop` 拿到**绝对路径**后调
  `WorkspaceService.openPath` 读盘。侧栏保留 dragover 高亮虚线框（只作视觉
  反馈，不再消费 drop 事件）；非 `.band` 文件在响应区错误条提示；
  一次拖多个文件取第一个 `.band`。拖入打开带真实路径，「保存」直接写回原文件。
  **落点路由**：onFileDrop 负载含视口坐标 `{paths,x,y}`——`elementFromPoint(x,y)`
  命中 form-data 文件控件（`[data-file-drop-index]`）时把 `paths[0]` 填进该行
  （`setFormFilePath`），不触发工作区打开；其余落点才走上述 .band 流程。
- form-data 文件行（kvTable 的 opt-in `withFileKind` + `pickFile`，仅 bodyEditor
  的 formData 表开启）：每行「类型」分段切换文本/文件；文件行值单元格换成文件控件
  （basename 或占位「未选择文件」、完整路径在 title、「选择」按钮经
  `RequestService.pickFile()` 选盘），容器带 `data-file-drop-index` 与 dragover
  高亮（纯视觉反馈）。`FormRow` 带 `kind/filePath`，随 .band 持久化（旧数据按文本行读）。
  **异步回写守卫**：原生对话框返回是要等的，期间可能切条目/换工作区/删掉那一行，
  所以两处（form-data 行与 binary 的「选择文件」）都在发起时捕获当前 `RequestState`
  对象（form-data 还捕获那一行的对象引用），返回后只有两者都还是同一个才写回；
  对不上就丢弃这次选择。拖入路径是同步写回，不走这条守卫。

### Markdown 文档

- 条目模型见上：kind="markdown"、内容存 `SavedRequest.markdown`，与请求共用
  .band 的 `requests/<id>.json`、工作区树、改名、删除、拖动；旧归档无该字段读出空串。
- 编辑器 `components/markdownEditor.ts`（Vditor 4，**ir 即时渲染**默认，工具栏
  可切分屏 / 所见即所得）。实例惰性创建（首次切到文档条目才加载 lute ≈3.6MB），
  之后常驻；语言切换重建实例，主题切换走 `setTheme`。Vditor 的 JS 与 CSS 用
  动态 `import()` 一起加载（`loadVditor()` 缓存同一个 promise，失败清掉以便重试），
  从没打开过 Markdown 条目的会话不为它付启动开销；20s 没初始化完成就放弃并释放
  创建闸门（下次切回可重试）。每次创建带一个递增 token：超时、销毁、语言重建都让
  它作废，迟到的 `after()` 绝不写进当前实例；盒子仍归这次创建时顺手
  `destroy()` 掉，盒子已经交给更新的实例时只丢弃不 destroy（Vditor 的 `destroy()`
  会还原「构造它的那个元素」的 innerHTML，做了等于删掉新实例的 DOM）。初始化期间
  界面语言变了，`after()` 发现语言不一致就丢弃重建。
- **资源本地化离线可用**：`scripts/prepare-vditor.mjs`（predev/prebuild 自动跑）
  把 node_modules/vditor/dist 里用到的部分（css、emoji 图、i18n、icons、
  highlight.js、lute、mermaid）拷进 `frontend/public/vditor/dist`，初始化 `cdn: "vditor"`；
  数学公式（katex/mathjax）与 echarts 等冷门图表**不随包分发**——相应选项关闭
  （inlineMath/mathBlockPreview=false），文档里出现那些代码块按普通代码块显示。
- **工具栏剔除** upload/record/export：无上传地址会卡上传态，录音走 getUserMedia
  在桌面端挂起，导出触发 HTML 下载——对本地文档编辑器都不适用。
- 顶栏（urlBar 自管，kind==="markdown" 分支）：隐藏方法选择器 / URL / h2c，
  「发送」槽位换成**存入工作区**（`upsertEntry(snapshotCurrent())`，不写 .band，
  按钮文案闪「已存入工作区」），旁边是**文档名输入框**（输入即 `renameCurrentMarkdown`，
  回车/失焦去空格）。.band 落盘仍归侧栏保存/另存。
- 内容双向绑定走 get/set/subscribe 约定（同 scriptEditor）；外部载入整篇替换并
  `clearStack()`；Vditor 的 localStorage 缓存关闭（cache.enable=false），
  持久化归 state.ts 的编辑器快照（300ms 防抖）。

### 多语言（src/i18n/）

- 词典 `src/i18n/locales/{zh-CN,en-US,ja}.json`：嵌套 JSON、点路径键、`{{var}}`
  插值；zh-CN 是兜底语言，缺键先回退 zh-CN 再回退键名（便于发现漏键）。
- `t(key, vars?)` 用**生效语言**；`getLang()` 生效语言，`getLangPref()` 偏好
  （`auto` 或具体语言）；持久化 localStorage `netdream.lang`，缺省 `auto`。
- 「跟随系统」的探测源是 **mygo-runtime 的 `locale()`**（MyGo 把系统首选语言与
  Go 侧 `SetLanguages` 声明的语言匹配后注入页面，见 backend/main.go——**漏调
  SetLanguages 时支持列表只有英语，`App.Locale()` 恒为 en**；WebView2 的
  `navigator.language` 不跟系统语言走，中文系统也报 en，不能用）；页面跑在
  MyGo 窗口外（纯浏览器调试）时取不到，退回 `navigator.language`，再缺回落
  zh-CN。映射：zh*→zh-CN、ja*→ja、其余→en-US。`onLocaleChange` 广播在 auto
  下实时跟随系统语言切换。
- 静态文案：元素标 `data-i18n="节.键"`（textContent）或
  `data-i18n-attr="placeholder:节.键;title:节.键"`，`applyStatic()` 统一刷新并
  同步 `<html lang>`；动态文案在 render 代码里用 `t()`。
- 语言切换重刷：组件在 mount 时自己 `onLangChange(cb)`（**mount 返回的是卸载
  函数，不能当刷新函数收集**），卸载时退订；重画不得丢用户态——改名/输入进行
  中的组件跳过会吞输入的重画，SSE/Socket 日志区重画滚动回底（与本来 render 一致）。
- 断言 actual、socket 系统日志这类**事后仍需随语言变**的文本存 `{key, vars}`
  结构（assert.ts 的 AssertionResult.actual、socket.ts 的 SocketLogEntry.i18n），
  渲染时再过 `t()`；用户数据（工作区名/请求名/URL/消息体）绝不进词典。
- 新 UI 字符串必须三语同键入词典；Monaco 编辑器自身 UI 保持英文，
  `monacoSetup.ts` 里文是脚本 API 的 JSDoc（悬浮文档用），不翻。
- 设置弹层「界面设置」页里的语言下拉是入口；切语言不重载页面、不动 state。

### 主题（src/theme.ts）

- 浅色 / 深色 / 跟随系统；持久化 localStorage `netdream.theme`
  （light/dark/auto，缺省 auto）；`<html data-theme>` 驱动 style.css：
  `dark` 恒暗、`light` 恒亮、`auto` 走 `@media (prefers-color-scheme: dark)`。
- 暗色取值在 style.css 里写两份（媒体查询内 `data-theme="auto"` + 媒体查询外
  `data-theme="dark"`，纯 CSS 无法共用），**改配色必须两处同步**（注释已标明）。
- 首屏防闪烁：index.html 在样式表前有一小段内联同步脚本落 `data-theme`，
  执行时机早于模块脚本；auto 模式下系统配色变化由 CSS 接管，但 Monaco 需要
  `monacoSetup.monacoTheme()` + `onThemeChange` 手动 `setTheme` 跟随。

### 约定

- 原生 TS + strict（`noUncheckedIndexedAccess`），不引入任何运行时 npm 依赖
  （例外：`monaco-editor`（预/后执行脚本编辑器）与 `vditor`（Markdown 文档），
  两者都按需 `import()`；`html-minifier-terser` 仅构建期压缩 HTML，不进产物运行时）。
- 模块级调用 mygo API 前先过 `isMyGo()`（src/runtime.ts）：纯浏览器里没有
  `globalThis.mygo`，入口模块求值抛错会让入口 chunk 进入 errored 状态，凡是静态
  `import` 它的动态 chunk 都会在 `import()` 时重抛同一错误——Monaco 曾因此整条
  懒加载链失败（现在 `events.workspaceCloseRequested.on` 已守卫）。
- UI 文案多语言（zh-CN 兜底 + en-US + ja），一律走 src/i18n 词典；
  样式按 ApiPost 截图：浅底、圆角卡片、蓝色主色 `#3b82f6`。
- 生成文件 `src/mygo.ts` 只能通过 `bun run generate` 更新。

## 命令

```bash
go build ./... && go vet ./...   # 根目录
bun run generate                 # 重新生成前端绑定
bun run typecheck                # = frontend tsc --noEmit
bun run dev                      # 开发（热重载）
bun run sse:demo                 # 本地 SSE 测试服务器（http://localhost:8123/sse）
bun run socket:demo              # 本地 WS/TCP 回显（ws://localhost:8124，tcp 127.0.0.1:8125）
bun run socketio:demo            # 本地 Socket.IO 演示服务器（http://localhost:6021/，命名空间 /admin）
go test -race ./backend/...      # 连接有并发，改连接/流代码后跑一遍
bunx mygo vet backend            # 或 backend 下 node_modules/.bin/mygo vet .（= Go vet，无 go tool mygo）
```
