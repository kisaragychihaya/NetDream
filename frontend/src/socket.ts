import { Channel } from "mygo-runtime";
import { t } from "./i18n";
import type { KeyValue, SocketConnectParams, SocketEvent } from "./mygo";
import { SocketService } from "./mygo";
import { getRequest, updateResponse, wireValues, type RequestKind } from "./state";

export type SocketStatus = "idle" | "connecting" | "open" | "closed";

export interface SocketLogEntry {
  id: string;
  at: number;
  /** send/receive 是收发消息；system 是连接状态与被后端拒绝的行。 */
  direction: "send" | "receive" | "system";
  payload: string;
  isBinary: boolean;
  /** 系统行的着色：错误红、其余灰。 */
  tone?: "error" | "muted";
  /** 系统行的词典键与插值变量：存结构而不是成品文案，切语言后按当前语言显示。 */
  i18n?: { key: string; vars?: Record<string, string | number> };
}

/** 日志行的显示文案：系统行按当前语言现取，收发消息是用户数据。 */
export function logText(entry: SocketLogEntry): string {
  return entry.i18n === undefined ? entry.payload : t(entry.i18n.key, entry.i18n.vars);
}

export interface SocketSession {
  connId: string | null;
  kind: RequestKind;
  /** 建立连接时编辑器里的地址，断开时凭它判断编辑器是否已经换了一条请求。 */
  url: string;
  status: SocketStatus;
  log: SocketLogEntry[];
  handshakeHeaders: KeyValue[] | null;
  protocol: string;
  stoppedByUser: boolean;
}

export const SOCKET_LOG_LIMIT = 500;

const session: SocketSession = {
  connId: null,
  kind: "http",
  url: "",
  status: "idle",
  log: [],
  handshakeHeaders: null,
  protocol: "",
  stoppedByUser: false,
};

type Listener = () => void;
const listeners = new Set<Listener>();

export function getSocketSession(): SocketSession {
  return session;
}

