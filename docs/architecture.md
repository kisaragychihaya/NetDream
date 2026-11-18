# NetDream 架构与模块契约

仿 ApiPost「调试」页的桌面 API 调试工具（mygo：Go 后端 + 系统 webview 前端）。
本文件是前后端之间的**契约**，改动需同步更新。

## 目录结构

```
backend/
  main.go              # main()：Bind + 窗口（窗口常量）+ OnFileDrop 激活（拖入文件拿路径）+ guardWindow
  close.go             # 窗口关闭守卫：OnClose 否决 + workspace:closeRequested 事件 + SetDirty/CloseWindow 支撑
  types.go             # IPC 类型：KeyValue、FormField、AuthInfo、APIRequest、ResponseCookie、APIResponse、ScriptLog、ScriptTest、SSEOpen、SSEEvent、SSEMessage、SocketConnectParams、SocketSendParams、SocketEvent、SavedAssertion、SavedMessage、SavedRequest、WorkspaceData、WorkspaceFile
  service.go           # RequestService：Send、PickFile（仅有的两个导出方法）
  request.go           # 未导出 helper：URL/Body 构造、头与 Cookie、响应整形、截断、h2c 传输
  script.go            # 脚本引擎（goja）：预/后执行脚本 + {{var}} 替换，全部未导出
  sse.go               # SSE：Content-Type 判定、Open/Event 推送、事件流解析器
  socket.go            # SocketService：Connect/Send/Disconnect + 连接注册表 + WS/TCP 读写循环
  workspace.go         # WorkspaceService：Open/OpenPath/Save/SaveAs + .band（zip）读写
frontend/
  index.html           # 静态骨架（id 见下）
  src/
    main.ts            # 入口：装配各模块；按编辑器 kind 切换 HTTP / Socket 视图
    monacoSetup.ts     # Monaco 装配：worker 注册 + nd API 补全库（extraLib）
    state.ts           # 请求/响应/工作区/变量状态 + localStorage 持久化 + 事件
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
      socketComposer.ts  # Socket「发送消息」页：消息模板列表 + 编辑器 + 类型下拉
      socketPanel.ts   # Socket 响应区：实时响应日志（发送/接收）+ 握手响应头
      assertionsPanel.ts # 断言与校验侧栏
      workspaceSidebar.ts # 工作区侧栏（新建/打开/保存/另存 + 新建请求菜单 + 变量区 + 请求列表）
      closeConfirm.ts  # 通用三键确认框（未保存守卫：窗口关闭 / 新建工作区 / 打开 / 拖入）
  src/mygo.ts          # `mygo generate` 生成，禁止手改
  style.css
scripts/
  sse-demo.ts          # 本地 SSE 测试服务器（http://localhost:8123/sse）
  socket-demo.ts       # 本地 WS/TCP 回显测试服务器（ws://localhost:8124，tcp://localhost:8125）
resources/
  icon.png             # 应用图标（正方形 PNG）：mygo.config.ts 的 icon 指向它，
                       # 用于窗口标题栏/任务栏/打包产物；frontend/icon.png 是同一份拷贝（favicon）
```

## IPC 契约（backend/types.go + backend/service.go + backend/workspace.go，唯一事实来源）

