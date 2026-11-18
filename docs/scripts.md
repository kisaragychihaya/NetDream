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
| `nd.crypto` | 哈希 / HMAC / AES / 编码 / 随机数，全部**字符串进出**（见下文） |
| `nd.time` | 毫秒/秒时间戳、Go 布局的格式化与严格解析、本地时区偏移（见下文） |
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

### `nd.crypto`（哈希 / HMAC / 加密 / 编码）

所有参数与返回值都是**字符串**（变量也只存字符串），字节一律用 base64 或 hex
表达。参数个数/类型错误、非法的 encoding/mode/algo、解码失败都会抛异常，
可用 `try/catch` 接住。

| 调用 | 说明 |
| --- | --- |
| `md5(s, encoding?)`、`sha1`、`sha256`、`sha512` | `s` 按 UTF-8 取字节；`encoding` 默认 `'hex'`，可选 `'base64'` / `'base64url'`（无 padding） |
| `hmac(algo, data, key, encoding?)` | `algo`：`'md5'`/`'sha1'`/`'sha256'`/`'sha512'`（大小写不敏感）；`key`、`data` 均按 UTF-8，`encoding` 同上 |
| `base64Encode(s)` / `base64Decode(s)` | 标准 base64；**UTF-8 安全**，中文直接传入即可 |
| `base64UrlEncode(s)` / `base64UrlDecode(s)` | URL 安全 base64：编码不带 padding，解码两种写法都接受 |
| `hexEncode(s)` / `hexDecode(s)` | 同样 UTF-8 安全；`hexDecode` 遇非法字符抛异常 |
| `aesEncrypt(data, key, iv, mode?)` / `aesDecrypt(data, key, iv, mode?)` | `key` 与 `iv` 是 **base64 字符串**；密文输出 base64，解密输入 base64 |
| `randomBytes(n)` / `randomHex(n)` | `crypto/rand` 生成 `n` 字节（1–4096），分别返回 base64 / hex |

AES 细节：

- `key` 解码后必须是 16 / 24 / 32 字节（AES-128/192/256），否则抛异常。
- `mode` 默认 `'cbc'`：PKCS#7 填充，`iv` 必须 16 字节；`'gcm'`：`iv` 是 nonce，
  必须 12 字节，密文自带认证标签，解密校验失败即抛异常。
- CBC 解密会校验 PKCS#7 填充，key 或 iv 不对时报「解密失败」而不是返回乱码。

```js
nd.vars.set("sig", nd.crypto.sha256("app_id=1&ts=" + Date.now()));
nd.vars.set("token", nd.crypto.base64Encode("中文 token 也能正确编码"));
```

> 沙箱内置了 `Uint8Array.prototype.toBase64/toHex/fromBase64/fromHex`（以及
> `Uint8Array.fromBase64/fromHex`），纯 ASCII 场景可以直接用；但它按 UTF-16
> 码元逐字节处理，中文会被截断——**UTF-8 场景请走 `nd.crypto`**。

### `nd.time`（时间）

`Date` 在沙箱里照样能用，但它的日期字符串解析和 `toLocale*` 格式化都有坑
（见文末「注意事项」）。要和后端或其他系统对齐时用 `nd.time`：参数类型不对、
时区名不认识会抛异常，可用 `try/catch` 接住。

| 调用 | 返回 | 说明 |
| --- | --- | --- |
| `now()` | `number` | 当前毫秒时间戳（等价 `Date.now()`） |
| `unix()` | `number` | 当前秒时间戳（等价 `Math.floor(Date.now()/1000)`） |
| `format(ms, layout, timeZone?)` | `string` | 按 **Go 布局**格式化 `ms`（毫秒时间戳）；`timeZone` 是 IANA 名，省略或空串按本地时区 |
| `parse(s, layout, timeZone?)` | `number \| null` | 按 Go 布局**严格解析**成毫秒时间戳；字符串不符合布局或日期不存在时返回 `null`，**不抛异常** |
| `offset()` | `number` | 本地时区偏移的**分钟数**，符号同 `Date.getTimezoneOffset()`（东八区是 `-480`） |
| `zone()` | `string` | 本地时区名（`time.Local`；Windows 上通常是 `Local`） |

`layout` 用 Go 参考时刻的写法，常用的几个：

| 写法 | 含义 |
| --- | --- |
| `2006` | 年 |
| `01` | 月（`1` 不补零） |
| `02` | 日 |
| `15` | 时（24 小时制；`03` 是 12 小时制） |
| `04` / `05` | 分 / 秒 |
| `.000` | 毫秒（`.999` 去掉末尾 0） |
| `MST` | 时区缩写 |
| `-07:00` | 偏移（`Z07:00` 在 UTC 输出 `Z`） |
| `Mon` / `Jan` / `Monday` | 星期 / 月份名 |

`timeZone` 接受 `time.LoadLocation` 认识的 IANA 名，如 `Asia/Shanghai`、
`UTC`、`America/New_York`；写错会抛「不支持的 timeZone」。

```js
// 解析固定格式的服务器时间（严格：不符合布局就是 null，不会瞎猜）
var ms = nd.time.parse("2025-01-01 08:00:00", "2006-01-02 15:04:05", "Asia/Shanghai");
nd.assert(ms !== null, "响应里的时间格式不符合布局");

// 反过来格式化：一天之后、同样的时区
nd.vars.set("next_day", nd.time.format(ms + 86400000, "2006-01-02", "Asia/Shanghai")); // "2025-01-02"
nd.request.headers["X-Timestamp"] = String(nd.time.unix());
```

### 变量系统

- 变量是**应用级**的（不随单个请求保存），在侧栏「变量」区可视化编辑；
  **随工作区保存**——写入 `.band` zip 内的 `engine/vars.json`，打开工作区时整体载入。
- 变量**不写 localStorage**：应用关闭后内存中的变量即清空（避免 token/apikey
  等凭据在本地残留）；工作区有未保存改动（含变量变更）时，关闭窗口、新建工作区、
  打开或拖入其他 .band 都会先弹「保存 / 不保存 / 取消」确认。
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
- `Date` 的 `toLocaleString` / `toLocaleDateString` / `toLocaleTimeString`（以及
  带参数的同族方法）在沙箱里**忽略传入的参数**，固定输出美式 `月/日/年` 格式
  （`new Date(0).toLocaleString("zh-CN")` 得到 `01/01/1970, 08:00:00`）。要中文
  或自定义格式，请用 `nd.time.format`。
- `Date` 解析日期字符串时**时区是隐式的**，同一个字符串可能差一整天：
  - `"2025-01-01"` 这种纯日期按 **UTC** 解析，东八区读出来是当天 08:00；
  - 带 `T` 但不带偏移的（`"2025-01-01T00:00:00"`）按**本地**解析，是当天 00:00；
  - `CST` 这类缩写只有非 ISO 写法能解析（`"Wed, 01 Jan 2025 00:00:00 CST"`），
    且被当成**美国中部时间**（UTC-6）——而 ISO 串后直接加缩写
    （`"2025-01-01T00:00:00 CST"`）在沙箱里直接解析失败（`NaN`）。

  要确定语义就用 `nd.time.parse(s, layout, timeZone)` 显式指定时区。
