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

通用 API：`nd.vars`（get/set/unset/has/toObject）、`nd.test(name, fn)`、`nd.assert(cond, msg)`、
`nd.crypto`（md5/sha1/sha256/sha512、hmac、base64(+url)、hex、aesEncrypt/aesDecrypt、randomBytes/randomHex）、
`nd.time`（now/unix/format/parse/offset/zone）、`console.log/warn/error`（进「控制台」页签或连接日志）。
脚本编辑器页顶部的「插入范例」按钮内置了本文的大部分范例。

> 注意：`nd.vars` 是方法式 API——读写变量一律用 `nd.vars.get("name")` / `nd.vars.set("name", value)`，
> `nd.vars.name` 这样的属性直读永远是 `undefined`（`get` 对不存在的变量返回 `null`）。

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