export function subscribeSocket(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function notifySocket(): void {
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

function systemLine(payload: string, tone?: "error" | "muted"): SocketLogEntry {
  return { id: newId(), at: Date.now(), direction: "system", payload, isBinary: false, tone };
}

// 系统行的固定文案（连接状态等）走词典：payload 留空，显示时按当前语言取。
function systemMessage(
  key: string,
  vars?: Record<string, string | number>,
  tone?: "error" | "muted",
): SocketLogEntry {
  return { id: newId(), at: Date.now(), direction: "system", payload: "", isBinary: false, tone, i18n: { key, vars } };
}

function appendLog(entry: SocketLogEntry): void {
  session.log.push(entry);
  if (session.log.length > SOCKET_LOG_LIMIT) session.log.splice(0, session.log.length - SOCKET_LOG_LIMIT);
  notifySocket();
}

export function clearSocketLog(): void {
  if (session.log.length === 0) return;
  session.log = [];
  notifySocket();
}

// urlProblem reports why url cannot be dialed as kind, or null when it can.
function urlProblem(kind: RequestKind, url: string): string | null {
  const target = url.trim();
  if (kind === "websocket") {
    if (target === "" || !/^wss?:\/\//i.test(target)) return t("socket.urlNeedsWs");
    try {
      const parsed = new URL(target);
      if (parsed.host === "") return t("socket.urlNeedsWs");
    } catch {
      return t("socket.urlInvalidWs");
    }
    return null;
  }
  if (kind === "tcp") {
    const marker = target.lastIndexOf(":");
    if (marker <= 0) return t("socket.urlNeedsTcp");
    const host = target.slice(0, marker).trim();
    const port = Number(target.slice(marker + 1));
    if (host === "" || !Number.isInteger(port) || port < 1 || port > 65535) {
      return t("socket.urlNeedsTcp");
    }
    return null;
  }
  return t("socket.httpNoConnect");
}

// connectSocket dials the connection the editor describes. Connect stays for
// the life of the connection, so it is not awaited: events arrive on the
// channel and the session shows them.
export function connectSocket(): void {
  const request = getRequest();
  if (session.status === "connecting" || session.status === "open") return;
  const problem = urlProblem(request.kind, request.url);
  if (problem !== null) {
    updateResponse({ error: problem });
    return;
  }

  const kind: RequestKind = request.kind;
  const params: SocketConnectParams = {
    id: newId(),
    kind,
    url: request.url.trim(),
    params: wireValues(request.params),
    headers: wireValues(request.headers),
    protocol: request.socketProtocol.trim(),
  };

  session.connId = params.id;
  session.kind = kind;
  session.url = params.url;
  session.status = "connecting";
  session.log = [];
  session.handshakeHeaders = null;
  session.protocol = "";
  session.stoppedByUser = false;
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
  notifySocket();

  const channel = new Channel<SocketEvent>((event) => handleEvent(params, event));
  channel.onclose = () => {
    if (session.connId !== params.id) return;
    // Connect 返回即通道关闭；正常收尾已由 close/error 事件做完，这里只兜住
    // 没收到任何事件就结束的连接。
    if (session.status === "connecting" || session.status === "open") {
      session.status = "closed";
      appendLog(systemMessage("socket.closedByChannel", undefined, "muted"));
    }
  };
  void SocketService.connect(params, channel).catch((error: unknown) => {
    if (session.connId !== params.id) return;
    if (session.stoppedByUser) return;
    const message = error instanceof Error ? error.message : String(error);
    // 握手失败会先推一条 error 事件，Connect 的拒绝不再重复记一行。
    const reported = session.status === "closed";
    session.status = "closed";
    updateResponse({ error: message });
    if (!reported) appendLog(systemLine(message, "error"));
  });
}

function handleEvent(params: SocketConnectParams, event: SocketEvent): void {
  if (session.connId !== params.id) return;
  switch (event.type) {
    case "open": {
      session.status = "open";
      session.handshakeHeaders = event.headers ?? [];
      session.protocol = event.protocol ?? "";
      const label = params.kind === "websocket" ? "WebSocket" : "TCP";
      const protocol = session.protocol === "" ? "" : t("socket.protocolSuffix", { protocol: session.protocol });
      appendLog(systemMessage("socket.connected", { kind: label, url: params.url, protocol }, "muted"));
      return;
    }
    case "message": {
      appendLog({
        id: newId(),
        at: event.at > 0 ? event.at : Date.now(),
        direction: event.direction === "send" ? "send" : "receive",
        payload: event.payload ?? "",
        isBinary: event.isBinary === true,
      });
      return;
    }
    case "error": {
      session.status = "closed";
      const reason = event.reason === undefined || event.reason === "" ? t("socket.connectError") : event.reason;
      const message =
        event.code !== undefined && event.code > 0
          ? t("socket.errorWithCode", { reason, code: event.code })
          : reason;
      updateResponse({ error: message });
      appendLog(systemLine(message, "error"));
      return;
    }
    case "close": {
      session.status = "closed";
      appendLog(systemLine(closeText(event), "muted"));
      return;
    }
    default:
      return;
  }
}

function closeText(event: SocketEvent): string {
  const details: string[] = [];
  if (event.code !== undefined && event.code > 0) details.push(t("socket.closeCode", { code: event.code }));
  if (event.reason !== undefined && event.reason !== "") details.push(event.reason);
  return details.length === 0
    ? t("socket.closedByPeer")
    : t("socket.closedWithDetails", { details: details.join(t("common.listSeparator")) });
}

// sendSocketMessage writes one message on the live connection; the log line
// comes back from the backend as a direction:"send" event, so it is not faked
// here.
export function sendSocketMessage(message: { payload: string; isBinary: boolean }): void {
  const connId = session.connId;
  if (connId === null || session.status !== "open") {
    updateResponse({ error: t("socket.notConnected") });
    return;
  }
  void SocketService.send({ id: connId, payload: message.payload, isBinary: message.isBinary }).catch(
    (error: unknown) => {
      const text = error instanceof Error ? error.message : String(error);
      updateResponse({ error: text });
      if (session.connId === connId) appendLog(systemLine(text, "error"));
    },
  );
}

export function disconnectSocket(): void {
  const connId = session.connId;
  if (session.status === "idle" || session.status === "closed") {
    session.connId = null;
    return;
  }
  session.stoppedByUser = true;
  session.connId = null;
  session.status = "closed";
  appendLog(systemMessage("socket.disconnected", undefined, "muted"));
  if (connId === null) return;
  void SocketService.disconnect(connId).catch(() => {
    // 连接已经没了：Disconnect 对未知 id 本就不是错误。
  });
}

// stopSocketIfActive closes a live connection before the editor switches kind
// or loads another entry.
export function stopSocketIfActive(): void {
  if (session.status === "connecting" || session.status === "open") disconnectSocket();
}
