import type { Channel } from "mygo-runtime";
import type { AssertionResult, AssertionRule } from "./assert";
import { ASSERTION_TYPE_IDS, newRuleId } from "./assert";
import type {
  APIRequest,
  APIResponse,
  FormField,
  KeyValue,
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

/** 编辑器四种类型：HTTP 请求、WebSocket 连接、TCP 连接、Socket.IO 连接。 */
export const REQUEST_KINDS = ["http", "websocket", "tcp", "socketio"] as const;
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
  /** 预执行脚本；空 = 不执行。 */
  preScript: string;
  /** 后执行脚本；空 = 不执行。 */
  postScript: string;
}

export interface ResponseState {
  sending: boolean;
  streaming: boolean;
  open: SSEOpen | null;
  events: SSEEvent[];
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
  currentId: string | null;
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
    preScript: "",
    postScript: "",
  };
}

let request: RequestState = defaultRequest();

const runtime: ResponseState = {
  sending: false,
  streaming: false,
  open: null,
  events: [],
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
  scheduleSave();
  notify();
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
  Object.assign(runtime, patch);
  notify();
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

// Events are pushed into the live array: a long stream must not copy it per event.
export function pushStreamEvent(event: SSEEvent): void {
  runtime.events.push(event);
  notify();
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
    localStorage.setItem(STORAGE_KEY, JSON.stringify(request));
  } catch {
    // Storage can be disabled or full; the page keeps working without it.
  }
}

/* ---- 工作区脏状态 ---- */

// 变量随工作区落盘，所以改变量也是改工作区；保存成功时由 main.ts 清脏。
// Go 侧凭这个状态决定用户点窗口 X 时是直接关窗还是先问页面。
let workspaceDirty = false;

export function isWorkspaceDirty(): boolean {
  return workspaceDirty;
}

export function setWorkspaceDirty(dirty: boolean): void {
  if (workspaceDirty === dirty) return;
  workspaceDirty = dirty;
  void WorkspaceService.setDirty(dirty).catch(() => {
    // 推给 Go 只影响关窗时的询问，推送失败不影响页面内的状态。
  });
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
  if (changed) setWorkspaceDirty(true);
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
    preScript: text(source.preScript),
    postScript: text(source.postScript),
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
  currentId: null,
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
    preScript: text(saved.preScript),
    postScript: text(saved.postScript),
  };
}

export function loadWorkspace(file: WorkspaceFile): void {
  workspace.name = text(file.data.name);
  workspace.path = text(file.path) || null;
  workspace.entries = file.data.requests;
  // 变量属于工作区文件：整体换掉，且替换本身不算改动（清脏统一在后面做）。
  replaceVars(hydrateVars(file.data.vars));
  const first = workspace.entries[0];
  if (first === undefined) {
    workspace.currentId = null;
    setWorkspaceDirty(false);
    notify();
    return;
  }
  workspace.currentId = first.id;
  applyRequest(requestFromSaved(first));
  setWorkspaceDirty(false);
}

// 新建空工作区：条目与变量清空、回到一条空请求，且这里的一切都不算改动
// （脏状态最后统一清掉，否则换变量/换编辑器时的 notify 会把它又点起来）。
export function newWorkspace(): void {
  workspace.name = "";
  workspace.path = null;
  workspace.entries = [];
  workspace.currentId = null;
  // 变量跟着工作区走，新工作区从空变量开始；走不置脏的安静路径。
  replaceVars({});
  // 空工作区没有条目可载入，编辑器回落到一条空 HTTP 请求（同 newRequest 语义）。
  applyRequest(defaultRequest());
  setWorkspaceDirty(false);
  notify();
}

export function newRequest(kind: RequestKind = "http"): void {
  workspace.currentId = null;
  applyRequest(defaultRequest(kind));
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
  if (workspace.currentId === id) workspace.currentId = null;
  setWorkspaceDirty(true);
  notify();
}

export function renameEntry(id: string, name: string): void {
  const entry = workspace.entries.find((item) => item.id === id);
  if (entry === undefined || entry.name === name) return;
  entry.name = name;
  setWorkspaceDirty(true);
  notify();
}

export function renameWorkspace(name: string): void {
  if (workspace.name === name) return;
  workspace.name = name;
  setWorkspaceDirty(true);
  notify();
}

export function setWorkspacePath(path: string): void {
  if (workspace.path === path) return;
  workspace.path = path;
  notify();
}

export function upsertEntry(entry: SavedRequest): void {
  const index = workspace.entries.findIndex((item) => item.id === entry.id);
  if (index < 0) workspace.entries = [...workspace.entries, entry];
  else workspace.entries[index] = entry;
  workspace.currentId = entry.id;
  setWorkspaceDirty(true);
  notify();
}

function newId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

/** 连接类 kind 在自动命名里的前缀；HTTP 没有类型前缀，用方法名。 */
const KIND_PREFIX: Partial<Record<RequestKind, string>> = { websocket: "WS", socketio: "SIO" };

function generatedName(kind: RequestKind, method: string, url: string): string {
  const target = url.trim();
  let name: string;
  if (kind === "tcp") {
    name = `TCP ${target}`.trim();
  } else {
    let host = target;
    try {
      const parsed = new URL(target);
      host = `${parsed.host}${parsed.pathname}`;
    } catch {
      // A URL without a scheme keeps its raw form.
    }
    name = `${KIND_PREFIX[kind] ?? method} ${host}`.trim();
  }
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
    preScript: state.preScript,
    postScript: state.postScript,
  };
}

window.addEventListener("beforeunload", () => {
  if (saveTimer !== undefined) {
    window.clearTimeout(saveTimer);
    saveTimer = undefined;
    save();
  }
});
