/**
 * 脚本编辑器的「插入范例」：每个脚本钩子几段可直接运行的短脚本，
 * 末尾点一个按钮就能插进编辑器。
 *
 * 范例正文（名字与代码里的注释）按界面语言分三份，直接写在这里而不进
 * locales JSON：它们是多行代码，不是一句界面文案，放进词典只会让两处都难读。
 * 代码逻辑三语完全一致，只有注释与测试名跟着语言走——脚本跑起来的行为不该
 * 因为界面语言而变。界面语言用 i18n 的 getLang() 取，切语言时重画菜单。
 *
 * API 契约见 docs/architecture.md「IPC 契约」与「Socket 脚本」两节，
 * 可粘贴的完整范例集见 docs/scripts.md。
 */

import { getLang, onLangChange, t, type Lang } from "./i18n";

/** 六个脚本钩子：HTTP 的预执行/后执行，连接类 kind 的预连接/发送/接收，Composer 编排。 */
export type ScriptExampleContext =
  | "http-pre"
  | "http-post"
  | "socket-pre"
  | "socket-send"
  | "socket-receive"
  | "composer";

/** 一份三语文案。 */
type Text = Record<Lang, string>;

interface ScriptExample {
  /** 菜单里显示的名字。 */
  name: Text;
  /** 插入编辑器的代码，注释跟随界面语言。 */
  code: Text;
}

