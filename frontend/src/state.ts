import type { Channel } from "mygo-runtime";
import type { AssertionResult, AssertionRule } from "./assert";
import { ASSERTION_TYPE_IDS, newRuleId } from "./assert";
import type {
  APIRequest,
  APIResponse,
  FormField,
  IdentityInfo,
  KeyValue,
  SavedExample,
  SavedFolder,
  SavedMessage,
  SavedRequest,
  SavedSocketEvent,
  ScriptLog,
  ScriptTest,
  SSEEvent,
  SSEMessage,
  SSEOpen,
  WorkspaceFile,
} from "./mygo";
import { WorkspaceService } from "./mygo";

export const STORAGE_KEY = "netdream.request";
const SAVE_DELAY_MS = 300;

export const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;
export type Method = (typeof METHODS)[number];

/**
 * 编辑器类型：HTTP 请求、WebSocket 连接、TCP 连接、Socket.IO 连接、gRPC 调用、
 * Composer 自动化编排、Markdown 文档。
 */
export const REQUEST_KINDS = [
  "http",
  "websocket",
  "tcp",
  "socketio",
  "grpc",
  "composer",
  "markdown",
] as const;
export type RequestKind = (typeof REQUEST_KINDS)[number];

/** WS/Socket.IO 消息模板的类型（SavedMessage.msgType）。 */
export const MESSAGE_TYPES = ["text", "json", "xml", "html", "binary"] as const;
export type MessageType = (typeof MESSAGE_TYPES)[number];

export const BODY_TYPES = ["none", "formData", "urlencoded", "raw", "binary"] as const;
export type BodyType = (typeof BODY_TYPES)[number];

export const RAW_TYPES = ["json", "text", "xml", "html"] as const;
export type RawType = (typeof RAW_TYPES)[number];

export const AUTH_TYPES = ["none", "basic", "bearer"] as const;
export type AuthType = (typeof AUTH_TYPES)[number];

export interface KVRow {
  key: string;
  value: string;
  description: string;
  enabled: boolean;
}

/** form-data 字段类型：文本值，或把所选文件作为文件 part 上传。 */
export const FORM_FIELD_KINDS = ["text", "file"] as const;
export type FormFieldKind = (typeof FORM_FIELD_KINDS)[number];

export interface FormRow extends KVRow {
  contentType: string;
  kind: FormFieldKind;
  /** kind === "file" 时上传的文件绝对路径。 */
  filePath: string;
}

export interface AuthState {
  type: AuthType;
  username: string;
  password: string;
  token: string;
}

export interface RequestState {
  kind: RequestKind;
  method: Method;
  url: string;
  params: KVRow[];
  headers: KVRow[];
  pathParams: KVRow[];
  bodyType: BodyType;
  formData: FormRow[];
  urlEncoded: KVRow[];
  raw: string;
  rawType: RawType;
  binaryPath: string;
  auth: AuthState;
  cookies: KVRow[];
  timeout: number;
  http2: boolean;
  assertions: AssertionRule[];
  /** WS/TCP/Socket.IO 的已存消息模板。 */
  messages: SavedMessage[];
  /** WS 子协议（Sec-WebSocket-Protocol），逗号分隔。 */
  socketProtocol: string;
  /** Socket.IO 事件定义（发送消息页的事件列表）。 */
  socketEvents: SavedSocketEvent[];
  /** Socket.IO 协议版本："4"（默认）| "3"。 */
  socketIOVersion: string;
  /** Socket.IO 的 Engine.IO 握手路径；空 = /socket.io/。 */
  socketIOPath: string;
  /** gRPC 选中的方法，形如 "pkg.Service/Method"（后端点号写法也认）；空 = 未选。 */
  grpcMethod: string;
  /** gRPC 明文传输（不使用 TLS）；默认 true。 */
  grpcPlaintext: boolean;
  /** gRPC 跳过 TLS 证书校验（自签证书调试用）。 */
  grpcTlsSkipVerify: boolean;
  /** gRPC 的 TLS ServerName（SNI）；空 = 取目标主机名。 */
  grpcServerName: string;
  /** gRPC 本地 .proto 文件绝对路径；非空即按本地描述解析，不走服务器反射。 */
  grpcProtoFiles: string[];
  /** gRPC proto 的 import 搜索目录。 */
  grpcImportPaths: string[];
  /**
   * 预执行脚本；空 = 不执行。HTTP 在发送前运行，连接类 kind（WS/TCP/Socket.IO）
   * 里是「预连接脚本」：建立连接前运行。
   */
  preScript: string;
  /** 后执行脚本；空 = 不执行（仅 HTTP）。 */
  postScript: string;
  /** 发送脚本；空 = 不执行（仅 WS/TCP/Socket.IO：每条发出的消息先过它）。 */
  sendScript: string;
  /** 接收脚本；空 = 不执行（仅 WS/TCP/Socket.IO：每条收到的消息先过它）。 */
  receiveScript: string;
  /** kind="markdown" 的文档内容；其余 kind 恒为空串。 */
  markdown: string;
  /**
   * kind="composer" 的编排脚本（JS，goja 运行）：串行调用工作区里的请求、断言、
   * 写变量；其余 kind 恒为空串。
   */
  composerScript: string;
  /** 响应示例（响应快照）；列表结构为将来扩展预留，目前只保存一条。 */
  examples: SavedExample[];
}

export interface ResponseState {
  sending: boolean;
  streaming: boolean;
  open: SSEOpen | null;
  events: SSEEvent[];
  /** 事件超过保留上限时被丢掉的条数；0 = 一条没丢。 */
  droppedEvents: number;
  stopped: boolean;
  response: APIResponse | null;
  error: string | null;
  results: AssertionResult[];
  /** 脚本产物（不持久化）。 */
  scriptLogs: ScriptLog[];
  scriptTests: ScriptTest[];
  preScriptError: string | null;
  postScriptError: string | null;
}

