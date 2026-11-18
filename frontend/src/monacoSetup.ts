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