const EXAMPLES: Record<ScriptExampleContext, readonly ScriptExample[]> = {
  "http-pre": [
    {
      name: {
        "zh-CN": "注入 Bearer token",
        "en-US": "Inject a Bearer token",
        ja: "Bearer トークンを注入",
      },
      code: {
        "zh-CN": `// 有 token 变量就用 Bearer；没有就现场用 secret 给时间戳签名兜底。
const token = nd.vars.get("token");
if (token) {
  nd.request.headers["authorization"] = "Bearer " + token;
} else {
  const ts = nd.time.now().toString();
  nd.request.headers["x-ts"] = ts;
  nd.request.headers["x-sign"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
}
`,
        "en-US": `// Use the token variable as a Bearer token; without one, sign a timestamp with the secret.
const token = nd.vars.get("token");
if (token) {
  nd.request.headers["authorization"] = "Bearer " + token;
} else {
  const ts = nd.time.now().toString();
  nd.request.headers["x-ts"] = ts;
  nd.request.headers["x-sign"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
}
`,
        ja: `// 変数 token があれば Bearer で送信し、無ければ secret でタイムスタンプに署名して代用します。
const token = nd.vars.get("token");
if (token) {
  nd.request.headers["authorization"] = "Bearer " + token;
} else {
  const ts = nd.time.now().toString();
  nd.request.headers["x-ts"] = ts;
  nd.request.headers["x-sign"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
}
`,
      },
    },
    {
      name: {
        "zh-CN": "按环境改写 host",
        "en-US": "Rewrite the host per environment",
        ja: "環境ごとにホストを書き換え",
      },
      code: {
        "zh-CN": `// 变量 apiHost 存在时只换域名，路径与查询参数保持原样。
const host = nd.vars.get("apiHost");
if (host) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = "https://" + host + path;
  console.log("发往", nd.request.url);
}
`,
        "en-US": `// When the apiHost variable is set, swap the host only and keep path and query as they are.
const host = nd.vars.get("apiHost");
if (host) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = "https://" + host + path;
  console.log("Sending to", nd.request.url);
}
`,
        ja: `// 変数 apiHost があればホストだけ差し替えます（パスとクエリはそのまま）。
const host = nd.vars.get("apiHost");
if (host) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = "https://" + host + path;
  console.log("送信先", nd.request.url);
}
`,
      },
    },
    {
      name: {
        "zh-CN": "请求体注入时间戳",
        "en-US": "Add a timestamp to the body",
        ja: "ボディにタイムスタンプを追加",
      },
      code: {
        "zh-CN": `// 请求体是 JSON 对象时补上 ts 字段；不是就跳过，不会毁掉表单/文本请求。
const body = nd.request.body.trim();
if (body.startsWith("{")) {
  try {
    const data = JSON.parse(body);
    data.ts = nd.time.now();
    nd.request.body = JSON.stringify(data);
    nd.request.headers["content-type"] = "application/json";
  } catch (e) {
    console.warn("请求体解析失败，跳过时间戳注入");
  }
} else {
  console.warn("请求体不是 JSON，跳过时间戳注入");
}
`,
        "en-US": `// Add a ts field when the body is a JSON object; anything else is left untouched.
const body = nd.request.body.trim();
if (body.startsWith("{")) {
  try {
    const data = JSON.parse(body);
    data.ts = nd.time.now();
    nd.request.body = JSON.stringify(data);
    nd.request.headers["content-type"] = "application/json";
  } catch (e) {
    console.warn("Body is not parseable, skipping the timestamp");
  }
} else {
  console.warn("Body is not JSON, skipping the timestamp");
}
`,
        ja: `// ボディが JSON オブジェクトなら ts を足します。そうでなければ何もしません。
const body = nd.request.body.trim();
if (body.startsWith("{")) {
  try {
    const data = JSON.parse(body);
    data.ts = nd.time.now();
    nd.request.body = JSON.stringify(data);
    nd.request.headers["content-type"] = "application/json";
  } catch (e) {
    console.warn("ボディを解析できないため、タイムスタンプは追加しません");
  }
} else {
  console.warn("ボディが JSON ではないため、タイムスタンプは追加しません");
}
`,
      },
    },
    {
      name: {
        "zh-CN": "弹窗请用户输入 token",
        "en-US": "Ask for a token in a dialog",
        ja: "ダイアログでトークンを尋ねる",
      },
      code: {
        "zh-CN": `// 弹输入框请用户填 token：确认后写入变量 token 并继续，之后可用 {{token}} 或
// nd.vars.get("token") 引用；用户点「取消」会抛异常，catch 后按匿名继续。
// 等待输入期间脚本不计时，想多久都不会触发超时。
try {
  nd.input("token", "请输入本次要用的访问令牌");
} catch (e) {
  console.warn("没有输入 token，按匿名继续");
}
`,
        "en-US": `// Ask for a token in a dialog: confirming stores it in the token variable, usable later as
// {{token}} or nd.vars.get("token"); cancelling throws, so catch it and continue anonymously.
// The script is not timed while it waits, so thinking time never trips the timeout.
try {
  nd.input("token", "Paste the access token for this run");
} catch (e) {
  console.warn("No token entered, continuing anonymously");
}
`,
        ja: `// 入力ダイアログでトークンを尋ねます：確認すると変数 token に書き込まれ、以後
// {{token}} や nd.vars.get("token") で使えます。「キャンセル」は例外になるので
// catch して匿名で続行します。待機中はスクリプトの計時は止まります。
try {
  nd.input("token", "今回使うアクセストークンを入力してください");
} catch (e) {
  console.warn("トークン未入力のため匿名で続行します");
}
`,
      },
    },
  ],

  "http-post": [
    {
      name: {
        "zh-CN": "断言状态码与字段",
        "en-US": "Assert the status and a field",
        ja: "ステータスとフィールドを検証",
      },
      code: {
        "zh-CN": `// 断言结果记进「脚本测试」：失败不中断脚本，响应照旧保留。
nd.test("状态码 2xx", () => nd.assert(nd.response.status < 300, "status=" + nd.response.status));
let data = null;
try {
  data = nd.response.json();
} catch (e) {
  console.warn("响应不是 JSON");
}
nd.test("响应含 token", () => nd.assert(Boolean(data && data.token), "缺 token 字段"));
`,
        "en-US": `// Test results land in "Script tests": a failure does not stop the script or drop the response.
nd.test("Status is 2xx", () => nd.assert(nd.response.status < 300, "status=" + nd.response.status));
let data = null;
try {
  data = nd.response.json();
} catch (e) {
  console.warn("Response is not JSON");
}
nd.test("Response has a token", () => nd.assert(Boolean(data && data.token), "missing token"));
`,
        ja: `// 検証結果は「スクリプトテスト」に入ります。失敗してもスクリプトは止まらず、レスポンスも残ります。
nd.test("ステータスが 2xx", () => nd.assert(nd.response.status < 300, "status=" + nd.response.status));
let data = null;
try {
  data = nd.response.json();
} catch (e) {
  console.warn("レスポンスが JSON ではありません");
}
nd.test("token を含む", () => nd.assert(Boolean(data && data.token), "token がありません"));
`,
      },
    },
    {
      name: {
        "zh-CN": "响应字段写入变量",
        "en-US": "Save a response field as a variable",
        ja: "レスポンスの値を変数に保存",
      },
      code: {
        "zh-CN": `// 把响应里的字段写进变量，后续请求可以直接用 {{token}} 引用。
try {
  const data = nd.response.json();
  if (data && data.token) {
    nd.vars.set("token", String(data.token));
    console.log("已写入变量 token");
  }
} catch (e) {
  console.warn("响应不是 JSON，没提取变量");
}
`,
        "en-US": `// Store a response field as a variable; later requests can use {{token}} directly.
try {
  const data = nd.response.json();
  if (data && data.token) {
    nd.vars.set("token", String(data.token));
    console.log("Stored variable token");
  }
} catch (e) {
  console.warn("Response is not JSON, no variable extracted");
}
`,
        ja: `// レスポンスの値を変数に入れます。以後のリクエストで {{token}} として参照できます。
try {
  const data = nd.response.json();
  if (data && data.token) {
    nd.vars.set("token", String(data.token));
    console.log("変数 token を保存しました");
  }
} catch (e) {
  console.warn("レスポンスが JSON ではないため、変数は取り出しません");
}
`,
      },
    },
  ],

  "socket-pre": [
    {
      name: {
        "zh-CN": "握手注入签名 token",
        "en-US": "Sign the handshake with a token",
        ja: "ハンドシェイクに署名トークンを付与",
      },
      code: {
        "zh-CN": `// 握手签名：x-token = hmac(secret, ts)，与自带的 socket 演示服务器约定一致。
const ts = nd.time.now().toString();
nd.request.headers["x-ts"] = ts;
nd.request.headers["x-token"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
console.log("连接", nd.request.url);
`,
        "en-US": `// Handshake signature: x-token = hmac(secret, ts), matching the bundled socket demo server.
const ts = nd.time.now().toString();
nd.request.headers["x-ts"] = ts;
nd.request.headers["x-token"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
console.log("Connecting", nd.request.url);
`,
        ja: `// ハンドシェイク署名: x-token = hmac(secret, ts)。同梱のソケットデモサーバーと同じ約束です。
const ts = nd.time.now().toString();
nd.request.headers["x-ts"] = ts;
nd.request.headers["x-token"] = nd.crypto.hmac("sha256", nd.vars.get("secret") || "demo", ts);
console.log("接続", nd.request.url);
`,
      },
    },
    {
      name: {
        "zh-CN": "query 附加时间戳",
        "en-US": "Add a timestamp to the query",
        ja: "クエリにタイムスタンプを付与",
      },
      code: {
        "zh-CN": `// 查询参数带上时间戳，绕开代理/网关对握手请求的缓存。
nd.request.query["ts"] = nd.time.now().toString();
if (!nd.request.query["client"]) nd.request.query["client"] = "netdream";
`,
        "en-US": `// Put a timestamp in the query so proxies and gateways do not cache the handshake.
nd.request.query["ts"] = nd.time.now().toString();
if (!nd.request.query["client"]) nd.request.query["client"] = "netdream";
`,
        ja: `// クエリにタイムスタンプを足して、プロキシのハンドシェイクキャッシュを避けます。
nd.request.query["ts"] = nd.time.now().toString();
if (!nd.request.query["client"]) nd.request.query["client"] = "netdream";
`,
      },
    },
    {
      name: {
        "zh-CN": "按变量改写连接地址",
        "en-US": "Rewrite the URL from a variable",
        ja: "変数で接続先を書き換え",
      },
      code: {
        "zh-CN": `// 变量 localWs（如 ws://localhost:8124）存在时改连本地演示服务器。
const local = nd.vars.get("localWs");
if (local) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = local + path;
  console.log("改写连接地址为", nd.request.url);
}
`,
        "en-US": `// When the localWs variable is set (e.g. ws://localhost:8124), point at the local demo server.
const local = nd.vars.get("localWs");
if (local) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = local + path;
  console.log("URL rewritten to", nd.request.url);
}
`,
        ja: `// 変数 localWs（例: ws://localhost:8124）があればローカルのデモサーバーへ向けます。
const local = nd.vars.get("localWs");
if (local) {
  const path = nd.request.url.replace(/^[a-z]+:\\/\\/[^/]+/i, "");
  nd.request.url = local + path;
  console.log("接続先を書き換え:", nd.request.url);
}
`,
      },
    },
  ],

  "socket-send": [
    {
      name: {
        "zh-CN": "文本包装成 JSON 信封",
        "en-US": "Wrap text in a JSON envelope",
        ja: "テキストを JSON エンベロープで包む",
      },
      code: {
        "zh-CN": `// 文本包成 {at, data} 信封；二进制消息（payload 是 base64）原样发出。
if (!nd.message.isBinary) {
  nd.message.payload = JSON.stringify({ at: nd.time.now(), data: nd.message.payload });
}
`,
        "en-US": `// Wrap text as an {at, data} envelope; binary messages (payload is base64) go out untouched.
if (!nd.message.isBinary) {
  nd.message.payload = JSON.stringify({ at: nd.time.now(), data: nd.message.payload });
}
`,
        ja: `// テキストは {at, data} のエンベロープに包みます。バイナリ（payload は base64）はそのまま送ります。
if (!nd.message.isBinary) {
  nd.message.payload = JSON.stringify({ at: nd.time.now(), data: nd.message.payload });
}
`,
      },
    },
    {
      name: {
        "zh-CN": "关键词拦截",
        "en-US": "Block a keyword",
        ja: "キーワードを遮断",
      },
      code: {
        "zh-CN": `// 命中关键词就抛异常：这一条不发送，失败原因显示在连接日志里。
if (nd.message.payload.includes("blocked")) {
  throw new Error("被发送脚本拦截：消息含 blocked");
}
`,
        "en-US": `// Throwing skips this message: the reason shows up in the connection log.
if (nd.message.payload.includes("blocked")) {
  throw new Error("Blocked by the send script: contains blocked");
}
`,
        ja: `// 例外を投げるとこの 1 通は送信されません。理由は接続ログに出ます。
if (nd.message.payload.includes("blocked")) {
  throw new Error("送信スクリプトが遮断しました: blocked を含みます");
}
`,
      },
    },
    {
      name: {
        "zh-CN": "空消息补心跳包",
        "en-US": "Turn an empty message into a heartbeat",
        ja: "空メッセージをハートビートに",
      },
      code: {
        "zh-CN": `// 空消息当心跳：补一个 {type:"ping"} 信封，服务端据此保活。
if (!nd.message.isBinary && nd.message.payload.trim() === "") {
  nd.message.payload = JSON.stringify({ type: "ping", at: nd.time.now() });
}
`,
        "en-US": `// Treat an empty message as a heartbeat and send a {type:"ping"} envelope instead.
if (!nd.message.isBinary && nd.message.payload.trim() === "") {
  nd.message.payload = JSON.stringify({ type: "ping", at: nd.time.now() });
}
`,
        ja: `// 空のメッセージはハートビート扱いで {type:"ping"} を補います。
if (!nd.message.isBinary && nd.message.payload.trim() === "") {
  nd.message.payload = JSON.stringify({ type: "ping", at: nd.time.now() });
}
`,
      },
    },
  ],

  "socket-receive": [
    {
      name: {
        "zh-CN": "计数 + 非空断言",
        "en-US": "Count messages and assert non-empty",
        ja: "件数の記録と空でないことの検証",
      },
      code: {
        "zh-CN": `// 计数并断言非空：结果进「脚本测试」，消息本身照旧显示，不受脚本影响。
const count = Number(nd.vars.get("msgCount") || "0") + 1;
nd.vars.set("msgCount", String(count));
nd.test("消息非空", () => nd.assert(nd.message.payload.length > 0, "收到空消息"));
console.log("第 " + count + " 条消息");
`,
        "en-US": `// Count and assert: results go to "Script tests"; the message itself is untouched.
const count = Number(nd.vars.get("msgCount") || "0") + 1;
nd.vars.set("msgCount", String(count));
nd.test("Message is not empty", () => nd.assert(nd.message.payload.length > 0, "empty message"));
console.log("Message #" + count);
`,
        ja: `// 件数を数えて空でないことを検証します。結果は「スクリプトテスト」に出て、メッセージ自体には影響しません。
const count = Number(nd.vars.get("msgCount") || "0") + 1;
nd.vars.set("msgCount", String(count));
nd.test("メッセージが空でない", () => nd.assert(nd.message.payload.length > 0, "空のメッセージ"));
console.log(count + " 通目");
`,
      },
    },
    {
      name: {
        "zh-CN": "错误关键词告警",
        "en-US": "Warn on an error keyword",
        ja: "エラーキーワードを警告",
      },
      code: {
        "zh-CN": `// 含 error 的消息打一条告警日志，方便在连接日志里一眼看到。
if (nd.message.payload.toLowerCase().includes("error")) {
  console.warn("消息含 error：", nd.message.payload);
}
`,
        "en-US": `// Log a warning for messages containing "error" so they stand out in the connection log.
if (nd.message.payload.toLowerCase().includes("error")) {
  console.warn("Message contains error:", nd.message.payload);
}
`,
        ja: `// error を含むメッセージは警告ログにして、接続ログで見つけやすくします。
if (nd.message.payload.toLowerCase().includes("error")) {
  console.warn("error を含むメッセージ:", nd.message.payload);
}
`,
      },
    },
    {
      name: {
        "zh-CN": "消息字段存入变量",
        "en-US": "Store a message field as a variable",
        ja: "メッセージの値を変数に保存",
      },
      code: {
        "zh-CN": `// 把 JSON 消息里的 id 存进变量，发送脚本可以用 {{lastId}} 引用。
try {
  const data = JSON.parse(nd.message.payload);
  if (data && data.id !== undefined) nd.vars.set("lastId", String(data.id));
} catch (e) {
  // 非 JSON 消息，忽略
}
`,
        "en-US": `// Keep the id of a JSON message as a variable so the send script can use {{lastId}}.
try {
  const data = JSON.parse(nd.message.payload);
  if (data && data.id !== undefined) nd.vars.set("lastId", String(data.id));
} catch (e) {
  // Not a JSON message, ignore it
}
`,
        ja: `// JSON メッセージの id を変数に入れ、送信スクリプトから {{lastId}} で参照できます。
try {
  const data = JSON.parse(nd.message.payload);
  if (data && data.id !== undefined) nd.vars.set("lastId", String(data.id));
} catch (e) {
  // JSON ではないメッセージなので無視します
}
`,
      },
    },
  ],

  composer: [
    {
      name: {
        "zh-CN": "串行调用两个请求并断言状态码",
        "en-US": "Call two requests in order and assert their status",
        ja: "2 つのリクエストを順に呼び、ステータスを検証",
      },
      code: {
        "zh-CN": `// 请求名 = 左侧工作区里的条目名；串行调用，单个请求出错不中断整段脚本。
let login = null;
try {
  login = nd.composer.run("登录");
  console.log("登录", login.status);
} catch (e) {
  console.error("调用「登录」失败：" + e);
}
nd.test("登录返回 2xx", () =>
  nd.assert(Boolean(login) && login.status < 300, "status=" + (login ? login.status : "无")),
);

try {
  const list = nd.composer.run("用户列表");
  nd.test("列表返回 200", () => nd.assert(list.status === 200, "status=" + list.status));
} catch (e) {
  nd.test("列表返回 200", () => nd.assert(false, String(e)));
}
`,
        "en-US": `// The name is a workspace entry on the left; calls run one after another and a failure does not stop the script.
let login = null;
try {
  login = nd.composer.run("Login");
  console.log("Login", login.status);
} catch (e) {
  console.error("Calling Login failed: " + e);
}
nd.test("Login returns 2xx", () =>
  nd.assert(Boolean(login) && login.status < 300, "status=" + (login ? login.status : "none")),
);

try {
  const list = nd.composer.run("User list");
  nd.test("List returns 200", () => nd.assert(list.status === 200, "status=" + list.status));
} catch (e) {
  nd.test("List returns 200", () => nd.assert(false, String(e)));
}
`,
        ja: `// 名前は左側ワークスペースの項目名です。順に呼び出し、1 つの失敗でスクリプト全体は止まりません。
let login = null;
try {
  login = nd.composer.run("ログイン");
  console.log("ログイン", login.status);
} catch (e) {
  console.error("「ログイン」の呼び出しに失敗: " + e);
}
nd.test("ログインが 2xx", () =>
  nd.assert(Boolean(login) && login.status < 300, "status=" + (login ? login.status : "なし")),
);

try {
  const list = nd.composer.run("ユーザー一覧");
  nd.test("一覧が 200", () => nd.assert(list.status === 200, "status=" + list.status));
} catch (e) {
  nd.test("一覧が 200", () => nd.assert(false, String(e)));
}
`,
      },
    },
    {
      name: {
        "zh-CN": "登录 token 写进变量并带在后续调用里",
        "en-US": "Store the login token and pass it to later calls",
        ja: "ログインの token を変数に入れて次の呼び出しに渡す",
      },
      code: {
        "zh-CN": `// 登录拿 token 写进变量，后面的请求用 options.vars 覆盖这次调用的变量。
const login = nd.composer.run("登录");
const token = login.json().token;
nd.vars.set("token", token);
console.log("已写入变量 token");

// options.vars 是单次覆盖：这次调用在变量池的副本上跑，{{token}} 与被调条目的
// 脚本都取到它，调用结束后变量池保持原样（脚本写下的值仍会随变量表写回）。
const profile = nd.composer.run("我的资料", {
  vars: { token: token, client: "netdream" },
});
nd.test("资料接口 2xx", () => nd.assert(profile.status < 300, "status=" + profile.status));
`,
        "en-US": `// Keep the login token as a variable, then hand it to the next call through options.vars.
const login = nd.composer.run("Login");
const token = login.json().token;
nd.vars.set("token", token);
console.log("Stored variable token");

// options.vars is a per-call override: this call runs on a copy of the variable pool,
// so {{token}} and the called entry's script see it, and the pool is left as it was
// afterwards (what the script itself wrote is still written back).
const profile = nd.composer.run("My profile", {
  vars: { token: token, client: "netdream" },
});
nd.test("Profile returns 2xx", () => nd.assert(profile.status < 300, "status=" + profile.status));
`,
        ja: `// ログインで得た token を変数に入れ、次の呼び出しには options.vars で渡します。
const login = nd.composer.run("ログイン");
const token = login.json().token;
nd.vars.set("token", token);
console.log("変数 token を保存しました");

// options.vars はこの呼び出しだけの上書きです。変数プールの複製で実行されるため、
// {{token}} も呼ばれた項目のスクリプトもこの値を見ますが、呼び出し後にプールは
// 元のままです（スクリプト自身が書いた値は変数表へ書き戻されます）。
const profile = nd.composer.run("プロフィール", {
  vars: { token: token, client: "netdream" },
});
nd.test("プロフィールが 2xx", () => nd.assert(profile.status < 300, "status=" + profile.status));
`,
      },
    },
    {
      name: {
        "zh-CN": "运行前弹窗询问参数",
        "en-US": "Ask for a parameter up front",
        ja: "実行前にパラメータを尋ねる",
      },
      code: {
        "zh-CN": `// 运行开始先弹输入框问参数：确认后写入变量并继续，后面的调用经 nd.vars.get 读取；
// 用户点「取消」会抛异常，catch 后给个默认值兜底。等待输入期间整个运行不计时。
try {
  nd.input("keyword", "要搜索的关键词？");
} catch (e) {
  nd.vars.set("keyword", "默认");
}
console.log("关键词", nd.vars.get("keyword"));

const res = nd.composer.run("搜索");
nd.test("搜索返回 2xx", () => nd.assert(res.status < 300, "status=" + res.status));
`,
        "en-US": `// Ask for a parameter up front: confirming stores it in a variable and the run continues;
// later calls read it with nd.vars.get. Cancelling throws, so catch it and fall back to a
// default. The whole run is not timed while it waits for the input.
try {
  nd.input("keyword", "Keyword to search for?");
} catch (e) {
  nd.vars.set("keyword", "default");
}
console.log("keyword", nd.vars.get("keyword"));

const res = nd.composer.run("Search");
nd.test("Search returns 2xx", () => nd.assert(res.status < 300, "status=" + res.status));
`,
        ja: `// 実行開始時にパラメータを尋ねます：確認すると変数に書き込まれ、以後の呼び出しは
// nd.vars.get で読みます。「キャンセル」は例外になるので catch して既定値にします。
// 入力を待っている間、実行全体の計時は止まります。
try {
  nd.input("keyword", "検索キーワードは？");
} catch (e) {
  nd.vars.set("keyword", "default");
}
console.log("keyword", nd.vars.get("keyword"));

const res = nd.composer.run("検索");
nd.test("検索が 2xx", () => nd.assert(res.status < 300, "status=" + res.status));
`,
      },
    },
  ],
};