export interface WorkspaceState {
  name: string;
  path: string | null;
  entries: SavedRequest[];
  /** 请求树的文件夹；没有归属的请求平铺在根目录（folders 里查不到它的 id）。 */
  folders: SavedFolder[];
  currentId: string | null;
  /**
   * 当前 .band 是否以加密信封保存（v1 与明文 v2 恒为 false）。保存弹窗据此默认
   * 勾选「加密保存」并注明可以沿用原收件人。
   */
  encrypted: boolean;
}

export function emptyRow(): KVRow {
  return { key: "", value: "", description: "", enabled: true };
}

export function emptyFormRow(): FormRow {
  return {
    key: "",
    value: "",
    contentType: "",
    description: "",
    enabled: true,
    kind: "text",
    filePath: "",
  };
}

function defaultRequest(kind: RequestKind = "http"): RequestState {
  return {
    kind,
    method: "GET",
    url: "",
    params: [emptyRow()],
    headers: [emptyRow()],
    pathParams: [],
    bodyType: "none",
    formData: [emptyFormRow()],
    urlEncoded: [emptyRow()],
    raw: "",
    rawType: "json",
    binaryPath: "",
    auth: { type: "none", username: "", password: "", token: "" },
    cookies: [emptyRow()],
    timeout: 0,
    http2: false,
    assertions: [],
    messages: [],
    socketProtocol: "",
    socketEvents: [],
    socketIOVersion: "",
    socketIOPath: "",
    grpcMethod: "",
    grpcPlaintext: true,
    grpcTlsSkipVerify: false,
    grpcServerName: "",
    grpcProtoFiles: [],
    grpcImportPaths: [],
    preScript: "",
    postScript: "",
    sendScript: "",
    receiveScript: "",
    markdown: "",
    composerScript: "",
    examples: [],
  };
}

let request: RequestState = defaultRequest();

const runtime: ResponseState = {
  sending: false,
  streaming: false,
  open: null,
  events: [],
  droppedEvents: 0,
  stopped: false,
  response: null,
  error: null,
  results: [],
  scriptLogs: [],
  scriptTests: [],
  preScriptError: null,
  postScriptError: null,
};

type Listener = () => void;
const listeners = new Set<Listener>();

export function getRequest(): RequestState {
  return request;
}

export function getResponse(): ResponseState {
  return runtime;
}