`mygo.Bind(RequestService{}, WorkspaceService{}, SocketService{})` 绑定。注意：**结构体的所有导出方法都会被绑定**，
各 Service 上只允许出现契约列出的导出方法（helper 必须小写或未导出）。
方法在自己的 goroutine 运行；第一个参数可为 `context.Context`（页面关闭自动取消）。
方法返回可以是 `error`、`(T, error)` 或无返回（mygo 反射均支持）。

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
	At        int64      `json:"at"`                  // Unix 毫秒
	Code      int        `json:"code,omitempty"`      // WS 关闭码；握手失败时为 HTTP 状态码
	Reason    string     `json:"reason,omitempty"`    // 错误/关闭原因
	Headers   []KeyValue `json:"headers,omitempty"`   // open：WS 握手响应头（按 key 排序、同名合并）
	Protocol  string     `json:"protocol,omitempty"`  // open：实际协商出的子协议
}
```

方法：

- `Send(ctx context.Context, req APIRequest, stream *mygo.Channel[SSEMessage]) (APIResponse, error)` — 发送请求。
  - 普通响应：stream 不使用（Send 返回时自动关闭），行为同前；
  - SSE 响应（去参数后的 Content-Type 是 `text/event-stream`）：经 stream 先推一个
    `SSEMessage{Open}`（状态/协议/响应头），随后每条事件推 `SSEMessage{Event}`；
    不套用 30s 超时（只靠页面 close 通道或窗口关闭取消）；流正常结束返回
    APIResponse（meta，Body 为空，TimeMS 为全程耗时）；页面 close 导致 ctx 取消时
    返回 error，前端将其视为「用户停止」吞掉。
  - SSE 解析按规范：空行分派事件；`data:` 多行以 \n 拼接；`event:`/`id:` 透传；
    `:` 开头注释与 `retry:` 忽略。
  - Query 参数拼到 URL；BodyType 语义：
    - `formData` → `multipart/form-data`（仅文本字段；字段 ContentType 非空时写 part 头）
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
  - 脚本 API（同步；单次 5s `vm.Interrupt` 超时 + 页面关闭取消）：
    - `nd.vars`：get/set/unset/has/toObject（快照进、全量出，写回经 APIResponse.Vars）
    - `nd.request`：`{method,url,headers,query,body}`；预脚本可写并回写 DTO
      （headers/query 大小写不敏感 diff：消失或 null 删除、新写法覆盖；body 改了则
      BodyType=raw、RawType 空补 text），后脚本为只读快照
    - `nd.response`（仅后脚本）：`{status,statusText,headers,body,timeMS,sizeBytes}` + `json()`
    - `nd.test(name, fn)`（fn 抛异常 = 失败，不中断脚本）、`nd.assert(cond, msg)`
    - `console.log/warn/error(...)` → ScriptLogs（「控制台」页签）
- `PickFile() (string, error)` — 原生打开文件对话框（`mygo.Dialog.Open`），
  用户取消时返回 `("", nil)`。

### SocketService（backend/socket.go）

WebSocket / TCP 流式调试。依赖 `github.com/gorilla/websocket`。所有连接登记在
包级注册表（`map[string]*socketConn` + `sync.Mutex`）中，支持多条并发连接；
`*socketConn` 内再有写互斥（WS/TCP 写不能并发）。helper（注册表、query 拼接、
TCP 文本判定）一律未导出。

- `Connect(ctx context.Context, params SocketConnectParams, stream *mygo.Channel[SocketEvent]) error`
  — 建立连接，方法在连接存续期间不返回（等同 SSE 的 Send）。
  - 注册表条目在 dial 前就位（含 cancel），因此 **Disconnect 在 dial 进行中也能生效**；
    id 重复返回 error。
  - WS：URL 必须是 `ws://` 或 `wss://`（其他 scheme 报错）；Params 拼到查询串；
    Headers 过滤 gorilla 托管头（大小写不敏感）：`Host`、`Upgrade`、`Connection`、
    `Sec-Websocket-Key`、`Sec-Websocket-Version`、`Sec-Websocket-Extensions`，
    其余原样传递（用户可用 `Sec-WebSocket-Protocol` 头自定义子协议，
    与 params.Protocol 二选一，params.Protocol 优先写入 dialer.Subprotocols）；
    `websocket.Dialer{HandshakeTimeout: 30s}` + `ReadLimit(10MiB)`，
    默认 ping/pong/close 控制帧处理。
  - TCP：`net.DialTimeout("tcp", url, 30s)`，URL 即 `host:port`。
  - 成功 → 推 `open`（WS 带握手响应头——按 key 排序、同名合并，及协商子协议；
    TCP 无头），随后进入读循环：WS 每条消息推一条 `message`（text 原文 /
    binary base64+isBinary）；TCP 每次 `Read`（≤64KiB）推一条 `message`，
    `utf8.Valid` 视为文本，否则 base64+isBinary。
  - `Send` 成功写入后由 Connect 推 `message{direction:"send"}`（以后端确认准），
    前端不本地伪造发送日志。
  - 读循环因对端关闭/错误退出 → 推 `close`（WS 带 Code/Reason；TCP EOF 无码）后返回 nil。
  - 用户 Disconnect / 页面关闭（ctx 取消）→ 不再推事件，清理注册表并返回
    （error 由前端按「用户停止」吞掉，语义同 SSE）。
- `Send(params SocketSendParams) error` — 注册表定位连接；不存在/已关闭 → error；
  IsBinary 时 base64 解码失败 → error；WS 写 Text/Binary 消息，TCP 写原始字节；
  写失败返回 error（读循环随后以 close 收尾）。
- `Disconnect(id string)` — 定位并关闭连接（WS 先写 CloseMessage 再 Close），
  幂等（不存在则静默返回）；注册表在 Connect 的 defer 中移除。
