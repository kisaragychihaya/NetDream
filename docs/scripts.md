# 脚本编写指南（预执行 / 后执行）

NetDream 的每个 HTTP 请求可以挂两段 JavaScript：**预执行脚本**（发送前）与
**后执行脚本**（拿到响应后）。脚本在后端经 [goja](https://github.com/dop251/goja)
执行——同步 API（**没有 async/await**）、单次运行 **5 秒超时**、页面关闭自动取消。
脚本随请求一起存入 `.band` 工作区文件。

## 执行顺序

一次发送的完整链路（理解顺序是写好脚本的关键）：

```
预执行脚本（可改写请求 / 读写变量）
  → {{变量}} 替换（URL / Params / Headers / Cookies / URLEncoded / FormData 值 /
                    Raw / Auth 三项；未定义的变量保留 {{var}} 原样）
  → 组装并发送请求（认证、Cookie、Content-Type 等在此落位）
  → 拿到响应
  → 后执行脚本（断言 / 提取字段写变量 / 打日志）
```

推论：**预脚本里 `nd.vars.set` 的变量，当次请求的 `{{var}}` 就能用**。

## API 参考

| 全局 | 说明 |
| --- | --- |
| `nd.vars` | `get(key): string\|null`、`set(key, value)`（值转字符串）、`unset(key)`、`has(key): boolean`、`toObject(): Record<string,string>` |
| `nd.request` | `{ method, url, headers, query, body }`；预脚本可写，后脚本为只读快照 |
| `nd.response` | 仅后脚本：`{ status, statusText, headers, body, timeMS, sizeBytes }` + `json()` |
| `nd.test(name, fn)` | 断言：`fn` 抛异常 = 失败（不中断脚本）；结果进断言面板「脚本测试」 |
| `nd.assert(cond, msg?)` | `cond` 为假即抛 `Error(msg)` |
| `console.log / warn / error(...)` | 参数 JSON 序列化后以空格拼接，进响应区「控制台」页签 |

编辑器（Monaco）已内置上述 API 的补全与悬浮文档，直接输入 `nd.` 即可看到。

### 预脚本改写请求的规则

- `nd.request.method` / `nd.request.url`：赋非空字符串即覆盖。
- `nd.request.headers` / `nd.request.query`：普通 JS 对象，按**大小写不敏感**
  diff 回写——对象里消失的 key（或值为 `null`/`undefined`）被删除；其余 upsert，
  同名 key 以新写法为准。删除示例：`delete nd.request.headers["X-Debug"]`。
- `nd.request.body`：读取时 raw 即原文、urlencoded 为 `k=v&` 序列化、其他类型
  为空串；**改写后**本次请求按 raw 发送（RawType 为空时补 `text`）。

### `nd.response.json()`

对 `body` 做 JSON 解析，返回真正的 JS 对象/数组；解析失败抛异常（整段后脚本
记为「后执行脚本错误」，但**不影响已拿到的响应**）。SSE 流式响应的后脚本
`body` 为空是正常情况。

### 变量系统

- 变量是**应用级**的（不随单个请求保存），在侧栏「变量」区可视化编辑，也持久化
  在本地（`netdream.vars`）。
- 脚本对 `nd.vars` 的修改在发送结束后**全量写回**界面变量区（包括删光变量的
  情况）。
- `{{变量名}}` 替换发生在**预脚本之后**，目标字段：URL、Params/Headers/Cookies/
  URLEncoded 各键值、FormData 值、Raw、Auth 用户名/密码/Token。

## 错误与超时语义

| 情况 | 行为 |
| --- | --- |
| 预脚本语法错误 / 抛异常 / 超时 | **不发送请求**，错误条显示「预执行脚本出错」，已产生的日志仍进控制台 |
| 后脚本抛异常 / 超时 | 响应保留，记「后执行脚本错误」（控制台可见） |
| `nd.test` 的 fn 抛异常 | 该条测试失败，脚本继续执行 |
| 运行超过 5 秒（含 `nd.test` 内的死循环） | 强制中断，按脚本错误处理 |

## 范例

### 预执行：注入追踪头与时间戳变量

```js
nd.vars.set("ts", String(Date.now()));
nd.request.headers["X-Trace-Id"] = "trace-" + Date.now();
```

### 预执行：用变量拼认证头

```js
var token = nd.vars.get("token");
if (token) {
  nd.request.headers["Authorization"] = "Bearer " + token;
}
```

### 后执行：基础断言

```js
nd.test("状态码为 200", function () {
  nd.assert(nd.response.status === 200, "实际 " + nd.response.status);
});
nd.test("耗时小于 500ms", function () {
  nd.assert(nd.response.timeMS < 500, "实际 " + nd.response.timeMS + "ms");
});
```

### 后执行：从 `/v1/models` 筛出所有 deepseek 模型

筛选结果写进变量，后续接口用 `{{deepseek_model}}` 引用（例如聊天接口的
raw body：`{"model": "{{deepseek_model}}", "messages": [...]}`）：

```js
var list = nd.response.json();
var ids = (list.data || []).filter(function (m) {
  return m.id && m.id.toLowerCase().indexOf("deepseek") !== -1;
}).map(function (m) { return m.id; });

nd.test("存在 deepseek 模型", function () {
  nd.assert(ids.length > 0, "未找到 deepseek 模型");
});

nd.vars.set("deepseek_model", ids.length > 0 ? ids[0] : "");
console.log("deepseek 模型(" + ids.length + "):", ids.join(", "));
```

### 组合：登录取 token，后续接口自动带认证

登录接口的后执行脚本：

```js
var data = nd.response.json();
nd.test("登录成功", function () {
  nd.assert(data && data.token, "响应里没有 token");
});
nd.vars.set("token", data.token || "");
console.log("token 已写入变量");
```

业务接口的预执行脚本（或直接在认证页选 Bearer 填 `{{token}}`，效果相同）：

```js
var token = nd.vars.get("token");
nd.assert(token, "请先调用登录接口获取 token");
nd.request.headers["Authorization"] = "Bearer " + token;
```

## 注意事项

- goja 支持 ES5.1 与大部分 ES6 语法（`const/let`、箭头函数、模板字符串可用），
  但**不支持 async/await**，`nd` 全部 API 均为同步返回。
- 沙箱里没有 `require`、`process`、`setTimeout`、网络与文件系统能力——脚本就是
  纯计算 + `nd` API。
- 变量名含空格时 `{{ name }}` 也能匹配（替换前会 Trim）。
