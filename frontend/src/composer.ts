/**
 * Composer 自动化编排的会话：一次运行 = 一段脚本 + 一个事件通道。
 *
 * 与 grpc.ts / socket.ts 同一套会话模式（应用只有一个编辑器，同时只跑一次）：
 * - 状态机 idle → running → done；终止走 ComposerService.stop(id)（幂等），
 *   用户主动终止后后端回来的取消错误被吞掉；
 * - 会话记着发起运行的编辑器对象：换条目/载入工作区之后迟到的事件一律丢弃，
 *   既不写控制台也不写变量表；
 * - 事件驱动：ComposerService.run 不 await，日志与控制台由事件逐条落成。
 *
 * 事件 → 控制台行的映射见 handleEvent；界面在 components/composerPanel.ts。
 */

import { Channel } from "mygo-runtime";
import { t } from "./i18n";
import type { ComposerEvent, ComposerResult, ComposerRunParams, SavedRequest } from "./mygo";
import { ComposerService } from "./mygo";
import { requireScriptTrust } from "./scriptGate";
import {
  getRequest,
  getVars,
  getWorkspace,
  setVars,
  updateResponse,
  type RequestState,
} from "./state";

export type ComposerStatus = "idle" | "running" | "done";

/** 控制台行的类型：脚本日志、断言结果、单个请求的开始/结束、运行汇总、系统行。 */
export type ComposerLineType = "log" | "test" | "request" | "done" | "system";

export interface ComposerLogEntry {
  id: string;
  at: number;
  type: ComposerLineType;
  /** log 行的级别（"log" | "warn" | "error"）；其余类型为 undefined。 */
  level?: string;
  /** 正文：日志文本、断言名、请求条目名；汇总行由 tests/passed/failed 现算。 */
  text: string;
  /** test 行的结果；request 结束行的 ok（status < 400）。 */
  pass?: boolean;
  /** test 失败原因 / request 结束行的错误。 */
  error?: string;
  /** request 行是开始还是结束。 */
  phase?: "start" | "end";
  status?: number;
  timeMS?: number;
  /** done 行的断言统计与原因。 */
  tests?: number;
  passed?: number;
  failed?: number;
  reason?: string;
  /** 着色：日志灰、warn 橙、error 红、pass 绿（样式复用 sock-row 的 tone 类）。 */
  tone?: "muted" | "warn" | "error" | "pass";
}

/** 一次运行最多留这么多行（与 socket/grpc 一致）。 */
export const COMPOSER_LOG_LIMIT = 500;

export interface ComposerSummary {
  tests: number;
  passed: number;
  failed: number;
  reason: string;
}

export interface ComposerSession {
  /** 正在跑的那次运行的 id（ComposerService.stop 用它）；没跑时为 null。 */
  runId: string | null;
  status: ComposerStatus;
  log: ComposerLogEntry[];
  /** 最近一次结束时的汇总；还没跑完为 null。 */
  summary: ComposerSummary | null;
  /**
   * 发起运行的编辑器对象：换条目/载入工作区会换掉它（state 的 applyRequest），
   * 迟到的事件凭它判断还该不该写界面。
   */
  editor: RequestState | null;
  stoppedByUser: boolean;
}

const session: ComposerSession = {
  runId: null,
  status: "idle",
  log: [],
  summary: null,
  editor: null,
  stoppedByUser: false,
};

type Listener = () => void;
const listeners = new Set<Listener>();

export function getComposerSession(): ComposerSession {
  return session;
}

export function subscribeComposer(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function notifyComposer(): void {
  for (const listener of listeners) listener();
}

let sequence = 0;

// Webview 不一定是安全上下文，randomUUID 可能缺席；退回时间戳 + 序号。
function newId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  sequence += 1;
  return `${Date.now().toString(36)}-${sequence.toString(36)}`;
}

/* ---- 控制台行 ---- */

function appendLog(entry: ComposerLogEntry): void {
  session.log.push(entry);
  if (session.log.length > COMPOSER_LOG_LIMIT) {
    session.log.splice(0, session.log.length - COMPOSER_LOG_LIMIT);
  }
  notifyComposer();
}

/** 系统行（运行状态、本地校验失败、终止）：文案是界面文案，走词典。 */
function systemLine(text: string, tone?: "muted" | "error"): ComposerLogEntry {
  return { id: newId(), at: Date.now(), type: "system", text, tone };
}

export function clearComposerLog(): void {
  if (session.log.length === 0) return;
  session.log = [];
  notifyComposer();
}

// 事件还该不该写界面/变量：编辑器被换掉（切条目、载入工作区）之后一律丢。
function stillCurrent(): boolean {
  return session.editor === getRequest();
}

// 错误条挂在共享的响应区上（与 socket/grpc 同一个容器），换条目后迟到的错误
// 不能挂到新请求上。
function reportError(message: string): void {
  if (!stillCurrent()) return;
  updateResponse({ error: message });
}

function atOf(event: ComposerEvent): number {
  return event.at > 0 ? event.at : Date.now();
}

/* ---- 参数构造 ---- */

/**
 * 交给后端的请求快照：工作区里的全部条目，去掉 Markdown 文档（没有可调用的请
 * 求语义）与 `examples`（一条响应示例的响应体可达 10MiB，编排用不到）。
 */
function composerRequests(): SavedRequest[] {
  const items: SavedRequest[] = [];
  for (const entry of getWorkspace().entries) {
    if (entry.kind === "markdown") continue;
    const { examples: _examples, ...rest } = entry;
    items.push({ ...rest, examples: [] });
  }
  return items;
}

/* ---- 运行 / 终止 ---- */