- `Channel.Send` 自带互斥（mygo 实现保证并发推送有序），读循环与 Send
  从各自 goroutine 共推一个通道是安全的。

### 工作区（.band）

`.band` 文件 = zip 压缩包（`archive/zip`），内部结构：

```
workspace.json          # {"version": 1, "name": "工作区名"}
engine/vars.json        # 脚本变量（Record<string,string>）；缺失/解析失败 = 空
requests/<id>.json      # 每个已保存请求一个文件，JSON（SavedRequest，缩进格式）
```

`workspace.json` 的 `version` 必须为 1，否则报错；`requests/*.json` 与
`engine/vars.json` 解析失败的条目跳过（vars 失败 = 空变量表，不报错）。

绑定 `WorkspaceService`（注意导出方法仅限这六个）：

- `Open() (WorkspaceFile, error)` — 打开文件对话框（过滤 `.band`），读 zip 返回
  `{path, data}`；用户取消返回 `WorkspaceFile{}`（Path 为空串，前端据此判取消）。
- `OpenPath(path string) (WorkspaceFile, error)` — 不经对话框直接读指定
  `.band` 路径（拖入打开用）；扩展名必须是 `.band`（大小写不敏感），否则报错。
  与 Open 相同返回 `{path, data}`——**带真实路径，保存直接写回原文件**。
- `Save(path string, data WorkspaceData) error` — 覆盖写 zip 到 path。
- `SaveAs(data WorkspaceData) (string, error)` — 保存对话框（默认文件名 `name.band`，
  缺 `.band` 后缀自动补），写 zip 返回实际路径；取消返回 `("", nil)`。
- `SetDirty(dirty bool)` — 前端推送工作区脏状态；脏时关闭窗口会先问过页面。
- `CloseWindow()` — 页面用户选择「不保存」后调用：强制关闭窗口（一次性 force，
  见 backend/close.go）。
- 写文件先写 `path + ".tmp"` 再 `os.Rename`，避免写一半损坏原文件。

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

type SavedMessage struct { // WS/TCP 的已存消息模板（发送消息页左侧列表）
	ID      string `json:"id"`
	Name    string `json:"name"`
	MsgType string `json:"msgType"` // "text" | "json" | "xml" | "html" | "binary"
	Payload string `json:"payload"`
}

type SavedRequest struct { // RequestState 的快照 + 展示名
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind"` // "http" | "websocket" | "tcp"；空 = http（兼容旧 .band）
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
	Messages   []SavedMessage   `json:"messages"`       // 仅 WS/TCP 使用；nil 落盘补 []（生成 TS 类型要求）
	SocketProtocol string       `json:"socketProtocol"` // WS 子协议（Sec-WebSocket-Protocol），逗号分隔；仅 WS 使用
}

type WorkspaceData struct {
	Name     string            `json:"name"`
	Requests []SavedRequest    `json:"requests"`
	Vars     map[string]string `json:"vars"` // 脚本变量；读写为 zip 内 engine/vars.json
}

type WorkspaceFile struct {
	Path string        `json:"path"`
	Data WorkspaceData `json:"data"`
}
```

H2 说明：`https://` 始终经 ALPN 自动协商 HTTP/2（Proto 字段可见结果）；
`http://` 默认 HTTP/1.1，`APIRequest.HTTP2=true` 时改用
`golang.org/x/net/http2` 的 `Transport{AllowHTTP: true}` 走 h2c prior-knowledge。

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
      <!-- Socket 响应区（kind=websocket/tcp 时可见；#res-* 隐藏） -->
      <nav id="socket-res-tabs"></nav>
      <div id="socket-res-content"></div>
    </section>
  </div>
</main>
```

视图切换：编辑器 `kind` 变化时 main.ts 切两组响应区的 `hidden`——http 显示
`#res-tabs/#res-content/#assert-panel`、隐藏 `#socket-res-*`；socket 反之。
请求页签集同样按 kind 切换（见「行为细节」）。

### state.ts

- `RequestState`：method/url/params/headers/pathParams/bodyType/formData/urlEncoded/raw/rawType/
  binaryPath/auth/cookies/timeout/http2/preScript/postScript + assertion 规则数组；**WS/TCP 追加**：
  `kind: "http" | "websocket" | "tcp"`（默认 "http"）、`messages: SavedMessage[]`
  （已存消息模板）、`socketProtocol: string`（WS 子协议，设置页编辑）。
