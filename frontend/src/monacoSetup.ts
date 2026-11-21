// 脚本编辑器只在用户第一次切到「预执行 / 后执行」页签时才需要：Monaco 编辑器本体、
// 语言服务与 contribution 全部走动态 import（`loadMonaco`），启动时主包不再带上它们。
// 只引入脚本编辑真正用到的部分：`editor.api`（编辑器本体 + languages 注册表），
// JavaScript 语法与语言服务（ts worker 提供补全、悬浮、诊断、格式化），以及一份
// 精选的编辑器 contribution。原先的 `import * as monaco from "monaco-editor"`
// 会把全部语言、css/html/json 语言服务与它们的 worker 一起打进构建。
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import TypeScriptWorker from "monaco-editor/language/typescript/ts.worker.js?worker";

import { getEffectiveTheme, onThemeChange } from "./theme";

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

/**
 * 对称加密工作模式：cbc（默认，PKCS#7 填充）、ecb（PKCS#7 填充、iv 必须为空）
 * 或 gcm（仅 AES，认证加密）。
 */
type NDCryptoAesMode = "cbc" | "ecb" | "gcm";

/** DES / 3DES 工作模式：cbc（默认）或 ecb；gcm 在运行时会抛异常，类型层面不允许。 */
type NDDesMode = "cbc" | "ecb";

/** 字节级 API 接受的输入：Uint8Array 或 ArrayBuffer。 */
type NDCryptoBytes = Uint8Array | ArrayBuffer;

/** HMAC / PBKDF2 支持的摘要算法（大小写不敏感）。 */
type NDCryptoHashAlgo = "md5" | "sha1" | "sha224" | "sha256" | "sha384" | "sha512";

/** RSA 签名/验签支持的摘要算法（拒绝 md5）。 */
type NDCryptoRsaHash = "sha1" | "sha224" | "sha256" | "sha384" | "sha512";

/** JWT 算法：HS* 的 secret 是字符串，RS* 的 secret 是 PEM 私钥/公钥。 */
type NDCryptoJwtAlgo = "HS256" | "HS384" | "HS512" | "RS256" | "RS384" | "RS512";

/**
 * 哈希 / HMAC / 对称加密 / RSA / PBKDF2 / JWT / 编码 / 随机数。
 * 参数与返回值默认是字符串（变量只能存字符串），字节用 base64、hex 或 raw 表达；
 * 需要真正的字节数组时用 bytesFromBase64 / bytesFromHex（返回 Uint8Array），
 * 或用 bytesToBase64 / bytesToHex 编码 Uint8Array / ArrayBuffer。
 *
 * 注意：沙箱内置的 Uint8Array.prototype.toBase64/toHex 按 UTF-16 码元逐字节
 * 处理，含中文或 ≥0x80 的字节会被截断；UTF-8 字节请走本接口的 bytes* 方法。
 *
 * key / iv / 签名等二进制字符串支持前缀：hex:（十六进制）、b64:（标准 base64）、
 * raw:（字面 UTF-8 字节）；无前缀按标准 base64 解码（兼容旧脚本）。
 */
