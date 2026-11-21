# 脚本编写指南（预执行 / 后执行）

NetDream 的每个 HTTP 请求可以挂两段 JavaScript：**预执行脚本**（发送前）与
**后执行脚本**（拿到响应后）。脚本在后端经 [goja](https://github.com/dop251/goja)
执行——同步 API（**没有 async/await**）、单次运行 **5 秒超时**、页面关闭自动取消。
脚本随请求一起存入 `.band` 工作区文件。

5 秒是**整段脚本**的墙钟上限，包含纯 Go 里跑的那些调用：`nd.crypto.pbkdf2`
（最高 10,000,000 轮，能自己跑满好几秒）在循环里定期检查同一把期限，
到点会像死循环一样停下，报「预执行脚本超时」，而不是把界面卡在那儿。

## 信任边界（打开别人的 .band 时）

脚本是本地的纯计算，但它读得到你的数据：预脚本能读 `nd.vars`（常放 token、
密码）、请求头与请求体，能改写 URL 与请求内容，之后由后端附上 Basic/Bearer 认证
发出去。也就是说，一份别人给的 `.band` 里塞一段预脚本，就能把变量里的凭据送到
任意地址——不需要任何越权 API。

因此打开/拖入的 `.band` 只要任一条目带预执行或后执行脚本，**第一次执行带脚本的
请求前会先弹确认框**（「信任并运行 / 不运行」，默认焦点在「不运行」），未确认
之前请求不会发出：

- 确认是**整份工作区、本次载入一次性**的：换条目不会重新问，也不会被绕过；
- 确认框打开期间拖入/打开了另一份工作区（或换了条目），这次确认就作废——它不会
  把「信任」按到别的工作区上，需要重新点发送、重新确认；
- 重新打开这个 `.band`、拖入另一个、或新建工作区都会重新判断——保存后再打开
  依然要再确认一次；
- 没有脚本的工作区、以及新建/本地恢复的会话（脚本是你自己写的）不会多问一句。

判断「这文件值不值得信」只能由你来做：确认键等于同意其中的脚本可以读走变量与
请求内容并对外发送。不确定来源时选「不运行」，先在脚本页读一遍再决定。

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
| `nd.crypto` | 哈希 / HMAC / AES / DES / RSA / PBKDF2 / JWT / 编码 / 随机数（见下文） |
| `nd.time` | 毫秒/秒时间戳、token 或 Go 布局的格式化与严格解析、本地时区偏移（见下文） |
| `console.log / warn / error(...)` | 参数 JSON 序列化后以空格拼接，进响应区「控制台」页签 |

编辑器（Monaco）已内置上述 API 的补全与悬浮文档，直接输入 `nd.` 即可看到。

**保留上限**：控制台日志与 `nd.test` 结果各保留前 1000 条，单条文本上限 64KiB
（超长按字符边界截断）。超出的部分直接丢弃，并在控制台末尾追加一条 warn 说明丢了
多少条——用 `for (let i = 0; i < 1e6; i++) console.log(i)` 打日志不会把内存写满。
限的是后端保留量，不是脚本自己的 JS 堆。

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

实现上只有这一次 `JSON.parse`：后端不再先用 Go 解析一遍 body，返回的就是脚本
沙箱里的 JS 值（对象方法、数组都在）。解析失败的异常消息以
`nd.response.json(): ` 开头，脚本可以自己 `try/catch` 处理（后脚本里其它未捕获的
异常仍按「后执行脚本错误」展示）。

### `nd.crypto`（哈希 / HMAC / 加密 / 签名 / 编码）

> **⚠️ 不要用 `Uint8Array.prototype.toBase64/toHex` 处理中文**：沙箱内置的这些
> 方法按 UTF-16 码元逐字节处理，含中文或 ≥0x80 的字节会被截断。字节级往返请用
> `nd.crypto.bytesToBase64` / `bytesFromBase64` / `bytesToHex` / `bytesFromHex`
> （真正的 `Uint8Array`），或直接用下面的字符串 API（按 UTF-8 处理）。

所有字符串参数与返回值按 UTF-8 取字节；密钥、IV、签名、密文等二进制数据用
**带前缀的字符串**表达：

| 写法 | 含义 |
| --- | --- |
| 无前缀 | 标准 base64（兼容旧脚本） |
| `hex:` | 十六进制，如 `hex:00112233` |
| `b64:` | 标准 base64（显式写法） |
| `raw:` | 字面 UTF-8 字节，如 `raw:0123456789abcdef` |

参数个数/类型错误、非法的 encoding/mode/algo、解码失败都会抛异常（中文消息），
可用 `try/catch` 接住。

| 调用 | 说明 |
| --- | --- |
| `md5(data, encoding?)`、`sha1`、`sha224`、`sha256`、`sha384`、`sha512` | `encoding` 默认 `'hex'`，可选 `'base64'` / `'base64url'`（无 padding） |
| `hmac(algo, key, data, encoding?)` | **key 在前、data 在后**；`algo`：`'md5'`/`'sha1'`/`'sha224'`/`'sha256'`/`'sha384'`/`'sha512'`（大小写不敏感） |
| `base64Encode(s)` / `base64Decode(s)` | 标准 base64；**UTF-8 安全**，中文直接传入即可 |
| `base64UrlEncode(s)` / `base64UrlDecode(s)` | URL 安全 base64：编码不带 padding，解码两种写法都接受 |
| `hexEncode(s)` / `hexDecode(s)` | 同样 UTF-8 安全；`hexDecode` 遇非法字符抛异常 |
| `urlEncode(s)` / `urlDecode(s)` | RFC 3986 百分号编码：unreserved 字符透传、空格→`%20`、`/`→`%2F`、hex 大写；解码把 `+` 当字面加号（不是空格） |
| `bytesToBase64(bytes)` | `bytes` 为 `Uint8Array` 或 `ArrayBuffer`，返回标准 base64 |
| `bytesFromBase64(text)` | 解码标准 base64，返回真正的 `Uint8Array`（可索引、`instanceof Uint8Array`） |
| `bytesToHex(bytes)` | `bytes` 为 `Uint8Array` 或 `ArrayBuffer`，返回小写 hex 字符串 |
| `bytesFromHex(text)` | 解码 hex 字符串，返回真正的 `Uint8Array`；非法字符抛异常 |
| `aesEncrypt(data, key, iv?, mode?)` / `aesDecrypt(data, key, iv?, mode?)` | `mode`：`'cbc'`（默认）/`'ecb'`/`'gcm'`；密文输出/输入均为 base64 |
| `desEncrypt(data, key, iv?, mode?)` / `desDecrypt(data, key, iv?, mode?)` | key 8 字节=DES、16/24 字节=3DES；仅支持 `'cbc'` / `'ecb'` |
| `rsaSign(data, privateKey, hash?)` / `rsaVerify(data, signature, publicKey, hash?)` | PKCS#1 v1.5；`hash` 默认 `'sha256'`，可选 sha1/sha224/sha256/sha384/sha512（**拒绝 md5**）；`signature` 为 base64，verify 返回 boolean |
| `rsaPssSign(data, privateKey, hash?)` / `rsaPssVerify(data, signature, publicKey, hash?)` | PSS 签名（SaltLength = hash 长度），参数与 PKCS#1 v1.5 版本一致 |
| `rsaEncrypt(data, publicKey)` / `rsaDecrypt(data, privateKey)` | PKCS#1 v1.5 公钥加密/私钥解密，密文 base64 |
| `pbkdf2(password, salt, iterations, keyLen, hash?)` | RFC 2898；`hash` 默认 `'sha256'`；`iterations` 1–10,000,000，`keyLen` 1–4096；返回 base64 |
| `jwtSign(payload, secretOrKey, algo?)` / `jwtVerify(token, secretOrKey, algo?)` | `algo`：HS256/HS384/HS512/RS256/RS384/RS512，默认 HS256；HS* 的 secret 是字符串，RS* 是 PEM 私钥/公钥 |
| `randomBytes(n)` / `randomHex(n)` | `crypto/rand` 生成 `n` 字节（1–4096），分别返回 base64 / hex |

对称加密细节：

- `key` 解码后：AES 必须 16 / 24 / 32 字节（AES-128/192/256）；DES 8 字节；
  3DES 16 字节（按 2-key 3DES 展开为 K1‖K2‖K1）或 24 字节。
- `'cbc'`：PKCS#7 填充，`iv` 必须等于块大小（AES 16 字节 / DES 8 字节）。
- `'ecb'`：`iv` 必须省略或为空串；PKCS#7 填充按块大小（AES 16 / DES 8）。
- `'gcm'`（仅 AES）：输出为 `base64(nonce ‖ 密文 ‖ tag)`。`iv` 省略时自动生成
  12 字节随机 nonce；解密时自动从密文前 12 字节切分 nonce。显式传 `iv` 时必须是
  12 字节（兼容旧调用）；解密时显式 nonce 同时接受带 nonce 前缀的新密文与
  `密文‖tag` 的旧格式密文。
- CBC / ECB 解密会校验 PKCS#7 填充，key 或 iv 不对时报「解密失败」而不是返回乱码。

RSA 细节：

- 私钥 PEM 支持 PKCS#1（`RSA PRIVATE KEY`）与 PKCS#8（`PRIVATE KEY`），且必须是
  RSA；公钥支持 PKIX（`PUBLIC KEY`）与 PKCS#1（`RSA PUBLIC KEY`）。
- 签名/验签使用 PKCS#1 v1.5（`rsaSign`/`rsaVerify`）或 PSS
  （`rsaPssSign`/`rsaPssVerify`），签名值统一 base64 进出。

JWT 细节：

- header 固定为 `{"alg": "<algo>", "typ": "JWT"}`，两段均用无 padding 的
  URL 安全 base64。
- `jwtVerify` 解析 `header.alg`；传了期望 `algo` 时先比对，不一致直接抛异常
  （防止 alg 降级）。验签失败抛异常，成功返回解析后的 payload JS 对象（不校验
  exp/nbf 等时间声明）。

```js
nd.vars.set("sig", nd.crypto.sha256("app_id=1&ts=" + Date.now()));
nd.vars.set("token", nd.crypto.base64Encode("中文 token 也能正确编码"));
nd.request.headers["X-Sign"] = nd.crypto.hmac("sha256", "my-secret", "app_id=1&ts=1");
```

#### 从旧版迁移（破坏性变更）

1. **`hmac` 参数顺序反转**：旧的 `hmac(algo, data, key, encoding?)` 改为
   `hmac(algo, key, data, encoding?)`。迁移时把第 2、3 个参数对调即可；同时
   `algo` 新增了 `sha224` / `sha384`。
2. **AES-GCM 输出格式变更**：旧实现输出 `密文‖tag`，且必须显式传 12 字节
   `iv`；新实现输出 `nonce‖密文‖tag`，`iv` 可省略。旧脚本若要解历史密文，继续
   显式传原来的 nonce 即可（`aesDecrypt(oldCt, key, oldNonce, "gcm")`）；若把旧
   密文当新格式（省略 iv）解，会因把密文前 12 字节当成 nonce 而校验失败。
3. key/iv 无前缀时仍按 base64 解码，旧脚本无需改动；需要更直观的写法时可用
   `hex:` / `raw:` 前缀。

### `nd.time`（时间）

`Date` 在沙箱里照样能用，但它的日期字符串解析和 `toLocale*` 格式化都有坑
（见文末「注意事项」）。要和后端或其他系统对齐时用 `nd.time`：参数类型不对、
时区名不认识会抛异常，可用 `try/catch` 接住。

| 调用 | 返回 | 说明 |
| --- | --- | --- |
| `now()` | `number` | 当前毫秒时间戳（等价 `Date.now()`） |
| `unix()` | `number` | 当前秒时间戳（等价 `Math.floor(Date.now()/1000)`） |
| `format(ms, layout, timeZone?)` | `string` | 按 `layout` 格式化 `ms`（毫秒时间戳）；`timeZone` 是 IANA 名，省略或空串按本地时区 |
| `parse(s, layout, timeZone?)` | `number \| null` | 按 `layout` **严格解析**成毫秒时间戳；字符串不符合布局或日期不存在时返回 `null`，**不抛异常** |
| `offset()` | `number` | 本地时区偏移的**分钟数**，符号同 `Date.getTimezoneOffset()`（东八区是 `-480`） |
| `zone()` | `string` | 本地时区名（`time.Local`；Windows 上通常是 `Local`） |

`layout` 支持两种写法，`format` 与 `parse` 共用：

- **token 布局**（JavaScript 习惯）：含 `YY` 或 `SSS` 时按 token 表翻译。
- **Go 参考布局**：不含上述 token 时原样使用，例如 `2006-01-02`。

| token | Go 布局 | 含义 |
| --- | --- | --- |
| `YYYY` / `YY` | `2006` / `06` | 四位 / 两位年 |
| `MM` / `M` | `01` / `1` | 月（补零 / 不补零） |
| `DD` / `D` | `02` / `2` | 日 |
| `HH` / `H` | `15` / `3` | 时（`HH` 为 24 小时制；`H` 为 12 小时制且不补零，容易误用，建议直接写 Go 布局） |
| `mm` / `m` | `04` / `4` | 分 |
| `ss` / `s` | `05` / `5` | 秒 |
| `SSS` | `000` | 毫秒（固定 3 位） |

常用 Go 布局写法：`2006` 年、`01` 月、`02` 日、`15` 时（24 小时制；`03` 是
12 小时制）、`04`/`05` 分/秒、`.000` 毫秒（`.999` 去掉末尾 0）、`MST`
时区缩写、`-07:00` 偏移（`Z07:00` 在 UTC 输出 `Z`）、`Mon`/`Jan`/`Monday`
星期/月份名。

`timeZone` 接受 `time.LoadLocation` 认识的 IANA 名，如 `Asia/Shanghai`、`UTC`、
`America/New_York`；写错会抛「不支持的 timeZone」。IANA 时区数据库已随可执行
文件内嵌（`time/tzdata`），Windows 上无需额外的 zoneinfo 文件。

```js
// 解析固定格式的服务器时间（严格：不符合布局就是 null，不会瞎猜）
var ms = nd.time.parse("2025-01-01 08:00:00.123", "YYYY-MM-DD HH:mm:ss.SSS", "Asia/Shanghai");
nd.assert(ms !== null, "响应里的时间格式不符合布局");

// 反过来格式化：一天之后、同样的时区
nd.vars.set("next_day", nd.time.format(ms + 86400000, "YYYY-MM-DD", "Asia/Shanghai")); // "2025-01-02"
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
| 5 秒内取消（关闭页面/换工作区）或超时，脚本正卡在 `nd.crypto.pbkdf2` 里 | 该调用自己检查期限后停下，报同样的「超时」/「已取消」，不会等到算完 |

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
