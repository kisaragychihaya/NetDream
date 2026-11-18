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
  SSEEvent,
  SSEMessage,
  SSEOpen,
  WorkspaceFile,
} from "./mygo";

export const STORAGE_KEY = "netdream.request";
const SAVE_DELAY_MS = 300;

export const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;
export type Method = (typeof METHODS)[number];

/** 编辑器三态：HTTP 请求、WebSocket 连接、TCP 连接。 */
export const REQUEST_KINDS = ["http", "websocket", "tcp"] as const;
export type RequestKind = (typeof REQUEST_KINDS)[number];

/** WS/TCP 消息模板的类型（SavedMessage.msgType）。 */
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

export interface FormRow extends KVRow {
  contentType: string;
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
  /** WS/TCP 的已存消息模板。 */
  messages: SavedMessage[];
  /** WS 子协议（Sec-WebSocket-Protocol），逗号分隔。 */
  socketProtocol: string;
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
  return { key: "", value: "", contentType: "", description: "", enabled: true };
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
    });
  }
  return messages;
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
  };
}

/* ---- 工作区 ---- */

const workspace: WorkspaceState = {
  name: "未命名工作区",
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
  };
}

export function loadWorkspace(file: WorkspaceFile): void {
  workspace.name = text(file.data.name) || "未命名工作区";
  workspace.path = text(file.path) || null;
  workspace.entries = file.data.requests;
  const first = workspace.entries[0];
  if (first === undefined) {
    workspace.currentId = null;
    notify();
    return;
  }
  workspace.currentId = first.id;
  applyRequest(requestFromSaved(first));
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
  notify();
}

export function renameEntry(id: string, name: string): void {
  const entry = workspace.entries.find((item) => item.id === id);
  if (entry === undefined || entry.name === name) return;
  entry.name = name;
  notify();
}

export function renameWorkspace(name: string): void {
  if (workspace.name === name) return;
  workspace.name = name;
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
  notify();
}

function newId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

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
    name = `${kind === "websocket" ? "WS" : method} ${host}`.trim();
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
  };
}

window.addEventListener("beforeunload", () => {
  if (saveTimer === undefined) return;
  window.clearTimeout(saveTimer);
  saveTimer = undefined;
  save();
});
