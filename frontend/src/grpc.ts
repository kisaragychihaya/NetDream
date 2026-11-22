import { Channel } from "mygo-runtime";
import { t } from "./i18n";
import type {
  GrpcConnectParams,
  GrpcEvent,
  GrpcListParams,
  GrpcMethodInfo,
  KeyValue,
} from "./mygo";
import { GrpcService } from "./mygo";
import { getRequest, updateResponse, wireValues, type RequestState } from "./state";

export type GrpcStatus = "idle" | "connecting" | "open" | "closed";

export interface GrpcLogEntry {
  id: string;
  at: number;
  /** send/receive 是收发消息；system 是调用状态与后端报错的行。 */
  direction: "send" | "receive" | "system";
  payload: string;
  /** 系统行的着色：错误红、其余灰。 */
  tone?: "error" | "muted";
  /** 结束行的状态码徽标（gRPC 状态名，如 "OK"）；ok 决定绿/红。 */
  status?: string;
  ok?: boolean;
  /** 结束行附带的 trailers。 */
  trailers?: KeyValue[];
  /** 系统行的词典键与插值变量：存结构而不是成品文案，切语言后按当前语言显示。 */
  i18n?: { key: string; vars?: Record<string, string | number> };
}

/** 一次调用最多留这么多行日志（与 socket.ts 一致）。 */
export const GRPC_LOG_LIMIT = 500;

export interface GrpcSession {
  connId: string | null;
  status: GrpcStatus;
  log: GrpcLogEntry[];
  /** open 事件里的响应元数据。 */
  metadata: KeyValue[];
  /** close 事件里的 trailers。 */
  trailers: KeyValue[];
  /** 建立调用时的目标地址（host:port）。 */
  target: string;
  /**
   * 发起调用的编辑器对象：换条目/载入工作区会换掉它（state 的 applyRequest），
   * 迟到的会话通知凭它判断还该不该写界面与工作区。
   */
  editor: RequestState | null;
  stoppedByUser: boolean;
  /** 结束发送（半关闭）已经做过：不能再发，但还能继续收。 */
  halfClosed: boolean;
}

const session: GrpcSession = {
  connId: null,
  status: "idle",
  log: [],
  metadata: [],
  trailers: [],
  target: "",
  editor: null,
  stoppedByUser: false,
  halfClosed: false,
};

type Listener = () => void;
const listeners = new Set<Listener>();

export function getGrpcSession(): GrpcSession {
  return session;
}

export function subscribeGrpc(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function notifyGrpc(): void {
  for (const listener of listeners) listener();
}

/* ---- 方法表（「方法」页获取，消息页按它判断流式形态） ---- */

let methods: readonly GrpcMethodInfo[] = [];

/** 最近一次「获取方法」的结果；空 = 还没获取过。 */
export function getGrpcMethods(): readonly GrpcMethodInfo[] {
  return methods;
}

export function setGrpcMethods(next: readonly GrpcMethodInfo[]): void {
  methods = next;
  notifyGrpc();
}

/** 流式形态：一元 / 服务端流式 / 客户端流式 / 双向流式。 */
export type GrpcStreamShape = "unary" | "server" | "client" | "bidi";

export function streamShapeOf(info: GrpcMethodInfo): GrpcStreamShape {
  if (info.clientStreaming && info.serverStreaming) return "bidi";
  if (info.clientStreaming) return "client";
  if (info.serverStreaming) return "server";
  return "unary";
}

// grpcMethod 存的是 "pkg.Service/Method"；生成绑定里的 fullName 是点号写法，
// 手填时两种都可能出现，所以统一成点号再比。
function normalizeMethod(value: string): string {
  return value.trim().replace("/", ".");
}

function sameMethod(info: GrpcMethodInfo, method: string): boolean {
  const normalized = normalizeMethod(method);
  if (normalizeMethod(info.fullName) === normalized) return true;
  return normalizeMethod(`${info.service}/${info.name}`) === normalized;
}

/** 编辑器当前选中的方法在方法表里的条目；没获取过方法表或没选时为 null。 */
export function currentGrpcMethod(): GrpcMethodInfo | null {
  const method = getRequest().grpcMethod.trim();
  if (method === "") return null;
  for (const info of methods) {
    if (sameMethod(info, method)) return info;
  }
  return null;
}

/** 当前方法的流式形态：没获取过方法表时按一元处理（与按钮行为一致）。 */
export function currentStreamShape(): GrpcStreamShape {
  const info = currentGrpcMethod();
  return info === null ? "unary" : streamShapeOf(info);
}

/* ---- 参数构造与校验 ---- */

let sequence = 0;

// Webview 不一定是安全上下文，randomUUID 可能缺席；退回时间戳 + 序号。
function newId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  sequence += 1;
  return `${Date.now().toString(36)}-${sequence.toString(36)}`;
}