export function subscribe(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function notify(): void {
  for (const listener of listeners) listener();
}

export function updateRequest(mutate?: (state: RequestState) => void): void {
  mutate?.(request);
  syncPathParams();
  // 改的是已入册条目的字段（URL/头/体/Markdown/响应示例…）：这就是在工作区数据上
  // 改东西，保存守卫与在途保存都要看得见。还没入册的新请求保持原语义——
  // 执行过一次、进了列表之后才开始算改动。
  if (currentEntryRegistered()) markWorkspaceDirty();
  scheduleSave();
  notify();
}

// 当前编辑器对应的是不是工作区里已存在的条目：新请求（currentId 为 null）与
// 已入册后又派生（尚未 upsert）的情况都算「还没入册」。
function currentEntryRegistered(): boolean {
  const id = workspace.currentId;
  return id !== null && workspace.entries.some((entry) => entry.id === id);
}

// form-data 的文件行路径由拖拽落点路由到这里（见 main.ts 的 onFileDrop）。
// 换掉数组让表格重画，文件控件随之显示新的文件名。
export function setFormFilePath(index: number, path: string): void {
  if (path === "") return;
  updateRequest((state) => {
    const row = state.formData[index];
    if (row === undefined || row.kind !== "file") return;
    state.formData = state.formData.map((item, position) =>
      position === index ? { ...item, filePath: path } : item,
    );
  });
}

export function updateResponse(patch: Partial<ResponseState>): void {
  // 先把排队的事件落地：清空、结束流、换请求这些补丁之后，界面看到的条数
  // 才是完整的（否则最后一帧的事件会被下一次 updateResponse 覆盖掉）。
  applyPendingEvents();
  Object.assign(runtime, patch);
  if (patch.events !== undefined) {
    // 换了一组事件（开流、重置、切请求）：字节预算与丢弃计数跟着重来。
    eventsBytes = dataBytes(patch.events);
    if (patch.events.length === 0) runtime.droppedEvents = 0;
  }
  notify();
}

/**
 * 把当前响应存为响应示例（新示例覆盖旧的一条——列表结构为将来多示例扩展预留）。
 * 示例是编辑器状态的一部分，随保存/发送快照进 .band；它走 updateRequest，所以和
 * 请求字段一样：改的是已入册条目就算工作区改动，未入册的新请求仍不置脏。
 */
export function saveCurrentExample(): void {
  const response = getResponse().response;
  if (response === null) return;
  const example: SavedExample = {
    id: newId(),
    name: "",
    savedAt: Date.now(),
    response,
  };
  updateRequest((state) => {
    state.examples = [example];
  });
}

/** 删除一条响应示例。 */
export function deleteExample(id: string): void {
  updateRequest((state) => {
    state.examples = state.examples.filter((example) => example.id !== id);
  });
}

let streamChannel: Channel<SSEMessage> | null = null;

export function setStreamChannel(channel: Channel<SSEMessage> | null): void {
  streamChannel = channel;
}

// stopStream closes the channel the page handed to Send: the Go side cancels
// its context, Send rejects, and main.ts swallows that rejection.
export function stopStream(): void {
  const channel = streamChannel;
  if (channel === null) return;
  streamChannel = null;
  updateResponse({ stopped: true, streaming: false });
  channel.close();
}

// 流式事件在内存里只留最近的这么多：一条长流可以推几万条，界面撑不住。
const MAX_STREAM_EVENTS = 5000;
// Data 总量上限：条数相同的事件体积可能相差几个量级，单独再卡一道。
const MAX_STREAM_DATA_BYTES = 32 << 20;

// 排队中的事件：一帧内到的事件合成一次通知。逐条 notify 会把渲染压在同一帧
// 里排队几十次，而每条事件本身只该算一次 DOM 追加。
let pendingEvents: SSEEvent[] = [];
let pendingBytes = 0;
// runtime.events 里 Data 的总字节数，淘汰时靠它判断要不要继续丢。
let eventsBytes = 0;
// 每次事件落地递增：条数到达上限后长度不再变化，界面靠它知道有新事件。
let streamRevision = 0;
let flushHandle: number | null = null;
let flushByFrame = false;

/** 事件落地了多少次；界面用它判断「有新事件」，条数到顶时也成立。 */
export function getStreamRevision(): number {
  return streamRevision;
}

function dataBytes(events: SSEEvent[]): number {
  let total = 0;
  for (const event of events) total += event.data.length;
  return total;
}

function cancelFlush(): void {
  if (flushHandle === null) return;
  // rAF 与定时器的编号是两套，不能拿一个去取消另一个。
  if (flushByFrame) {
    if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(flushHandle);
  } else {
    window.clearTimeout(flushHandle);
  }
  flushHandle = null;
}

function scheduleFlush(): void {
  if (flushHandle !== null) return;
  const run = () => {
    flushHandle = null;
    flushStreamEvents();
  };
  // 有 rAF 就按帧合；只有完全没有 rAF 的环境才退到 16ms 定时器。后台标签页里
  // rAF 会暂停，事件就留在队列里等下一帧——队列自己有上限，而且结束流、停止、
  // 任何 updateResponse 都会立刻把它们落地，所以这里不另加定时器。
  if (typeof requestAnimationFrame === "function") {
    flushByFrame = true;
    flushHandle = requestAnimationFrame(run);
  } else {
    flushByFrame = false;
    flushHandle = window.setTimeout(run, 16);
  }
}

// applyPendingEvents 把排队的事件并进 runtime.events 并按上限淘汰，返回是否
// 有变化；它不通知订阅者，好让 updateResponse 把补丁和这批事件一起通知一次。
function applyPendingEvents(): boolean {
  cancelFlush();
  if (pendingEvents.length === 0) return false;
  const incoming = pendingEvents;
  pendingEvents = [];
  pendingBytes = 0;
  const events = runtime.events;
  for (const event of incoming) {
    events.push(event);
    eventsBytes += event.data.length;
  }
  trimEvents(events);
  streamRevision += 1;
  return true;
}

// trimEvents 从最旧的开始丢，直到条数与字节数都回到上限以内。
function trimEvents(events: SSEEvent[]): void {
  let drop = 0;
  while (
    drop < events.length &&
    (events.length - drop > MAX_STREAM_EVENTS || eventsBytes > MAX_STREAM_DATA_BYTES)
  ) {
    const event = events[drop];
    if (event !== undefined) eventsBytes -= event.data.length;
    drop += 1;
  }
  if (drop === 0) return;
  events.splice(0, drop);
  runtime.droppedEvents += drop;
}

/** 立刻把排队的事件落地并通知一次；合帧到期与 updateResponse 都走它。 */
function flushStreamEvents(): void {
  if (applyPendingEvents()) notify();
}

// Events are pushed into the live array: a long stream must not copy it per event.
// 它们先进队列，同一帧里的多条并成一次通知与一次 DOM 追加。
export function pushStreamEvent(event: SSEEvent): void {
  pendingEvents.push(event);
  pendingBytes += event.data.length;
  // 后台标签页里 rAF 不跑，排队的事件同样要有上限，否则长流会把内存堆满。
  while (pendingEvents.length > MAX_STREAM_EVENTS || pendingBytes > MAX_STREAM_DATA_BYTES) {
    const dropped = pendingEvents.shift();
    if (dropped === undefined) break;
    pendingBytes -= dropped.data.length;
    runtime.droppedEvents += 1;
  }
  scheduleFlush();
}

let saveTimer: number | undefined;

function scheduleSave(): void {
  if (saveTimer !== undefined) window.clearTimeout(saveTimer);
  saveTimer = window.setTimeout(() => {
    saveTimer = undefined;
    save();
  }, SAVE_DELAY_MS);
}

function save(): void {
  try {
    // 响应示例里的响应体可能有 10MiB，只属于工作区里的请求条目：不进
    // localStorage 预览快照，否则一条示例就可能撑满配额、把快照写坏。
    // 读的时候仍按旧格式解析（老快照里的 examples 照样认得）。
    const { examples: _examples, ...snapshot } = request;
    localStorage.setItem(STORAGE_KEY, JSON.stringify(snapshot));
  } catch {
    // Storage can be disabled or full; the page keeps working without it.
  }
}

/* ---- 工作区脏状态 ---- */

// 变量随工作区落盘，所以改变量也是改工作区；保存成功时由 main.ts 清脏。
// Go 侧凭这个状态决定用户点窗口 X 时是直接关窗还是先问页面。
let workspaceDirty = false;

// 工作区版本号：每次改动与每次清脏都推进。保存开始时记下它，写完时只有
// 版本没变才清脏——保存期间又改了东西，工作区仍然算脏。
let workspaceRevision = 0;

export function isWorkspaceDirty(): boolean {
  return workspaceDirty;
}

/** 当前工作区版本号：保存流程凭它判断这期间有没有新的改动。 */
export function getWorkspaceRevision(): number {
  return workspaceRevision;
}

export function setWorkspaceDirty(dirty: boolean): void {
  if (workspaceDirty === dirty) return;
  workspaceDirty = dirty;
  void WorkspaceService.setDirty(dirty).catch(() => {
    // 推给 Go 只影响关窗时的询问，推送失败不影响页面内的状态。
  });
}

/**
 * 记一次改动：推进版本号并置脏。
 * 已经脏的时候 setWorkspaceDirty(true) 是空操作，版本号仍要涨，
 * 否则保存期间的新改动会被旧的保存当成「没变」而清脏。
 */
function markWorkspaceDirty(): void {
  workspaceRevision += 1;
  setWorkspaceDirty(true);
}

/**
 * 载入/新建工作区后的清脏：版本号同样推进，让之前发起的保存在完成时
 * 认出「工作区已经换了」而不去动新工作区的脏状态。
 */
function clearWorkspaceDirty(): void {
  workspaceRevision += 1;
  setWorkspaceDirty(false);
}

/* ---- 变量（工作区的一部分，随 .band 落盘） ---- */

let vars: Record<string, string> = {};
const varsListeners = new Set<Listener>();

export function getVars(): Record<string, string> {
  return vars;
}

export function setVars(next: Record<string, string>): void {
  const changed = !sameVars(vars, next);
  replaceVars(next);
  if (changed) markWorkspaceDirty();
}

export function updateVars(mutate: (next: Record<string, string>) => void): void {
  const next = { ...vars };
  mutate(next);
  setVars(next);
}

export function subscribeVars(listener: Listener): () => void {
  varsListeners.add(listener);
  return () => {
    varsListeners.delete(listener);
  };
}

/* ---- 本机身份缓存 ---- */

// 启动时（以及首次运行窗建好身份后）取一次身份存在这里：设置页打开时要立刻有
// 东西可显示，不必先等一次跨进程往返。真值仍以后端为准——设置页每次打开照旧调
// IdentityService.get() 刷新。
let cachedIdentity: IdentityInfo | null = null;

/** 最近一次取到的本机身份；还没取过或还没建立时为 null。 */
export function getCachedIdentity(): IdentityInfo | null {
  return cachedIdentity;
}

/** 记下最新取到的本机身份；身份没建立时传 null。 */
export function setCachedIdentity(identity: IdentityInfo | null): void {
  cachedIdentity = identity;
}

// 换掉整份变量而不置脏：载入工作区时用，变量属于那个文件，替换本身不是改动。
function replaceVars(next: Record<string, string>): void {
  vars = { ...next };
  for (const listener of varsListeners) listener();
}

function sameVars(left: Record<string, string>, right: Record<string, string>): boolean {
  const keys = Object.keys(left);
  if (keys.length !== Object.keys(right).length) return false;
  return keys.every((key) => left[key] === right[key]);
}

function hydrateVars(value: unknown): Record<string, string> {
  const next: Record<string, string> = {};
  if (!isRecord(value)) return next;
  for (const [key, item] of Object.entries(value)) {
    if (typeof item === "string") next[key] = item;
  }
  return next;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function text(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function flag(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function wholeNumber(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.floor(value) : fallback;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  if (typeof value === "string" && (allowed as readonly string[]).includes(value)) return value as T;
  return fallback;
}

/** 字符串列表（proto 文件、import 目录）：非数组给空表，非字符串项丢掉。 */
function textList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const items: string[] = [];
  for (const item of value) {
    if (typeof item === "string") items.push(item);
  }
  return items;
}

function hydrateRows(value: unknown, createBlank: () => KVRow): KVRow[] {
  if (!Array.isArray(value)) return [createBlank()];
  const rows: KVRow[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    rows.push({
      key: text(item.key),
      value: text(item.value),
      description: text(item.description),
      enabled: flag(item.enabled, true),
    });
  }
  return rows;
}

function hydrateFormRows(value: unknown): FormRow[] {
  if (!Array.isArray(value)) return [emptyFormRow()];
  const rows: FormRow[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    rows.push({
      key: text(item.key),
      value: text(item.value),
      contentType: text(item.contentType),
      description: text(item.description),
      enabled: flag(item.enabled, true),
      // 旧快照没有这两个字段：一律回落到文本行。
      kind: oneOf(item.kind, FORM_FIELD_KINDS, "text"),
      filePath: text(item.filePath),
    });
  }
  return rows;
}

function hydrateAssertions(value: unknown): AssertionRule[] {
  if (!Array.isArray(value)) return [];
  const rules: AssertionRule[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    rules.push({
      id: text(item.id) || newRuleId(),
      enabled: flag(item.enabled, true),
      type: oneOf(item.type, ASSERTION_TYPE_IDS, "statusEquals"),
      value: text(item.value),
    });
  }
  return rules;
}

function requestKind(value: unknown): RequestKind {
  return oneOf(value, REQUEST_KINDS, "http");
}

function hydrateMessages(value: unknown): SavedMessage[] {
  if (!Array.isArray(value)) return [];
  const messages: SavedMessage[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    messages.push({
      id: text(item.id) || newId(),
      name: text(item.name),
      msgType: oneOf(item.msgType, MESSAGE_TYPES, "text"),
      payload: text(item.payload),
      event: text(item.event),
    });
  }
  return messages;
}

function hydrateSocketEvents(value: unknown): SavedSocketEvent[] {
  if (!Array.isArray(value)) return [];
  const events: SavedSocketEvent[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    events.push({
      id: text(item.id) || newId(),
      name: text(item.name),
      enabled: flag(item.enabled, true),
      description: text(item.description),
    });
  }
  return events;
}

function hydrateExamples(value: unknown): SavedExample[] {
  if (!Array.isArray(value)) return [];
  const examples: SavedExample[] = [];
  for (const item of value) {
    if (!isRecord(item) || !isRecord(item.response)) continue;
    examples.push({
      id: text(item.id) || newId(),
      name: text(item.name),
      savedAt: typeof item.savedAt === "number" && Number.isFinite(item.savedAt) ? item.savedAt : 0,
      response: item.response as unknown as SavedExample["response"],
    });
  }
  return examples;
}

function hydrate(source: Record<string, unknown>): RequestState {
  const auth = isRecord(source.auth) ? source.auth : {};
  return {
    kind: requestKind(source.kind),
    method: oneOf(source.method, METHODS, "GET"),
    url: text(source.url),
    params: hydrateRows(source.params, emptyRow),
    headers: hydrateRows(source.headers, emptyRow),
    pathParams: hydrateRows(source.pathParams, emptyRow),
    bodyType: oneOf(source.bodyType, BODY_TYPES, "none"),
    formData: hydrateFormRows(source.formData),
    urlEncoded: hydrateRows(source.urlEncoded, emptyRow),
    raw: text(source.raw),
    rawType: oneOf(source.rawType, RAW_TYPES, "json"),
    binaryPath: text(source.binaryPath),
    auth: {
      type: oneOf(auth.type, AUTH_TYPES, "none"),
      username: text(auth.username),
      password: text(auth.password),
      token: text(auth.token),
    },
    cookies: hydrateRows(source.cookies, emptyRow),
    timeout: wholeNumber(source.timeout),
    http2: flag(source.http2, false),
    assertions: hydrateAssertions(source.assertions),
    messages: hydrateMessages(source.messages),
    socketProtocol: text(source.socketProtocol),
    socketEvents: hydrateSocketEvents(source.socketEvents),
    socketIOVersion: text(source.socketIOVersion),
    socketIOPath: text(source.socketIOPath),
    grpcMethod: text(source.grpcMethod),
    grpcPlaintext: flag(source.grpcPlaintext, true),
    grpcTlsSkipVerify: flag(source.grpcTlsSkipVerify, false),
    grpcServerName: text(source.grpcServerName),
    grpcProtoFiles: textList(source.grpcProtoFiles),
    grpcImportPaths: textList(source.grpcImportPaths),
    preScript: text(source.preScript),
    postScript: text(source.postScript),
    sendScript: text(source.sendScript),
    receiveScript: text(source.receiveScript),
    markdown: text(source.markdown),
    composerScript: text(source.composerScript),
    examples: hydrateExamples(source.examples),
  };
}

export function applyRequest(next: RequestState): void {
  request = next;
  syncedURL = null;
  syncPathParams();
  scheduleSave();
  notify();
}

export function loadSavedRequest(): void {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORAGE_KEY);
  } catch {
    raw = null;
  }
  let next = defaultRequest();
  if (raw) {
    let parsed: unknown = null;
    try {
      parsed = JSON.parse(raw);
    } catch {
      parsed = null;
    }
    if (isRecord(parsed)) next = hydrate(parsed);
  }
  applyRequest(next);
}

export function pathPlaceholders(url: string): string[] {
  const names: string[] = [];
  for (const match of url.matchAll(/\{([^{}]+)\}/g)) {
    const name = match[1];
    if (name !== undefined && !names.includes(name)) names.push(name);
  }
  return names;
}

export function resolveURL(url: string, rows: readonly KVRow[]): string {
  let resolved = url;
  for (const row of rows) {
    if (!row.enabled || row.key === "") continue;
    resolved = resolved.replaceAll(`{${row.key}}`, encodeURIComponent(row.value));
  }
  return resolved;
}

let syncedURL: string | null = null;

function syncPathParams(): void {
  if (syncedURL === request.url) return;
  syncedURL = request.url;

  const names = pathPlaceholders(request.url);
  const current = request.pathParams;
  const unchanged =
    names.length === current.length && names.every((name, index) => current[index]?.key === name);
  if (unchanged) return;

  const previous = new Map<string, KVRow>();
  for (const row of current) previous.set(row.key, row);
  request.pathParams = names.map((name) => {
    const row = previous.get(name);
    return row ? { ...row, key: name } : { key: name, value: "", description: "", enabled: true };
  });
}

export function wireValues(rows: readonly KVRow[]): KeyValue[] {
  const values: KeyValue[] = [];
  for (const row of rows) {
    if (!row.enabled || row.key.trim() === "") continue;
    values.push({ key: row.key, value: row.value });
  }
  return values;
}

export function buildRequest(): APIRequest {
  const state = request;
  return {
    method: state.method,
    url: resolveURL(state.url, state.pathParams),
    params: wireValues(state.params),
    headers: wireValues(state.headers),
    bodyType: state.bodyType,
    formData: state.formData
      .filter((row) => row.enabled && row.key.trim() !== "")
      .map((row) => ({
        key: row.key,
        value: row.value,
        contentType: row.contentType,
        description: row.description,
        kind: row.kind,
        filePath: row.filePath,
      })),
    urlEncoded: wireValues(state.urlEncoded),
    raw: state.raw,
    rawType: state.rawType,
    binaryPath: state.binaryPath,
    auth: {
      type: state.auth.type,
      username: state.auth.username,
      password: state.auth.password,
      token: state.auth.token,
    },
    cookies: wireValues(state.cookies),
    timeout: state.timeout,
    http2: state.http2,
    preScript: state.preScript,
    postScript: state.postScript,
    vars: { ...vars },
  };
}

/* ---- 工作区 ---- */

// 未命名工作区在数据里存空串：显示名由界面按当前语言给（侧栏与保存对话框），
// 存进 .band 的也还是空串，换语言时不会把旧语言的默认名写成用户数据。
const workspace: WorkspaceState = {
  name: "",
  path: null,
  entries: [],
  folders: [],
  currentId: null,
  encrypted: false,
};

export function getWorkspace(): WorkspaceState {
  return workspace;
}

function rowsFromValues(values: readonly KeyValue[]): KVRow[] {
  return values.map((value) => ({ key: value.key, value: value.value, description: "", enabled: true }));
}

function formRowsFromFields(fields: readonly FormField[]): FormRow[] {
  return fields.map((field) => ({
    key: field.key,
    value: field.value,
    contentType: field.contentType,
    description: field.description,
    enabled: true,
    // 旧 .band 的字段没有 kind：回落到文本行。
    kind: oneOf(field.kind, FORM_FIELD_KINDS, "text"),
    filePath: text(field.filePath),
  }));
}

function requestFromSaved(saved: SavedRequest): RequestState {
  const auth: Record<string, unknown> = isRecord(saved.auth) ? saved.auth : {};
  return {
    kind: requestKind(saved.kind),
    method: oneOf(saved.method, METHODS, "GET"),
    url: text(saved.url),
    params: rowsFromValues(saved.params),
    headers: rowsFromValues(saved.headers),
    pathParams: [],
    bodyType: oneOf(saved.bodyType, BODY_TYPES, "none"),
    formData: formRowsFromFields(saved.formData),
    urlEncoded: rowsFromValues(saved.urlEncoded),
    raw: text(saved.raw),
    rawType: oneOf(saved.rawType, RAW_TYPES, "json"),
    binaryPath: text(saved.binaryPath),
    auth: {
      type: oneOf(auth.type, AUTH_TYPES, "none"),
      username: text(auth.username),
      password: text(auth.password),
      token: text(auth.token),
    },
    cookies: rowsFromValues(saved.cookies),
    timeout: wholeNumber(saved.timeout),
    http2: saved.http2 === true,
    assertions: saved.assertions.map((rule) => ({
      id: text(rule.id) || newRuleId(),
      enabled: flag(rule.enabled, true),
      type: oneOf(rule.type, ASSERTION_TYPE_IDS, "statusEquals"),
      value: text(rule.value),
    })),
    messages: hydrateMessages(saved.messages),
    socketProtocol: text(saved.socketProtocol),
    socketEvents: hydrateSocketEvents(saved.socketEvents),
    socketIOVersion: text(saved.socketIOVersion),
    socketIOPath: text(saved.socketIOPath),
    grpcMethod: text(saved.grpcMethod),
    // 旧 .band 没有 gRPC 字段：缺省按明文。
    grpcPlaintext: flag(saved.grpcPlaintext, true),
    grpcTlsSkipVerify: flag(saved.grpcTlsSkipVerify, false),
    grpcServerName: text(saved.grpcServerName),
    grpcProtoFiles: textList(saved.grpcProtoFiles),
    grpcImportPaths: textList(saved.grpcImportPaths),
    preScript: text(saved.preScript),
    postScript: text(saved.postScript),
    sendScript: text(saved.sendScript),
    receiveScript: text(saved.receiveScript),
    markdown: text(saved.markdown),
    composerScript: text(saved.composerScript),
    examples: saved.examples.map((example) => ({ ...example })),
  };
}

/* ---- 工作区脚本信任 ---- */

// 打开的 .band 里的预脚本能读变量、请求头与请求体，改写 URL 之后由后端附加认证
// 发出去——一份文件就能把用户的数据送走。信任是「本次载入」的内存状态：
// 本地恢复的会话与新建工作区默认可信，只有打开/拖入的 .band 要先问用户。
let scriptsTrusted = true;

// 工作区身份号：只在载入/新建时推进，普通编辑不推进（不能拿 revision 当身份，
// 敲一个字它就变了）。异步回调凭它判断「工作区还是不是当时那一份」。
let workspaceGeneration = 0;

/** 当前工作区的身份号：载入/新建工作区各推进一次。 */
export function getWorkspaceGeneration(): number {
  return workspaceGeneration;
}

/** 工作区里是否有任何条目带脚本（HTTP 前后脚本、连接类的三个脚本或 Composer 编排脚本）。 */
function workspaceHasScripts(): boolean {
  // 字段缺失一律按空串算：旧 .band 里没有连接脚本字段，手工改过的文件也可能缺。
  return workspace.entries.some((entry) =>
    [
      entry.preScript,
      entry.postScript,
      entry.sendScript,
      entry.receiveScript,
      entry.composerScript,
    ].some((value) => typeof value === "string" && value.trim() !== ""),
  );
}

/** 本次载入的工作区里，脚本能否直接运行。 */
export function scriptsTrustedInWorkspace(): boolean {
  return scriptsTrusted;
}

/** 用户确认信任本次载入的工作区脚本；重新打开 .band 或换工作区会重新问。 */
export function trustWorkspaceScripts(): void {
  scriptsTrusted = true;
}

export function loadWorkspace(file: WorkspaceFile): void {
  workspaceGeneration += 1;
  workspace.name = text(file.data.name);
  workspace.path = text(file.path) || null;
  workspace.entries = file.data.requests;
  // 请求树的归一化（孤儿请求、悬空 parentId、环）Go 侧读取时就做完了，这里直接信。
  workspace.folders = file.data.folders;
  // v1 与明文 v2 的 encrypted 恒为 false：保存弹窗据此决定是否默认勾选加密。
  workspace.encrypted = file.encrypted === true;
  // 变量属于工作区文件：整体换掉，且替换本身不算改动（清脏统一在后面做）。
  replaceVars(hydrateVars(file.data.vars));
  // 打开的 .band 里的脚本不可信：第一次要执行脚本前先问用户（见 trustWorkspaceScripts）。
  // 例外：v2 已验签且签名者已获信任（含「信任一次」）的工作区——身份已经密码学
  // 确认过，脚本直接放行。没有脚本的工作区照旧直接可用，不增加任何摩擦。
  scriptsTrusted = !workspaceHasScripts() || file.signatureTrusted === true;
  const first = workspace.entries[0];
  if (first === undefined) {
    workspace.currentId = null;
    clearWorkspaceDirty();
    notify();
    return;
  }
  workspace.currentId = first.id;
  applyRequest(requestFromSaved(first));
  clearWorkspaceDirty();
}

// 新建空工作区：条目与变量清空、回到一条空请求，且这里的一切都不算改动
// （脏状态最后统一清掉，否则换变量/换编辑器时的 notify 会把它又点起来）。
export function newWorkspace(): void {
  workspaceGeneration += 1;
  workspace.name = "";
  workspace.path = null;
  workspace.entries = [];
  workspace.folders = [];
  workspace.currentId = null;
  // 新工作区没有文件，也就没有信封：保存时默认不加密。
  workspace.encrypted = false;
  // 新工作区里的脚本都是用户自己写的：没有不可信来源，不必再问。
  scriptsTrusted = true;
  // 变量跟着工作区走，新工作区从空变量开始；走不置脏的安静路径。
  replaceVars({});
  // 空工作区没有条目可载入，编辑器回落到一条空 HTTP 请求（同 newRequest 语义）。
  applyRequest(defaultRequest());
  clearWorkspaceDirty();
  notify();
}

export function newRequest(kind: RequestKind = "http"): void {
  workspace.currentId = null;
  applyRequest(defaultRequest(kind));
}

/**
 * 文档类条目（Markdown / Composer）的零值快照：创建即入册，字段全部留空，
 * 名字留空由界面按当前语言给「未命名文档 / 未命名 Composer」。
 */
function blankDocumentEntry(kind: RequestKind): SavedRequest {
  return {
    id: newId(),
    name: "",
    kind,
    method: "",
    url: "",
    params: [],
    headers: [],
    bodyType: "none",
    formData: [],
    urlEncoded: [],
    raw: "",
    rawType: "json",
    binaryPath: "",
    auth: { type: "none", username: "", password: "", token: "" },
    cookies: [],
    timeout: 0,
    http2: false,
    assertions: [],
    messages: [],
    socketProtocol: "",
    socketEvents: [],
    socketIOVersion: "",
    socketIOPath: "",
    grpcMethod: "",
    grpcPlaintext: true,
    grpcTlsSkipVerify: false,
    grpcServerName: "",
    grpcProtoFiles: [],
    grpcImportPaths: [],
    preScript: "",
    postScript: "",
    sendScript: "",
    receiveScript: "",
    markdown: "",
    composerScript: "",
    examples: [],
  };
}

// 文档类条目没有「发送」动作，沿用请求的「执行后才收编」规则会无处可改名，
// 所以创建即入册：currentId 指向新条目、置脏，用户当场就能改名。
function addDocumentEntry(kind: RequestKind): void {
  const entry = blankDocumentEntry(kind);
  workspace.entries = [...workspace.entries, entry];
  workspace.currentId = entry.id;
  applyRequest(requestFromSaved(entry));
  markWorkspaceDirty();
}

/**
 * 新建一条未命名 Markdown 文档并入册：文档没有「发送」动作，创建即出现在
 * 工作区列表里（可当场双击或顶栏改名），编辑内容随保存工作区快照落盘。
 */
export function newMarkdownDocument(): void {
  addDocumentEntry("markdown");
}

/** 新建一条未命名 Composer 编排并入册（语义同 newMarkdownDocument）。 */
export function newComposerDocument(): void {
  addDocumentEntry("composer");
}

export function loadEntry(id: string): void {
  const entry = workspace.entries.find((item) => item.id === id);
  if (entry === undefined) return;
  workspace.currentId = id;
  applyRequest(requestFromSaved(entry));
}

export function deleteEntry(id: string): void {
  const next = workspace.entries.filter((item) => item.id !== id);
  if (next.length === workspace.entries.length) return;
  workspace.entries = next;
  // 请求没了，文件夹里的引用也要清掉；否则这里留的 id 在树上是一条空行。
  for (const folder of workspace.folders) {
    if (folder.requests.includes(id)) folder.requests = folder.requests.filter((item) => item !== id);
  }
  if (workspace.currentId === id) workspace.currentId = null;
  markWorkspaceDirty();
  notify();
}

export function renameEntry(id: string, name: string): void {
  const entry = workspace.entries.find((item) => item.id === id);
  if (entry === undefined || entry.name === name) return;
  entry.name = name;
  markWorkspaceDirty();
  notify();
}

/**
 * 给当前文档（Markdown 文档 / Composer 编排）改名。文档还没入册时（典型：应用
 * 重启后会话从本地快照恢复、内存工作区是空的）先收编成一条新条目再改，名字
 * 不再静默丢失。
 */
export function renameCurrentDocument(name: string): void {
  if (request.kind !== "markdown" && request.kind !== "composer") return;
  if (workspace.currentId === null) {
    upsertEntry(snapshotCurrent());
  }
  const id = workspace.currentId;
  if (id !== null) renameEntry(id, name);
}

export function renameWorkspace(name: string): void {
  if (workspace.name === name) return;
  workspace.name = name;
  markWorkspaceDirty();
  notify();
}

export function setWorkspacePath(path: string): void {
  if (workspace.path === path) return;
  workspace.path = path;
  notify();
}

/**
 * 记下当前 .band 是否加密保存。保存成功时由 main.ts 按写盘结果写回；这属于
 * 「这份文件是怎么存的」，不是内容改动，所以不置脏。
 */
export function setWorkspaceEncrypted(encrypted: boolean): void {
  workspace.encrypted = encrypted;
}

/**
 * 把一条请求快照写进内存工作区（已有同 id 的原地替换）。
 * focus 决定要不要把它变成「当前条目」：后台执行完的那次发送不该把列表里的
 * 当前条目从用户正在看的请求上抢走；用户还在那条请求上时才需要认领。
 */
export function upsertEntry(entry: SavedRequest, focus = true): void {
  const index = workspace.entries.findIndex((item) => item.id === entry.id);
  if (index < 0) workspace.entries = [...workspace.entries, entry];
  else workspace.entries[index] = entry;
  if (focus) workspace.currentId = entry.id;
  markWorkspaceDirty();
  notify();
}

/* ---- 请求分组（工作区的一部分，随 .band 落盘） ---- */

// 文件夹只在内存里改：树本身随 .band 保存，和 vars 一样「改了就置脏」。
// 顺序即显示顺序——folders 数组里同 parentId 的先后决定同级次序。

/** 新建文件夹，追加到同级末尾；parentId 为 null = 根目录。名字留空等界面改名。 */
export function addFolder(parentId: string | null): void {
  const parent = parentId === null ? null : workspace.folders.find((item) => item.id === parentId);
  if (parentId !== null && parent === undefined) return;
  workspace.folders = [
    ...workspace.folders,
    { id: newId(), name: "", parentId: parent?.id ?? "", requests: [] },
  ];
  markWorkspaceDirty();
  notify();
}

export function renameFolder(id: string, name: string): void {
  const folder = workspace.folders.find((item) => item.id === id);
  if (folder === undefined || folder.name === name) return;
  folder.name = name;
  markWorkspaceDirty();
  notify();
}

// 删文件夹不删内容：里面的请求上移一级（父是根就回根平铺），子文件夹改挂到
// 它的父级，然后把这一层摘掉——用户点的是「去掉这层分组」，不是「删掉这些请求」。
export function deleteFolder(id: string): void {
  const folder = workspace.folders.find((item) => item.id === id);
  if (folder === undefined) return;
  const parent = workspace.folders.find((item) => item.id === folder.parentId);
  if (parent !== undefined) parent.requests = [...parent.requests, ...folder.requests];
  for (const child of workspace.folders) {
    if (child.parentId === id) child.parentId = parent?.id ?? "";
  }
  workspace.folders = workspace.folders.filter((item) => item.id !== id);
  markWorkspaceDirty();
  notify();
}

/** 把请求移动到某个文件夹末尾；folderId 为 null = 移回根目录（从所在文件夹摘掉即可）。 */
export function moveRequest(requestId: string, folderId: string | null): void {
  if (!workspace.entries.some((entry) => entry.id === requestId)) return;
  const target = folderId === null ? undefined : workspace.folders.find((item) => item.id === folderId);
  if (folderId !== null && target === undefined) return;
  let changed = false;
  for (const folder of workspace.folders) {
    if (!folder.requests.includes(requestId)) continue;
    folder.requests = folder.requests.filter((item) => item !== requestId);
    changed = true;
  }
  if (target !== undefined) {
    target.requests = [...target.requests, requestId];
    changed = true;
  }
  if (!changed) return;
  markWorkspaceDirty();
  notify();
}

function newId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

/** 连接类 kind 在自动命名里的前缀；HTTP 没有类型前缀，用方法名。 */
const KIND_PREFIX: Partial<Record<RequestKind, string>> = {
  websocket: "WS",
  socketio: "SIO",
  tcp: "TCP",
  grpc: "GRPC",
};

function generatedName(kind: RequestKind, method: string, url: string): string {
  // Markdown 文档与 Composer 编排不参与自动命名：没有 URL 可推导，空名由界面按
  // 语言给「未命名文档 / 未命名 Composer」（与工作区空名的显示约定一致）。
  if (kind === "markdown" || kind === "composer") return "";
  const target = url.trim();
  // tcp/grpc 的地址就是 host:port，不能过 URL 解析——"api.example.com:50051"
  // 会被当成 scheme，host 为空，名字里只剩端口。
  const hostPort = kind === "tcp" || kind === "grpc";
  let host = target;
  if (!hostPort) {
    try {
      const parsed = new URL(target);
      host = `${parsed.host}${parsed.pathname}`;
    } catch {
      // A URL without a scheme keeps its raw form.
    }
  }
  const name = `${KIND_PREFIX[kind] ?? method} ${host}`.trim();
  return name.length > 40 ? name.slice(0, 40) : name;
}

export function snapshotCurrent(): SavedRequest {
  const state = request;
  const current =
    workspace.currentId !== null
      ? workspace.entries.find((item) => item.id === workspace.currentId)
      : undefined;
  // A request that diverged from the loaded entry is a new request: keep the
  // original entry untouched and fork a new one on send. HTTP compares method
  // and URL, WS/TCP only the URL, since neither has a method. A saved kind of
  // "" is an HTTP entry from an older .band.
  const forked =
    current !== undefined &&
    (requestKind(current.kind) !== state.kind ||
      current.url !== state.url ||
      (state.kind === "http" && current.method !== state.method));
  const id = forked || workspace.currentId === null ? newId() : workspace.currentId;
  const existing = workspace.entries.find((item) => item.id === id);
  // A rename sticks; an untouched entry follows the naming rule.
  const keepName =
    existing !== undefined &&
    existing.name.trim() !== "" &&
    existing.name !== generatedName(requestKind(existing.kind), existing.method, existing.url);
  const name =
    keepName && existing !== undefined
      ? existing.name
      : generatedName(state.kind, state.method, state.url);
  return {
    id,
    name,
    kind: state.kind,
    method: state.method,
    url: state.url,
    params: wireValues(state.params),
    headers: wireValues(state.headers),
    bodyType: state.bodyType,
    formData: state.formData
      .filter((row) => row.enabled && row.key.trim() !== "")
      .map((row) => ({
        key: row.key,
        value: row.value,
        contentType: row.contentType,
        description: row.description,
        kind: row.kind,
        filePath: row.filePath,
      })),
    urlEncoded: wireValues(state.urlEncoded),
    raw: state.raw,
    rawType: state.rawType,
    binaryPath: state.binaryPath,
    auth: {
      type: state.auth.type,
      username: state.auth.username,
      password: state.auth.password,
      token: state.auth.token,
    },
    cookies: wireValues(state.cookies),
    timeout: state.timeout,
    http2: state.http2,
    assertions: state.assertions.map((rule) => ({
      id: rule.id,
      enabled: rule.enabled,
      type: rule.type,
      value: rule.value,
    })),
    messages: state.messages.map((message) => ({ ...message })),
    socketProtocol: state.socketProtocol,
    socketEvents: state.socketEvents.map((event) => ({ ...event })),
    socketIOVersion: state.socketIOVersion,
    socketIOPath: state.socketIOPath,
    grpcMethod: state.grpcMethod,
    grpcPlaintext: state.grpcPlaintext,
    grpcTlsSkipVerify: state.grpcTlsSkipVerify,
    grpcServerName: state.grpcServerName,
    grpcProtoFiles: [...state.grpcProtoFiles],
    grpcImportPaths: [...state.grpcImportPaths],
    preScript: state.preScript,
    postScript: state.postScript,
    sendScript: state.sendScript,
    receiveScript: state.receiveScript,
    markdown: state.markdown,
    composerScript: state.composerScript,
    examples: state.examples.map((example) => ({ ...example })),
  };
}

window.addEventListener("beforeunload", () => {
  if (saveTimer !== undefined) {
    window.clearTimeout(saveTimer);
    saveTimer = undefined;
    save();
  }
});