interface NDCrypto {
  /** MD5 摘要，按 UTF-8 取字节。 */
  md5(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-1 摘要。 */
  sha1(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-224 摘要。 */
  sha224(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-256 摘要。 */
  sha256(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-384 摘要。 */
  sha384(data: string, encoding?: NDCryptoEncoding): string;
  /** SHA-512 摘要。 */
  sha512(data: string, encoding?: NDCryptoEncoding): string;
  /**
   * HMAC。注意参数顺序是 key 在前、data 在后（旧版是 data 在前，迁移时对调）。
   * @param algo 摘要算法（大小写不敏感）
   * @param key 密钥，按 UTF-8 取字节
   * @param data 消息
   * @param encoding 输出编码，默认 "hex"
   */
  hmac(algo: NDCryptoHashAlgo, key: string, data: string, encoding?: NDCryptoEncoding): string;
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
  /** RFC 3986 百分号编码：unreserved 透传、空格→%20、/→%2F、hex 大写；不会输出 +。 */
  urlEncode(data: string): string;
  /** RFC 3986 解码；+ 按字面加号处理，非法百分号编码抛异常。 */
  urlDecode(data: string): string;
  /** 把 Uint8Array / ArrayBuffer 编成标准 base64。 */
  bytesToBase64(bytes: NDCryptoBytes): string;
  /** 把标准 base64 解成真正的 Uint8Array（可索引、instanceof Uint8Array）。 */
  bytesFromBase64(text: string): Uint8Array;
  /** 把 Uint8Array / ArrayBuffer 编成小写 hex 字符串。 */
  bytesToHex(bytes: NDCryptoBytes): string;
  /** 把 hex 字符串解成真正的 Uint8Array（可索引、instanceof Uint8Array）；非法字符抛异常。 */
  bytesFromHex(text: string): Uint8Array;
  /**
   * AES 加密，返回 base64 密文。
   * @param key 16/24/32 字节（AES-128/192/256），支持 hex:/b64:/raw: 前缀，默认 base64
   * @param iv cbc 需 16 字节；ecb 省略或空串；gcm 省略时自动生成 12 字节随机 nonce
   * @param mode 默认 "cbc"
   */
  aesEncrypt(data: string, key: string, iv?: string | null, mode?: NDCryptoAesMode): string;
  /**
   * AES 解密，输入 base64 密文；cbc/ecb 会校验 PKCS#7 填充。
   * gcm 密文格式为 nonce‖密文‖tag：iv 省略时自动从密文前 12 字节切分 nonce；
   * 显式给 iv 时按该 nonce 解密，同时兼容带 nonce 前缀的新密文和无前缀的旧密文。
   */
  aesDecrypt(data: string, key: string, iv?: string | null, mode?: NDCryptoAesMode): string;
  /**
   * DES（8 字节 key）或 3DES（16/24 字节 key）加密，返回 base64；仅支持 cbc/ecb。
   * 16 字节 key 按 2-key 3DES（K1‖K2‖K1）展开。
   */
  desEncrypt(data: string, key: string, iv?: string | null, mode?: NDDesMode): string;
  /** DES / 3DES 解密；仅支持 cbc/ecb（gcm 会抛异常）。 */
  desDecrypt(data: string, key: string, iv?: string | null, mode?: NDDesMode): string;
  /**
   * RSA PKCS#1 v1.5 签名，返回 base64。
   * @param privateKey PKCS#1 或 PKCS#8 的 RSA PEM
   * @param hash 摘要算法，默认 "sha256"（拒绝 md5）
   */
  rsaSign(data: string, privateKey: string, hash?: NDCryptoRsaHash): string;
  /** RSA PKCS#1 v1.5 验签；signature 为 base64，返回是否通过。 */
  rsaVerify(data: string, signature: string, publicKey: string, hash?: NDCryptoRsaHash): boolean;
  /** RSA-PSS 签名（SaltLength = hash 长度），返回 base64。 */
  rsaPssSign(data: string, privateKey: string, hash?: NDCryptoRsaHash): string;
  /** RSA-PSS 验签；signature 为 base64，返回是否通过。 */
  rsaPssVerify(data: string, signature: string, publicKey: string, hash?: NDCryptoRsaHash): boolean;
  /** RSA PKCS#1 v1.5 公钥加密，返回 base64。 */
  rsaEncrypt(data: string, publicKey: string): string;
  /** RSA PKCS#1 v1.5 私钥解密，输入 base64。 */
  rsaDecrypt(data: string, privateKey: string): string;
  /**
   * PBKDF2（RFC 2898），返回 base64。
   * @param iterations 迭代次数，1–10,000,000
   * @param keyLen 输出字节数，1–4096
   * @param hash 默认 "sha256"
   */
  pbkdf2(password: string, salt: string, iterations: number, keyLen: number, hash?: NDCryptoHashAlgo): string;
  /**
   * JWT 签名。header 固定为 {"alg": algo, "typ": "JWT"}。
   * @param payload 可 JSON 序列化的值
   * @param secretOrKey HS* 为字符串 secret；RS* 为 PEM 私钥
   * @param algo 默认 "HS256"
   */
  jwtSign(payload: any, secretOrKey: string, algo?: NDCryptoJwtAlgo): string;
  /**
   * JWT 验签，成功返回解析后的 payload 对象。
   * 传了 algo 时先比对 header.alg（防止 alg 降级），不一致或验签失败都抛异常。
   * @param secretOrKey HS* 为字符串 secret；RS* 为 PEM 公钥
   */
  jwtVerify(token: string, secretOrKey: string, algo?: NDCryptoJwtAlgo): any;
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
   * 按布局格式化毫秒时间戳。
   * @param ms 毫秒时间戳（如 Date.now() 或 parse() 的返回值）
   * @param layout 两种写法：token 布局（含 YY 或 SSS 时启用，如
   *   "YYYY-MM-DD HH:mm:ss.SSS"：YYYY/YY 年、MM/M 月、DD/D 日、HH/H 时、
   *   mm/m 分、ss/s 秒、SSS 毫秒），或 Go 参考时刻布局（如
   *   "2006-01-02 15:04:05"：2006 年、01 月、02 日、15 时、04 分、05 秒、
   *   .000 毫秒、MST 时区缩写、-07:00 偏移）。不能为空。
   * @param timeZone IANA 时区名（如 "Asia/Shanghai"、"UTC"）；省略或空串按本地时区。
   *   不认识的名字会抛异常。时区数据库已内嵌在可执行文件里。
   */
  format(ms: number, layout: string, timeZone?: string): string;
  /**
   * 按布局严格解析时间字符串，返回毫秒时间戳。
   * @param s 要解析的文本
   * @param layout 与 format 相同的两种写法（token 布局或 Go 布局），必须与 s 的结构一致
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
  /** 哈希 / HMAC / 对称加密 / RSA / PBKDF2 / JWT / 编码 / 随机数；UTF-8 安全。 */
  crypto: NDCrypto;
  /** 时间戳、token 或 Go 布局的格式化与解析、本地时区：时区语义明确，优先于 Date。 */
  time: NDTime;
}

/** NetDream 注入的全局脚本对象。 */
declare const nd: ND;
`;

type MonacoApi = typeof import("monaco-editor/editor/editor.api.js");

export type { MonacoApi };

let monacoApi: MonacoApi | null = null;
let loading: Promise<MonacoApi> | null = null;

/**
 * 加载 Monaco（编辑器本体 + JS 语言服务 + 用到的 contribution）。
 * 第一次调用才真正拉包；连续 ensure 共用同一次加载；失败后清掉缓存，
 * 下次切到脚本页签还能重试。
 */
export function loadMonaco(): Promise<MonacoApi> {
  if (monacoApi !== null) return Promise.resolve(monacoApi);
  loading ??= (async () => {
    // worker 标签：javascript 也交给 ts worker，它同时提供 JS 的补全与校验。
    const environment = {
      getWorker(_workerId: string, label: string): Worker {
        return label === "typescript" || label === "javascript" ? new TypeScriptWorker() : new EditorWorker();
      },
    };
    (self as unknown as { MonacoEnvironment: typeof environment }).MonacoEnvironment = environment;

    const api = await import("monaco-editor/editor/editor.api.js");
    // 语言服务声明装在 languages/features/typescript/register.js，
    // language/typescript/monaco.contribution.js 只是它的再导出（那一层没有类型
    // 声明，所以直接从有 d.ts 的路径拿 javascriptDefaults / ScriptTarget）。
    // contribution 与 JS 语法高亮都在 monacoContributions 里一起拉。
    const [, typescript] = await Promise.all([
      import("./monacoContributions"),
      import("monaco-editor/languages/features/typescript/register.js"),
    ]);

    typescript.javascriptDefaults.setCompilerOptions({
      target: typescript.ScriptTarget.ES2020,
      allowJs: true,
      allowNonTsExtensions: true,
      checkJs: false,
      noEmit: true,
    });
    typescript.javascriptDefaults.addExtraLib(ND_API_DTS, "netdream-nd.d.ts");

    api.editor.setTheme(monacoTheme());
    monacoApi = api;
    return api;
  })();
  return loading.catch((error: unknown) => {
    // 加载失败（构建缺 chunk 等）：下次 ensure 重试，别把失败永久缓存。
    loading = null;
    throw error;
  });
}

/**
 * 编辑器主题跟着界面主题走：Monaco 画在 canvas 上，CSS 变量管不到它，
 * 跟随系统时系统配色一变必须手动换。
 */
export function monacoTheme(): "vs" | "vs-dark" {
  return getEffectiveTheme() === "dark" ? "vs-dark" : "vs";
}

// 已经创建的编辑器实例由 monaco.editor.setTheme 统一切换；还没加载时什么都不用做，
// loadMonaco 会在创建前按当时的主题设好。
onThemeChange(() => {
  monacoApi?.editor.setTheme(monacoTheme());
});
