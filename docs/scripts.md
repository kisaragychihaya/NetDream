# 脚本范例

NetDream 的脚本在 Go 后端经 goja（JavaScript 引擎）执行，单次 5 秒超时，页面关闭/连接断开自动取消。
本文是可粘贴的范例集；API 的权威契约见 [architecture.md](architecture.md) 的「IPC 契约」与「Socket 脚本」节。

## 钩子一览

| 钩子 | 适用 kind | 可写 | 失败语义 |
|---|---|---|---|
| 预执行脚本 | HTTP | `nd.request`（method/url/headers/query/body）、`nd.vars` | 不发送请求 |
| 后执行脚本 | HTTP（SSE 在流正常结束后） | `nd.vars` | 响应保留，只记错误 |
| 预连接脚本 | WS / Socket.IO / TCP | `nd.request`（url/headers/query）、`nd.vars` | 不拨号 |
| 发送脚本 | WS / Socket.IO / TCP | `nd.message.payload`、`nd.vars` | 该条消息不发送 |
| 接收脚本 | WS / Socket.IO / TCP | `nd.vars` | 只记错误，消息不受影响 |

通用 API：`nd.vars`（get/set/unset/has/toObject）、`nd.input(name, prompt?)`（弹输入框，
见下节）、`nd.test(name, fn)`、`nd.assert(cond, msg)`、
`nd.crypto`（md5/sha1/sha256/sha512、hmac、base64(+url)、hex、aesEncrypt/aesDecrypt、randomBytes/randomHex）、
`nd.time`（now/unix/format/parse/offset/zone）、`console.log/warn/error`（进「控制台」页签或连接日志）。
脚本编辑器页顶部的「插入范例」按钮内置了本文的大部分范例。

> 注意：`nd.vars` 是方法式 API——读写变量一律用 `nd.vars.get("name")` / `nd.vars.set("name", value)`，
> `nd.vars.name` 这样的属性直读永远是 `undefined`（`get` 对不存在的变量返回 `null`）。

## 弹窗输入（nd.input）

`nd.input("var_name", "提示语")` 阻塞脚本并弹出带提示的输入框；用户输入并点击确认后，
值写入变量 `var_name`（与 `nd.vars.set` 同一份数据）并作为返回值，脚本继续执行。
所有脚本上下文可用：HTTP 预/后执行、连接三钩子、Composer。

- **等待期间不计时**：脚本自己的 5s 超时、composer 整个运行的预算（含被调条目脚本
  里的等待）都暂停，想多久都不会触发超时。
- 用户点「取消」（或 Esc）抛可 `try/catch` 的 Error（「用户取消了输入」），脚本可
  兜底后继续；页面关闭 / 运行终止按「已取消」结束整个运行，还开着的输入框会被
  后端发事件收回（不会留着一个永远等不到回答的框）。
- 多个脚本同时等输入时页面逐个弹框回答；没有页面的环境（纯后端）抛
  「当前没有可交互的页面」。

HTTP 预执行脚本里询问 token（取消就按匿名继续）：

```js
try {
  nd.input("token", "请输入本次要用的访问令牌");
} catch (e) {
  console.warn("没有输入 token，按匿名继续");
}
```

Composer 运行前询问参数（被调条目脚本里的 nd.input 同样暂停整个运行）：

```js
try {
  nd.input("keyword", "要搜索的关键词？");
} catch (e) {
  nd.vars.set("keyword", "默认");
}
const res = nd.composer.run("搜索");
```

## HTTP 预执行脚本

注入签名 token：

```js
const ts = nd.time.now().toString();
nd.request.headers["x-sign"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
nd.request.headers["x-ts"] = ts;
```

按环境改 host（变量 `apiHost` 在侧栏「变量」区维护）：

```js
if (nd.vars.has("apiHost")) {
  nd.request.url = nd.request.url.replace(/^https?:\/\/[^/]+/, "https://" + nd.vars.get("apiHost"));
}
```

## HTTP 后执行脚本

断言与变量提取：