- 变量 store（应用级，随工作区文件保存）：`getVars/setVars/updateVars/subscribeVars`，
  **纯内存、不写 localStorage**（关闭即清空，避免凭据外泄）；`loadWorkspace` 用
  `file.data.vars` 整体替换（走不置脏的 replaceVars 安静路径），保存时
  `WorkspaceData.vars = getVars()` 写入 .band；`buildRequest()` 附带
  `preScript/postScript/vars`；发送返回时 `response.vars` 写回 store（带脚本时无条件下发，
  无脚本时后端原样返回故为空操作）。
- 工作区脏跟踪：`isWorkspaceDirty()` / `setWorkspaceDirty(dirty)`（置位时同步推送
  `WorkspaceService.setDirty`，驱动窗口关闭守卫）。置脏：`upsertEntry`、`deleteEntry`、
  `renameEntry`、`renameWorkspace`、`setVars`（内容真变时）；清脏：`loadWorkspace`、
  `newWorkspace`、保存成功。`loadEntry` 与编辑器字段编辑不置脏。
- `ResponseState` 追加脚本产物（不持久化）：`scriptLogs/scriptTests/preScriptError/postScriptError`。
- 订阅-通知（简单 emitter）；请求字段变更即写入 `localStorage["netdream.request"]`（防抖 300ms）。
- 响应与断言结果**不**持久化；脚本产物（日志/测试/错误）同样不持久化；
  Socket 会话（日志/连接状态）也不持久化。
- `newRequest(kind)` 按类型重置编辑器；`snapshotCurrent()` 快照带 kind/messages，
  命名规则：HTTP `METHOD host/path`；WS `WS host/path`；TCP `TCP host:port`（截断 40 字符）。

### socket.ts

- `SocketSession`：`{ connId, kind, status: "idle"|"connecting"|"open"|"closed",
  log: SocketLogEntry[], handshakeHeaders: KeyValue[] | null, stoppedByUser: boolean }`，
  单例（应用单编辑器，同时只保一条连接）；`SocketLogEntry` =
  `{ id, at, direction: "send"|"receive", payload, isBinary }`，**上限 500 条**。
- `connectSocket()`：校验 URL（WS 须 `ws://`/`wss://` 开头；TCP 须 `host:port`），
  不合法直接 `updateResponse({error})` 提示并返回；`connId = crypto.randomUUID()`；
  构造 `Channel<SocketEvent>` 调 `SocketService.connect(params, channel)`：
  - `open` → status=open、记录握手头、**立即入册内存工作区**（同 SSE 语义）；
  - `message` → 追加日志；`error`/`close` → 日志记系统行（红/灰）并置 status=closed；
  - channel onclose → 清理 in-flight 状态；Connect 返回的拒绝若 `stoppedByUser` 则吞掉。
- `sendSocketMessage({payload, isBinary})` → `SocketService.send`（发送日志由后端
  open 连接推回的 `direction:"send"` 事件产生）；失败 updateResponse error。
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

- 编辑器按 `kind` 三态渲染：
  - **urlBar**（kind 感知，订阅 kind 变化重渲染）：HTTP 不变（方法下拉 + 发送 + h2c）；
    WS/TCP 隐藏方法下拉与 h2c，占位符 WS「请输入以 ws:// 或 wss:// 开头的地址」、
    TCP「请输入主机:端口，如 127.0.0.1:7000」；右侧按钮随会话状态在 连接/断开 间切换
    （connecting/open 显示 断开）。
  - **请求页签集**：HTTP = 现有 8 页（预执行脚本 / 后执行脚本页挂 Monaco 编辑器，
    见 scriptEditor.ts；语法补全来自 monacoSetup.ts 的 nd API extraLib）；
    WS = 发送消息 / 请求头 / 请求参数 / 设置（设置 = 子协议输入框，映射 `socketProtocol`）；
    TCP = 发送消息。同样用 tabs.ts。
  - **发送消息页**（socketComposer.ts）：左侧消息模板列表（点击载入编辑器、双击名称
    行内重命名、行尾 × 删除、「+ 新增」把当前编辑器内容存为模板并选中）；右侧编辑器 =
    名称 + 类型下拉（Text/JSON/XML/HTML/Binary，映射 `SavedMessage.msgType`）+
    内容 textarea（Binary 时 placeholder 提示粘贴 base64）+ [发送]（未连接禁用）+
    [存为消息]。
  - **Socket 响应区**（socketPanel.ts）：页签 = 实时响应 / 响应头(n)（TCP 隐藏响应头页）。
    实时响应 = 日志列表（时间 + 发送/接收徽章 + 转义 `<pre>` 内容，二进制标注 base64，
    error/close 系统行红/灰着色），自动滚底，工具栏 清空 / 复制全部；响应头页 =
    WS 握手响应头表格（来自 open 事件，n 为数量）。