/** 某个钩子的范例，按当前界面语言取名字与代码。 */
export function examplesOf(context: ScriptExampleContext): { name: string; code: string }[] {
  const lang = getLang();
  return EXAMPLES[context].map((example) => ({
    name: example.name[lang],
    code: example.code[lang],
  }));
}

/**
 * 把一条范例接到已有脚本后面：空脚本整体填入，非空则追加，中间隔一行带范例名
 * 的注释分隔线（注释语言跟随界面）。用户写了一半的脚本绝不能被覆盖。
 */
export function appendExample(current: string, code: string, name: string): string {
  const block = code.replace(/\s+$/, "");
  if (block === "") return current;
  if (current.trim() === "") return `${block}\n`;
  return `${current.replace(/\s+$/, "")}\n\n${t("editor.exampleSeparator", { name })}\n${block}\n`;
}

/**
 * 在 host 里挂一个「插入范例」按钮 + 下拉菜单（交互与侧栏「＋ 新建请求」
 * 一致：点按钮开合、点外面或 Esc 关闭、点条目选中后关闭）。
 *
 * insert 拿到选中的代码与名字：名字给调用方拼分隔注释用，脚本编辑器据此
 * 判断「整体填入」还是「追加」。
 *
 * 返回销毁函数（摘掉文档监听与语言订阅、清空 host）。
 */