```js
nd.test("状态码 2xx", () => nd.assert(nd.response.status < 300, "status=" + nd.response.status));
const data = nd.response.json();
if (data && data.token) nd.vars.set("token", data.token);
```

## Socket 预连接脚本

握手注入签名（配合 `bun run socket:demo`，ws://localhost:8124）：

```js
const ts = nd.time.now().toString();
nd.request.headers["x-token"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
nd.request.query["ts"] = ts;
console.log("连接", nd.request.url);
```

## Socket 发送脚本（onSend）

文本包装成 JSON 信封：

```js
nd.message.payload = JSON.stringify({ at: nd.time.now(), data: nd.message.payload });
```

关键词拦截（这条消息不发送）：

```js
if (nd.message.payload.includes("blocked")) throw new Error("被脚本拦截");
```

## Socket 接收脚本（onReceive）

计数与非空断言：

```js
const n = Number(nd.vars.get("msgCount") || "0") + 1;
nd.vars.set("msgCount", String(n));
nd.test("消息非空", () => nd.assert(nd.message.payload.length > 0, "空消息"));
if (nd.message.payload.includes("error")) {
  console.error("第", n, "条消息含 error：", nd.message.payload);
}
```

## Composer 自动化编排

Composer 条目（侧栏「＋ 新建请求 → Composer 自动化」）的整个页面就是一个脚本：
`nd.composer.run()` 同步调用工作区里的 HTTP 条目（含它们的预/后脚本与 `{{var}}` 展开），
`nd.composer.ws|tcp|socketio` 以非阻塞句柄驱动连接。整个运行的超时在顶栏设置
（默认 60 秒，覆盖脚本与所有被调请求）；被调条目自己的预/后脚本默认仍限 5 秒，
可用 `scriptTimeout` 选项按次调整。

```js
nd.composer.run("取首页");                          // 按名字；重名会抛错，改用 id
const res = nd.composer.run("登录", {
  timeout: 10,          // 该次请求超时（秒）
  scriptTimeout: 10,    // 该次被调条目的预/后脚本超时（秒，默认 5）
  vars: { who: "一次" }, // 单次覆盖，不写回变量池
});
// res = { status, statusText, headers, body, timeMS, sizeBytes, truncated,
//         sse, eventCount, postScriptError, json() }
```

串行调用 + 断言 + 变量接力：

```js
const login = nd.composer.run("登录");
nd.test("登录成功", () => nd.assert(login.status === 200, "status=" + login.status));
nd.vars.set("token", login.json().token);
const me = nd.composer.run("当前用户");
nd.test("带 token 能访问", () => nd.assert(me.status === 200));
```

后台连接（pull 模型：open/send 不阻塞，事件经 receive/poll 主动拉取）：

```js
const ws = nd.composer.ws.open("ws://localhost:8124", {
  headers: { "x-token": nd.vars.get("token") || "" },
});
// open 立即返回，连接在后台建立；脚本继续干别的
const login = nd.composer.run("登录");
ws.send(JSON.stringify({ token: login.json().token }));

const evt = ws.receive(5000);   // 最多等 5 秒；超时/关闭/终止返回 null
nd.test("收到回显", () => nd.assert(evt !== null && evt.type === "message"));
ws.poll();                      // 非阻塞取一条，没有就 null
ws.close();
// 事件 = { type:"open"|"message"|"error"|"close", payload, isBinary,
//          event, isAck, code, reason, protocol, at, headers: [{key,value}] }
```

句柄三种 kind 方法一致：`send(payload, {isBinary?, event?, ack?})`（event/ack 仅
Socket.IO）、`receive(timeoutMs?)`、`poll()`、`close()`；另有只读 `id`/`kind`。
连接归本次运行所有：脚本结束或点「终止」时全部自动关闭，阻塞中的 `receive` 会
立刻返回 `null`。连接的 open/error/close 会作为日志行出现在控制台（消息事件不进）。
普通前后脚本与 socket 钩子里没有 `nd.composer`（`typeof nd.composer === "undefined"`），
嵌套拉起会被天然阻止。