- Path 页：从 URL 提取 `{placeholder}` 生成可编辑行；发送前把 `{k}` 替换为
  `encodeURIComponent(v)`（前端完成，不进 IPC）。
- 发送流程：构造 `new Channel<SSEMessage>(handler)` 传给 `RequestService.send(req, channel)`；
  handler 收到 `open` 切到流式视图（展示状态/协议/响应头），收到 `event` 追加事件行；
  点击「停止」调 `channel.close()`（Go 侧 ctx 取消、请求中断），用户主动停止时
  吞掉 Send 返回的取消错误。SSE 视图 = 事件表格（N/ID/事件名/Data）+ 自动滚底 + 停止按钮。
  返回后：`response.vars` 写回变量 store（带脚本时无条件下发后端快照）；
  `preScriptError` 非空按失败展示（错误条「预执行脚本出错」，response 置空）；
  脚本日志/测试/错误随响应落 ResponseState。
- URL 栏右侧一个「h2c」小复选框（强制 HTTP/2 明文），映射 `APIRequest.http2`。
- 响应工具栏在状态徽章旁显示协议版本（`response.proto`，如 HTTP/2.0）。
- Body 类型单选：none / form-data / urlencoded / binary / raw；raw 选中时出现子类型
  下拉（JSON/Text/XML/HTML）。
- 响应区选项卡：实时响应 / 响应头(n) / Cookie / 实际请求 / 控制台（n 为数量）；
  「响应示例」渲染为占位灰字「即将支持」。控制台页 = 脚本 console 输出
  （时间 + 级别徽标 log 灰/warn 橙/error 红 + 等宽文本）与两条脚本错误行，
  空态「暂无日志」。
- 实时响应工具栏：美化 | 原生 | 预览（iframe `sandbox=allow-scripts` srcdoc，注入 `<base href=请求URL>`，相对资源可加载）、复制按钮；
  美化 = JSON 可解析时 pretty+高亮；否则识别 HTML 时格式化缩进+标签/属性/注释高亮；再否则转义文本；状态徽章按时态着色（2xx 绿/3xx 蓝/4xx 橙/5xx 红）。
- 断言面板：开关折叠；规则列表（启用框、类型下拉、期望值输入、删除）；
  发送后逐条显示 ✓ 绿 / ✗ 红 + actual 值。规则下方「脚本测试 n / m」分组
  （有 scriptTests 时才显示）：✓/✗ + 名称，失败附 error 文本。

### 工作区（前端）

- `WorkspaceState`（state.ts，**不**进 localStorage 编辑器存档）：
  `name: string`、`path: string | null`、`entries: SavedRequest[]`（生成类型）、
  `currentId: string | null`（编辑器当前对应的条目 id；null = 未保存的新请求）。
- 动作：`loadWorkspace(file: WorkspaceFile)`（整体载入——含 vars 整体替换，并加载
  第一条请求进编辑器）、`newWorkspace()`（重置为空工作区「未命名工作区」：path=null、
  entries=[]、currentId=null、变量清空、编辑器重置为空 HTTP 请求、清脏）、
  `newRequest(kind)`（按类型重置编辑器、currentId=null）、`loadEntry(id)`、
  `deleteEntry(id)`（删的是当前条目则 currentId=null）、`renameEntry(id, name)`、
  `renameWorkspace(name)`、`snapshotCurrent(): SavedRequest`（编辑器 → 快照；命名规则
  见 state.ts 节：HTTP `METHOD host/path`、WS `WS host/path`、TCP `TCP host:port`，
  截断 40 字符）。
- state.ts 需导出 `applyRequest(next: RequestState)`：整体替换编辑器状态 +
  syncPathParams + scheduleSave + notify（loadSavedRequest / loadEntry 共用）；kind 切换
  由 main.ts 联动（响应区两组 hidden、页签集、urlBar）并调 socket.ts 的「切换先断开」。
