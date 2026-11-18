import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import TypeScriptWorker from "monaco-editor/language/typescript/ts.worker.js?worker";

// 补全与悬浮文档的数据源：在后端 goja 里运行时的同名 API（见 docs/architecture.md）。
const ND_API_DTS = `
/** NetDream 变量表：与 {{变量名}} 替换共用同一份数据。 */
interface NDVars {
  /** 读取变量的值；不存在时返回 null。 */
  get(key: string): string | null;
  /** 写入变量（值会被转成字符串）。 */
  set(key: string, value: string): void;
  /** 删除变量。 */
  unset(key: string): void;
  /** 变量是否存在。 */
  has(key: string): boolean;
  /** 导出全部变量的副本。 */
  toObject(): Record<string, string>;
}

/**
 * 请求信息：预执行脚本里可写，改动作用于本次发送。
 * 后执行脚本里为只读快照。
 */
interface NDRequest {
  /** 请求方法，如 "GET"。 */
  method: string;
  /** 请求 URL（不含查询参数），可整体改写。 */
  url: string;
  /** 请求头，键值均为字符串。 */
  headers: Record<string, string>;
  /** 查询参数。 */
  query: Record<string, string>;
  /** 请求体文本。 */
  body: string;
}

/** 响应信息：仅后执行脚本可用。 */
interface NDResponse {
  /** HTTP 状态码。 */
  status: number;
  /** 状态文本，如 "OK"。 */
  statusText: string;
  /** 响应头。 */
  headers: Record<string, string>;
  /** 响应体文本。 */
  body: string;
  /** 耗时（毫秒）。 */
  timeMS: number;
  /** 响应体大小（字节）。 */
  sizeBytes: number;
  /** 把响应体解析成 JSON；解析失败会抛异常。 */
  json(): any;
}

/** 摘要输出的编码方式：hex（默认）、标准 base64、URL 安全 base64（无 padding）。 */
type NDCryptoEncoding = "hex" | "base64" | "base64url";

/** AES 工作模式：cbc（默认，PKCS#7 填充）或 gcm（认证加密）。 */
type NDCryptoAesMode = "cbc" | "gcm";

/**
 * 哈希 / HMAC / AES / 编码 / 随机数。
 * 参数与返回值一律是字符串（变量只能存字符串），字节用 base64 或 hex 表达。
 */
interface NDCrypto {
  /** MD5 摘要，按 UTF-8 取字节。 */
  md5(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-1 摘要。 */
  sha1(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-256 摘要。 */
  sha256(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-512 摘要。 */
  sha512(data: string, encoding?: NDCryptoEncoding): string;
  /** HMAC：data 与 key 均按 UTF-8 取字节。 */
  hmac(algo: "md5" | "sha1" | "sha256" | "sha512", data: string, key: string, encoding?: NDCryptoEncoding): string;
  /** 标准 base64 编码（UTF-8 安全，中文可直接传入）。 */
  base64Encode(data: string): string;
  /** 标准 base64 解码；非法输入抛异常。 */
  base64Decode(data: string): string;
  /** URL 安全 base64 编码（不带 padding）。 */
  base64UrlEncode(data: string): string;
  /** URL 安全 base64 解码（带或不带 padding 都接受）。 */
  base64UrlDecode(data: string): string;
  /** 十六进制编码（UTF-8 安全）。 */
  hexEncode(data: string): string;
  /** 十六进制解码；非法字符抛异常。 */
  hexDecode(data: string): string;
  /** AES 加密，返回 base64 密文；key、iv 为 base64，key 需 16/24/32 字节，cbc 的 iv 16 字节、gcm 的 iv 12 字节。 */
  aesEncrypt(data: string, key: string, iv: string, mode?: NDCryptoAesMode): string;
  /** AES 解密，输入 base64 密文；cbc 会校验 PKCS#7 填充。 */
  aesDecrypt(data: string, key: string, iv: string, mode?: NDCryptoAesMode): string;
  /** crypto/rand 生成 n 字节（1–4096），返回 base64。 */
  randomBytes(n: number): string;
  /** crypto/rand 生成 n 字节（1–4096），返回 hex。 */
  randomHex(n: number): string;
}

/**
 * 时间戳、Go 布局的格式化与严格解析、本地时区。
 * Date 在沙箱里能用，但它的日期字符串解析带隐式时区、toLocale* 会忽略参数，
 * 需要确定的时区语义时走这里。
 */
interface NDTime {
  /** 当前毫秒时间戳，等价 Date.now()。 */
  now(): number;
  /** 当前秒时间戳，等价 Math.floor(Date.now() / 1000)。 */
  unix(): number;
  /**
   * 按 Go 布局格式化毫秒时间戳。
   * @param ms 毫秒时间戳（如 Date.now() 或 parse() 的返回值）
   * @param layout Go 参考时刻写法，如 "2006-01-02 15:04:05"：2006 年、01 月、
   *   02 日、15 时（24 小时制）、04 分、05 秒、.000 毫秒、MST 时区缩写、
   *   -07:00 偏移（Z07:00 在 UTC 输出 Z）。不能为空。
   * @param timeZone IANA 时区名（如 "Asia/Shanghai"、"UTC"）；省略或空串按本地时区。
   *   不认识的名字会抛异常。
   */
  format(ms: number, layout: string, timeZone?: string): string;
  /**
   * 按 Go 布局严格解析时间字符串，返回毫秒时间戳。
   * @param s 要解析的文本
   * @param layout Go 参考时刻写法，必须与 s 的结构一致
   * @param timeZone 解读 s 时用的 IANA 时区名；省略或空串按本地时区
   * @returns 毫秒时间戳；字符串不符合布局或日期不存在（如 "2025-02-31"）时返回
   *   null（不抛异常，和 Date.parse 的静默归一化不同）。
   */
  parse(s: string, layout: string, timeZone?: string): number | null;
  /**
   * 本地时区偏移的分钟数，符号与 Date.getTimezoneOffset() 相同：
   * 东八区为 -480，UTC 为 0。
   */
  offset(): number;
  /** 本地时区名（Go 的 time.Local，Windows 上通常是 "Local"）。 */
  zone(): string;
}

/** NetDream 注入的脚本全局对象（同步 API，没有 async/await）。 */
interface ND {
  /** 变量表：脚本写回后随响应返回，并同步到界面的变量区。 */
  vars: NDVars;
  /** 请求信息；预执行脚本可写。 */
  request: NDRequest;
  /** 响应信息；仅后执行脚本可用。 */
  response: NDResponse;
  /** 记录一条测试结果到「断言与校验」的「脚本测试」：fn 抛异常即视为失败。 */
  test(name: string, fn: () => void): void;
  /** 断言：条件不成立时抛异常（在 nd.test 内即记为失败）。 */
  assert(cond: unknown, message?: string): void;
  /** 哈希 / HMAC / AES / base64、hex 编码 / 随机数：字符串进出，UTF-8 安全。 */
  crypto: NDCrypto;
  /** 时间戳、Go 布局的格式化与解析、本地时区：时区语义明确，优先于 Date。 */
  time: NDTime;
}

/** NetDream 注入的全局脚本对象。 */
declare const nd: ND;
`;

const environment = {
  getWorker(_workerId: string, label: string): Worker {
    // javascript 标签也交给 ts worker：它同时提供 JS 的补全与校验。
    return label === "typescript" || label === "javascript" ? new TypeScriptWorker() : new EditorWorker();
  },
};

(self as unknown as { MonacoEnvironment: typeof environment }).MonacoEnvironment = environment;

const typescript = monaco.typescript;
typescript.javascriptDefaults.setCompilerOptions({
  target: typescript.ScriptTarget.ES2020,
  allowJs: true,
  allowNonTsExtensions: true,
  checkJs: false,
  noEmit: true,
});
typescript.javascriptDefaults.addExtraLib(ND_API_DTS, "netdream-nd.d.ts");

export { monaco };