function trimmedList(values: readonly string[]): string[] {
  const items: string[] = [];
  for (const value of values) {
    const item = value.trim();
    if (item !== "") items.push(item);
  }
  return items;
}

/** 当前编辑器状态映射成 List 的参数（获取方法用）。 */
export function grpcListParams(): GrpcListParams {
  const request = getRequest();
  return {
    target: request.url.trim(),
    plaintext: request.grpcPlaintext,
    tlsSkipVerify: request.grpcTlsSkipVerify,
    serverName: request.grpcServerName.trim(),
    metadata: wireValues(request.headers),
    protoFiles: trimmedList(request.grpcProtoFiles),
    importPaths: trimmedList(request.grpcImportPaths),
    timeout: request.timeout,
  };
}

/** 目标必须是 host:port：主机非空、端口是 1–65535 的数字。 */
export function grpcTargetProblem(target: string): string | null {
  const trimmed = target.trim();
  const marker = trimmed.lastIndexOf(":");
  if (marker <= 0) return t("grpc.urlNeedsTarget");
  const host = trimmed.slice(0, marker).trim();
  const port = Number(trimmed.slice(marker + 1));
  if (host === "" || !Number.isInteger(port) || port < 1 || port > 65535) {
    return t("grpc.urlNeedsTarget");
  }
  return null;
}

/** 消息体不是合法 JSON 时的原因；空串（后端按 "{}" 处理）不算错。 */
export function jsonProblem(payload: string): string | null {
  const text = payload.trim();
  if (text === "") return null;
  try {
    JSON.parse(text);
    return null;
  } catch (error) {
    return t("grpc.invalidJSON", { error: error instanceof Error ? error.message : String(error) });
  }
}

/* ---- 会话日志 ---- */

export function logText(entry: GrpcLogEntry): string {
  return entry.i18n === undefined ? entry.payload : t(entry.i18n.key, entry.i18n.vars);
}

function systemLine(payload: string, tone?: "error" | "muted"): GrpcLogEntry {
  return { id: newId(), at: Date.now(), direction: "system", payload, tone };
}

function systemMessage(
  key: string,
  vars?: Record<string, string | number>,
  tone?: "error" | "muted",
): GrpcLogEntry {
  return {
    id: newId(),
    at: Date.now(),
    direction: "system",
    payload: "",
    tone,
    i18n: { key, vars },
  };
}

function appendLog(entry: GrpcLogEntry): void {
  session.log.push(entry);
  if (session.log.length > GRPC_LOG_LIMIT) session.log.splice(0, session.log.length - GRPC_LOG_LIMIT);
  notifyGrpc();
}

export function clearGrpcLog(): void {
  if (session.log.length === 0) return;
  session.log = [];
  notifyGrpc();
}

// 会话事件还该不该写界面：编辑器被换掉（切条目、载入工作区）之后，迟到的错误条
// 不能挂到新请求上。会话本身的日志只属于这条调用，切换时连接已被断开。
function stillCurrent(): boolean {
  return session.editor === getRequest();
}

function reportError(message: string): void {
  if (!stillCurrent()) return;
  updateResponse({ error: message });
}

/* ---- 调用 ---- */

// grpcCall dials the method the editor describes. Connect stays for the life of
// the call, so it is not awaited: events arrive on the channel and the session
// shows them.
export function grpcCall(): void {
  const request = getRequest();
  if (session.status === "connecting" || session.status === "open") return;
  // 先把这次调用记在当前编辑器名下：校验失败的错误条也归这个编辑器。
  session.editor = request;

  const target = request.url.trim();
  const problem = grpcTargetProblem(target);
  if (problem !== null) {
    reportError(problem);
    return;
  }
  const method = request.grpcMethod.trim();
  if (method === "") {
    reportError(t("grpc.methodRequired"));
    return;
  }
  // 一元/服务端流式在调用时就把消息发出去；客户端/双向流式由「发送」逐条发。
  const shape = currentStreamShape();
  const streaming = shape === "client" || shape === "bidi";
  if (!streaming) {
    const payloadProblem = jsonProblem(request.raw);
    if (payloadProblem !== null) {
      reportError(payloadProblem);
      return;
    }
  }

  const params: GrpcConnectParams = {
    ...grpcListParams(),
    id: newId(),
    method,
    payload: streaming ? "" : request.raw,
  };

  session.connId = params.id;
  session.status = "connecting";
  session.log = [];
  session.metadata = [];
  session.trailers = [];
  session.target = params.target;
  session.editor = request;
  session.stoppedByUser = false;
  session.halfClosed = false;
  updateResponse({
    sending: false,
    streaming: false,
    open: null,
    events: [],
    response: null,
    error: null,
    results: [],
    stopped: false,
  });
  notifyGrpc();

  const channel = new Channel<GrpcEvent>((event) => handleEvent(params, event));
  channel.onclose = () => {
    if (session.connId !== params.id) return;
    // Connect 返回即通道关闭；正常收尾已由 close/error 事件做完，这里只兜住
    // 没收到任何事件就结束的调用。
    if (session.status === "connecting" || session.status === "open") {
      session.status = "closed";
      appendLog(systemMessage("grpc.closedByChannel", undefined, "muted"));
    }
  };
  void GrpcService.connect(params, channel).catch((error: unknown) => {
    if (session.connId !== params.id) return;
    if (session.stoppedByUser) return;
    const message = error instanceof Error ? error.message : String(error);
    // 后端会先推一条 error 事件：那条已经记过一行，这里只补上没报过的。
    const reported = session.status === "closed";
    session.status = "closed";
    reportError(message);
    if (!reported) appendLog(systemLine(message, "error"));
  });
}