export function mountExamplePicker(
  host: HTMLElement,
  context: ScriptExampleContext,
  insert: (code: string, name: string) => void,
): () => void {
  const root = document.createElement("div");
  root.className = "script-example";

  const button = document.createElement("button");
  button.type = "button";
  button.className = "script-example-btn";
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");

  const menu = document.createElement("div");
  menu.className = "script-example-menu";
  menu.setAttribute("role", "menu");
  menu.hidden = true;

  const close = (): void => {
    menu.hidden = true;
    button.setAttribute("aria-expanded", "false");
  };

  // 文案与菜单内容都跟界面语言走，切语言时整块重画。
  const render = (): void => {
    const title = t("editor.insertExampleTitle");
    button.textContent = t("editor.insertExample");
    button.title = title;
    button.setAttribute("aria-label", title);
    menu.textContent = "";
    for (const example of examplesOf(context)) {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "script-example-item";
      item.setAttribute("role", "menuitem");
      item.textContent = example.name;
      item.addEventListener("click", () => {
        close();
        insert(example.code, example.name);
      });
      menu.append(item);
    }
  };

  const onDocumentClick = (event: MouseEvent): void => {
    if (!(event.target instanceof Node)) return;
    if (root.contains(event.target)) return;
    close();
  };

  const onDocumentKey = (event: KeyboardEvent): void => {
    if (event.key === "Escape") close();
  };

  button.addEventListener("click", (event) => {
    event.stopPropagation();
    if (!menu.hidden) {
      close();
      return;
    }
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    menu.querySelector<HTMLButtonElement>(".script-example-item")?.focus();
  });

  document.addEventListener("click", onDocumentClick);
  document.addEventListener("keydown", onDocumentKey);
  const offLang = onLangChange(render);

  render();
  root.append(button, menu);
  host.append(root);

  return () => {
    offLang();
    document.removeEventListener("click", onDocumentClick);
    document.removeEventListener("keydown", onDocumentKey);
    root.remove();
  };
}