- 自动入册：一次发送（含报错、手动停止）结束后，当前编辑器快照自动 upsert 进
  内存工作区；SSE 在拿到响应头（open）时即入册，结束再刷新一次；**WS/TCP 在 connect
  的 open 事件时入册，到达 closed（断开、被拒、失败）再刷新一次**——失败也收，与
  HTTP「无论成败都收」一致；切换 kind / 载入其它条目先断开，那时编辑器 URL 与连接
  对不上，不会误收。新建请求执行一次后即以自动名出现在列表；**派生新条目规则泛化为：
  HTTP 比较 method+URL、WS/TCP 比较 URL——与已入册条目不一致即视为新请求，执行后
  派生新条目，原条目原样保留**；未变的编辑按 id 原地刷新，**改过名的条目保留别名**。
- 保存流程（main.ts）：内存即真相——「保存」把 `WorkspaceState` 整块
  `{name, entries}` + 当前变量表（`vars: getVars()`）写进 .band；有 path 走 `save`，
  无 path 走 `saveAs` 并写回路径（取消仅影响落盘）。保存失败在响应区错误条显示；
  成功（save/saveAs 两条）清脏。`saveWorkspace` 返回 boolean 供关闭/新建守卫判断
  「真的保存成功」（saveAs 对话框取消 = false）。
- 未保存守卫（components/closeConfirm.ts 通用三键确认框，单例）：脏时弹
  「保存 / 继续不保存 / 取消当前操作」。四个触发点——窗口 X（Go 否决关闭并发
  `workspace:closeRequested` 事件，直接弹框，文案「保存 / 不保存 / 取消」）、
  新建工作区（「保存 / 取消保存 / 取消新建」）、打开 .band、拖入 .band
  （后三个走 main.ts `guardUnsaved`，**文件选定/读取成功后才弹框**；保存成功才继续，
  saveAs 取消则留在应用）。Esc / 点遮罩 = 取消；保存中三键禁用。
- UI（components/workspaceSidebar.ts）：index.html 结构改为
  `<main id="app">` 两列网格——左 `<aside id="workspace-sidebar">`，原全部内容包进
  `<div id="main-area">`（**原有 id 一律不动**，组件无感）。
  侧栏内容：工作区名（双击行内重命名，title 显示文件路径）；按钮行「打开 / 保存 / 另存 / 新建」
  （两字一组，title 给全名；「新建」= 新建工作区，脏时先过未保存守卫）；
  「＋ 新建请求」为下拉菜单（HTTP 请求 / WebSocket 连接 / TCP 连接，选中调
  `newRequest(kind)`）；可折叠「变量」区（kvTable 编辑全局 vars，无启用/描述列；
  脚本 setVars 写回时表格随订阅重建）；请求列表（徽章按 kind：HTTP = 方法徽章按方法着色（现状）、
  WS = 绿色 `WS` 徽章、TCP = 紫色 `TCP` 徽章 + 名称 + 行尾 × 删除，
  × 需 window.confirm 确认；双击名称行内重命名）；点击行加载；当前条目高亮；
  空工作区显示引导文案。
- 拖入打开：`.band` 文件拖到**窗口任意位置**即可打开。机制是 mygo 原生文件
  拖放而非 webview 的 File API：`main.go` 在窗口上注册 `Window.OnFileDrop`
  监听器激活路径投递（至少要有一个 Go 侧监听器，原生层才会接受未被页面消费的
  文件拖放），前端经 mygo-runtime 的 `onFileDrop` 拿到**绝对路径**后调
  `WorkspaceService.openPath` 读盘。侧栏保留 dragover 高亮虚线框（只作视觉
  反馈，不再消费 drop 事件）；非 `.band` 文件在响应区错误条提示；
  一次拖多个文件取第一个 `.band`。拖入打开带真实路径，「保存」直接写回原文件。

### 约定

- 原生 TS + strict（`noUncheckedIndexedAccess`），不引入任何 npm 依赖
  （唯一例外：`monaco-editor`，预/后执行脚本编辑器，用户明确批准）。
- 中文 UI 文案；样式按 ApiPost 截图：浅底、圆角卡片、蓝色主色 `#3b82f6`。
- 生成文件 `src/mygo.ts` 只能通过 `bun run generate` 更新。

## 命令

```bash
go build ./... && go vet ./...   # 根目录
bun run generate                 # 重新生成前端绑定
bun run typecheck                # = frontend tsc --noEmit
bun run dev                      # 开发（热重载）
bun run sse:demo                 # 本地 SSE 测试服务器（http://localhost:8123/sse）
bun run socket:demo              # 本地 WS/TCP 回显（ws://localhost:8124，tcp 127.0.0.1:8125）
```