/**
 * 运行当前条目的编排脚本（urlBar 主按钮）：校验脚本 → 脚本信任门 → 组参数 →
 * 事件通道。校验失败只在响应区挂错误条，不写工作区。
 */
export function runComposer(): void {
  if (session.status === "running") return;
  const request = getRequest();
  // 先把这次运行记在当前编辑器名下：校验失败的错误条也归这个编辑器。
  session.editor = request;

  if (request.composerScript.trim() === "") {
    reportError(t("composer.scriptRequired"));
    return;
  }
  // 编排脚本能读变量、拿响应改写后续请求、把数据送到任意地址，与 HTTP 前后脚本
  // 同级：打开的 .band 里带的编排脚本，本次载入没确认过信任之前一次都不跑。
  // 确认后从 runComposer() 重来，按当时的编辑器与工作区重新判断（见 scriptGate.ts）。
  if (!requireScriptTrust(request, () => runComposer())) return;

  const params: ComposerRunParams = {
    id: newId(),
    script: request.composerScript,
    requests: composerRequests(),
    vars: { ...getVars() },
    timeout: request.timeout,
  };

  session.runId = params.id;
  session.status = "running";
  session.log = [];
  session.summary = null;
  session.editor = request;
  session.stoppedByUser = false;
  updateResponse({ error: null });
  notifyComposer();

  const channel = new Channel<ComposerEvent>((event) => handleEvent(params, event));
  channel.onclose = () => {
    if (session.runId !== params.id) return;
    // run 返回即通道关闭；正常收尾已由 done 事件做完，这里只兜住没收到 done
    // 就结束的运行。
    if (session.status === "running") {
      session.status = "done";
      appendLog(systemLine(t("composer.closedByChannel"), "muted"));
    }
  };
  void ComposerService.run(params, channel)
    .then((result: ComposerResult) => {
      if (session.runId !== params.id) return;
      // 没收到 done 事件（或 done 早于返回被丢掉）时，用返回的汇总收尾。
      if (session.status !== "done") finish(result);
    })
    .catch((error: unknown) => {
      if (session.runId !== params.id) return;
      // 用户点了「终止」：后端报的是取消，不是失败，不记错误。
      if (session.stoppedByUser) {
        session.status = "done";
        notifyComposer();
        return;
      }
      session.status = "done";
      const message = error instanceof Error ? error.message : String(error);
      appendLog(systemLine(message, "error"));
      reportError(t("composer.runFailed", { error: message }));
    });
}

// 一次运行的正常收尾：记汇总并把状态落到 done。
function finish(result: ComposerResult): void {
  session.status = "done";
  session.summary = { ...result };
  appendLog({
    id: newId(),
    at: Date.now(),
    type: "done",
    text: "",
    tests: result.tests,
    passed: result.passed,
    failed: result.failed,
    reason: result.reason,
  });
}

function handleEvent(params: ComposerRunParams, event: ComposerEvent): void {
  if (session.runId !== params.id) return;
  if (!stillCurrent()) return;
  switch (event.type) {
    case "log": {
      const level = event.level === undefined || event.level === "" ? "log" : event.level;
      const tone = level === "error" ? "error" : level === "warn" ? "warn" : "muted";
      appendLog({
        id: newId(),
        at: atOf(event),
        type: "log",
        level,
        text: event.text ?? "",
        tone,
      });
      return;
    }
    case "test": {
      const pass = event.pass === true;
      appendLog({
        id: newId(),
        at: atOf(event),
        type: "test",
        text: event.name ?? "",
        pass,
        error: event.error ?? "",
        tone: pass ? "pass" : "error",
      });
      return;
    }
    case "request": {
      const phase = event.phase === "end" ? "end" : "start";
      const status = event.status !== undefined && event.status > 0 ? event.status : undefined;
      const error = event.error ?? "";
      const ok = phase === "start" ? undefined : error === "" && (status === undefined || status < 400);
      appendLog({
        id: newId(),
        at: atOf(event),
        type: "request",
        text: event.request ?? "",
        phase,
        pass: ok,
        error,
        status,
        timeMS: event.timeMS !== undefined && event.timeMS > 0 ? event.timeMS : undefined,
        // 开始行灰；结束行出错红，其余不额外着色（状态码徽标自己分绿/红）。
        tone: phase === "start" ? "muted" : error === "" && ok !== false ? undefined : "error",
      });
      return;
    }
    case "done": {
      // 脚本写回的变量无条件覆盖界面的变量表：脚本文本里删掉全部变量时，
      // 空对象也要生效（与 HTTP/连接脚本的 setVars 语义一致）。
      if (event.vars !== undefined) setVars(event.vars);
      finish({
        tests: event.tests ?? 0,
        passed: event.passed ?? 0,
        failed: event.failed ?? 0,
        reason: event.reason ?? "",
      });
      return;
    }
    default:
      return;
  }
}

/**
 * 终止这次运行：先本地收尾（停止按钮立刻回到「运行」，可再点运行），再通知后端
 * 取消。后端被取消后照样补一条带变量快照的 done 事件——按 runId 收下它，变量
 * 写回不被丢掉。停止是幂等的：没在跑就什么都不做。
 */
export function stopComposer(): void {
  const runId = session.runId;
  if (session.status !== "running") return;
  session.stoppedByUser = true;
  session.status = "done";
  appendLog(systemLine(t("composer.stopped"), "muted"));
  if (runId === null) return;
  void ComposerService.stop(runId).catch(() => {
    // 运行已经没了：Stop 对未知 id 本就不是错误。
  });
}

/** 换编辑器（切 kind / 载入其它条目）之前先停掉正在跑的运行。 */
export function stopComposerIfActive(): void {
  if (session.status === "running") stopComposer();
}