function handleEvent(params: GrpcConnectParams, event: GrpcEvent): void {
  if (session.connId !== params.id) return;
  switch (event.type) {
    case "open": {
      session.status = "open";
      session.metadata = event.headers ?? [];
      appendLog(
        systemMessage("grpc.connected", { target: params.target, method: params.method }, "muted"),
      );
      return;
    }
    case "message": {
      const entry: GrpcLogEntry = {
        id: newId(),
        at: event.at > 0 ? event.at : Date.now(),
        direction: event.direction === "send" ? "send" : "receive",
        payload: event.payload ?? "",
      };
      appendLog(entry);
      return;
    }
    case "error": {
      session.status = "closed";
      const reason = event.reason === undefined || event.reason === "" ? t("grpc.callError") : event.reason;
      const code = event.code ?? "";
      if (code !== "") reportError(t("grpc.errorWithCode", { reason, code }));
      else reportError(reason);
      appendLog(
        code === "" ? systemLine(reason, "error") : systemMessage("grpc.errorWithCode", { reason, code }, "error"),
      );
      return;
    }
    case "close": {
      session.status = "closed";
      session.trailers = event.trailers ?? [];
      const code = event.code ?? "";
      const reason = event.reason ?? "";
      const entry: GrpcLogEntry =
        reason === ""
          ? systemMessage("grpc.closed", { code: code === "" ? t("grpc.noStatus") : code }, "muted")
          : systemMessage(
              "grpc.closedWithReason",
              { code: code === "" ? t("grpc.noStatus") : code, reason },
              "muted",
            );
      if (code !== "") {
        entry.status = code;
        entry.ok = code === "OK";
      }
      if (session.trailers.length > 0) entry.trailers = session.trailers;
      appendLog(entry);
      return;
    }
    default:
      return;
  }
}

/* ---- 发送 / 半关闭 ---- */

// sendGrpcMessage writes one message on a client/bidi-streaming call; the log
// line comes back from the backend as a direction:"send" event, so it is not
// faked here. "connecting" counts as sendable: a client-streaming server's
// headers often arrive only with its final answer, long after the first message
// had to go out — the backend queues sends from the moment the call registers.
export function sendGrpcMessage(payload: string): void {
  const connId = session.connId;
  if (connId === null || session.halfClosed || (session.status !== "connecting" && session.status !== "open")) {
    reportError(t("grpc.notConnected"));
    return;
  }
  const problem = jsonProblem(payload);
  if (problem !== null) {
    reportError(problem);
    return;
  }
  void GrpcService.send(connId, payload).catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    reportError(message);
    if (session.connId === connId) appendLog(systemLine(message, "error"));
  });
}

/** 结束发送（半关闭）：之后不再发送，响应继续到达直到服务端关流。 */
export function closeGrpcSend(): void {
  const connId = session.connId;
  if (connId === null || session.halfClosed || (session.status !== "connecting" && session.status !== "open")) return;
  session.halfClosed = true;
  appendLog(systemMessage("grpc.sendClosed", undefined, "muted"));
  void GrpcService.closeSend(connId).catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    reportError(message);
    if (session.connId === connId) appendLog(systemLine(message, "error"));
  });
}

export function disconnectGrpc(): void {
  const connId = session.connId;
  if (session.status === "idle" || session.status === "closed") {
    session.connId = null;
    return;
  }
  session.stoppedByUser = true;
  session.connId = null;
  session.status = "closed";
  appendLog(systemMessage("grpc.disconnected", undefined, "muted"));
  if (connId === null) return;
  void GrpcService.disconnect(connId).catch(() => {
    // 调用已经没了：Disconnect 对未知 id 本就不是错误。
  });
}

// stopGrpcIfActive closes a live call before the editor switches kind or loads
// another entry.
export function stopGrpcIfActive(): void {
  if (session.status === "connecting" || session.status === "open") disconnectGrpc();
}
