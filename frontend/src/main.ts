import { Channel } from "mygo-runtime";
import { runAssertions } from "./assert";
import { mountAssertionsPanel } from "./components/assertionsPanel";
import { mountAuthEditor } from "./components/authEditor";
import { mountBodyEditor } from "./components/bodyEditor";
import { mountKVTable } from "./components/kvTable";
import { mountResponseView } from "./components/responseView";
import { mountSocketComposer, mountSocketSettings } from "./components/socketComposer";
import { mountSocketPanel } from "./components/socketPanel";
import { createTabs, type TabDef } from "./components/tabs";
import { mountUrlBar } from "./components/urlBar";
import { mountWorkspaceSidebar } from "./components/workspaceSidebar";
import type { SSEMessage } from "./mygo";
import { RequestService, WorkspaceService, type WorkspaceData } from "./mygo";
import {
  connectSocket,
  disconnectSocket,
  getSocketSession,
  stopSocketIfActive,
  subscribeSocket,
} from "./socket";
import {
  buildRequest,
  deleteEntry,
  emptyRow,
  getRequest,
  getResponse,
  getWorkspace,
  loadEntry,
  loadSavedRequest,
  loadWorkspace,
  newRequest,
  pushStreamEvent,
  renameEntry,
  renameWorkspace,
  setStreamChannel,
  setWorkspacePath,
  snapshotCurrent,
  subscribe,
  updateResponse,
  upsertEntry,
  type RequestKind,
} from "./state";

const HTTP_REQUEST_TABS: TabDef[] = [
  { id: "headers", label: "Header" },
  { id: "query", label: "Query" },
  { id: "path", label: "Path" },
  { id: "body", label: "Body" },
  { id: "auth", label: "认证" },
  { id: "cookies", label: "Cookie" },
  { id: "pre", label: "预执行脚本" },
  { id: "post", label: "后执行脚本" },
];

const WEBSOCKET_REQUEST_TABS: TabDef[] = [
  { id: "messages", label: "发送消息" },
  { id: "headers", label: "请求头" },
  { id: "query", label: "请求参数" },
  { id: "settings", label: "设置" },
];

const TCP_REQUEST_TABS: TabDef[] = [{ id: "messages", label: "发送消息" }];

function requestTabs(kind: RequestKind): TabDef[] {
  switch (kind) {
    case "websocket":
      return WEBSOCKET_REQUEST_TABS;
    case "tcp":
      return TCP_REQUEST_TABS;
    default:
      return HTTP_REQUEST_TABS;
  }
}

function firstTab(kind: RequestKind): string {
  return requestTabs(kind)[0]?.id ?? "headers";
}

function need<T extends HTMLElement>(selector: string): T {
  const element = document.querySelector<T>(selector);
  if (element === null) throw new Error(`页面缺少 ${selector}`);
  return element;
}

const topbar = need<HTMLElement>("#topbar");
const reqTabsHost = need<HTMLElement>("#req-tabs");
const reqPanel = need<HTMLElement>("#req-panel");
const responsePane = need<HTMLElement>("#response-pane");
const resTabsHost = need<HTMLElement>("#res-tabs");
const resContent = need<HTMLElement>("#res-content");
const assertPanel = need<HTMLElement>("#assert-panel");
const socketResTabs = need<HTMLElement>("#socket-res-tabs");
const socketResContent = need<HTMLElement>("#socket-res-content");
const sidebar = need<HTMLElement>("#workspace-sidebar");

const views = new Map<string, HTMLElement>();
for (const id of ["headers", "query", "path", "body", "auth", "cookies", "pre", "post", "messages", "settings"]) {
  const view = document.createElement("div");
  view.className = "req-view";
  view.dataset.view = id;
  view.hidden = true;
  reqPanel.append(view);
  views.set(id, view);
}

function viewOf(id: string): HTMLElement {
  const view = views.get(id);
  if (view === undefined) throw new Error(`缺少请求面板 ${id}`);
  return view;
}

function showRequestTab(id: string): void {
  for (const [key, view] of views) view.hidden = key !== id;
}

// 执行过的请求自动收进内存工作区：新建的请求执行一次后出现在列表里；
// 已有条目按 id 原地刷新，改名的条目保留别名。成功、出错、手动停止都收。
function rememberCurrentRequest(): void {
  upsertEntry(snapshotCurrent());
}

async function send(): Promise<void> {
  if (getResponse().sending) return;
  if (getRequest().url.trim() === "") {
    document.querySelector<HTMLInputElement>("#url")?.focus();
    return;
  }

  const request = buildRequest();
  const channel = new Channel<SSEMessage>((message) => {
    if (message.open) {
      updateResponse({ streaming: true, open: message.open, response: null, events: [] });
      // 执行已确认（拿到响应头），立即把请求收进内存工作区，
      // 长流不必等到结束才出现在列表里。
      rememberCurrentRequest();
    } else if (message.event) {
      pushStreamEvent(message.event);
    }
  });
  channel.onclose = () => {
    if (getResponse().streaming) updateResponse({ streaming: false });
  };

  setStreamChannel(channel);
  updateResponse({
    sending: true,
    streaming: false,
    stopped: false,
    open: null,
    events: [],
    error: null,
    results: [],
  });

  try {
    const response = await RequestService.send(request, channel);
    setStreamChannel(null);
    updateResponse({
      sending: false,
      streaming: false,
      response,
      error: null,
      results: runAssertions(getRequest().assertions, response),
    });
  } catch (error) {
    setStreamChannel(null);
    const stopped = getResponse().stopped;
    updateResponse({
      sending: false,
      streaming: false,
      response: null,
      error: stopped ? null : error instanceof Error ? error.message : String(error),
      results: [],
    });
  }

  // 无论成败，执行过的请求都已进内存工作区；保存按钮只是把这整块
  // 内存写进 .band 文件。
  rememberCurrentRequest();
}

let workspaceError: string | null = null;

function reportWorkspaceError(error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  workspaceError = message;
  updateResponse({ error: message });
}

function clearWorkspaceError(): void {
  const previous = workspaceError;
  workspaceError = null;
  if (previous !== null && getResponse().error === previous) updateResponse({ error: null });
}

function workspaceData(): WorkspaceData {
  const state = getWorkspace();
  return { name: state.name, requests: state.entries };
}

async function openWorkspace(): Promise<void> {
  try {
    const file = await WorkspaceService.open();
    if (file.path === "") return;
    clearWorkspaceError();
    loadWorkspace(file);
  } catch (error) {
    reportWorkspaceError(error);
  }
}

// 拖入的 .band：字节经 IPC 导入。webview 拿不到拖入文件的路径，
// 所以 path 置空——之后点「保存」会走另存为，让用户选落盘位置。
async function importDroppedBand(base64: string): Promise<void> {
  try {
    const data = await WorkspaceService.import(base64);
    clearWorkspaceError();
    loadWorkspace({ path: "", data });
  } catch (error) {
    reportWorkspaceError(error);
  }
}

async function saveWorkspace(forceSaveAs: boolean): Promise<void> {
  upsertEntry(snapshotCurrent());
  try {
    const path = getWorkspace().path;
    if (!forceSaveAs && path !== null) {
      await WorkspaceService.save(path, workspaceData());
    } else {
      const saved = await WorkspaceService.saveAs(workspaceData());
      if (saved === "") return;
      setWorkspacePath(saved);
    }
    clearWorkspaceError();
  } catch (error) {
    reportWorkspaceError(error);
  }
}

function mountScriptPlaceholder(host: HTMLElement, placeholder: string): void {
  const area = document.createElement("textarea");
  area.className = "script-area";
  area.readOnly = true;
  area.spellcheck = false;
  area.placeholder = placeholder;

  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = "脚本执行将在后续版本提供";

  host.append(area, note);
}

loadSavedRequest();

mountUrlBar(topbar, {
  send: () => void send(),
  connect: () => connectSocket(),
  disconnect: () => disconnectSocket(),
});

mountKVTable(viewOf("headers"), {
  rows: () => getRequest().headers,
  append: (state) => {
    state.headers = [...state.headers, emptyRow()];
  },
  remove: (state, index) => {
    state.headers = state.headers.filter((_, position) => position !== index);
  },
  addLabel: "+ 添加请求头",
});

mountKVTable(viewOf("query"), {
  rows: () => getRequest().params,
  append: (state) => {
    state.params = [...state.params, emptyRow()];
  },
  remove: (state, index) => {
    state.params = state.params.filter((_, position) => position !== index);
  },
  addLabel: "+ 添加参数",
  hint: "启用的参数会拼接到 URL 的查询字符串中。",
});

mountKVTable(viewOf("path"), {
  rows: () => getRequest().pathParams,
  append: (state) => {
    state.pathParams = [...state.pathParams, emptyRow()];
  },
  remove: (state, index) => {
    state.pathParams = state.pathParams.filter((_, position) => position !== index);
  },
  addLabel: "+ 添加 Path 参数",
  hint: "URL 中的 {占位符} 会自动生成行，发送前替换为编码后的值。",
});

mountBodyEditor(viewOf("body"));
mountAuthEditor(viewOf("auth"));
mountSocketComposer(viewOf("messages"));
mountSocketSettings(viewOf("settings"));

mountKVTable(viewOf("cookies"), {
  rows: () => getRequest().cookies,
  append: (state) => {
    state.cookies = [...state.cookies, emptyRow()];
  },
  remove: (state, index) => {
    state.cookies = state.cookies.filter((_, position) => position !== index);
  },
  addLabel: "+ 添加 Cookie",
});

mountScriptPlaceholder(viewOf("pre"), "// 预执行脚本：在发送请求前运行");
mountScriptPlaceholder(viewOf("post"), "// 后执行脚本：在收到响应后运行");

let currentKind: RequestKind = getRequest().kind;
let currentRequest = getRequest();
let tabsKey = JSON.stringify(requestTabs(currentKind));

const requestTabsHandle = createTabs(reqTabsHost, requestTabs(currentKind), firstTab(currentKind), showRequestTab);
showRequestTab(firstTab(currentKind));

function applyKindLayout(kind: RequestKind): void {
  const socket = kind !== "http";
  resTabsHost.hidden = socket;
  resContent.hidden = socket;
  assertPanel.hidden = socket;
  socketResTabs.hidden = !socket;
  socketResContent.hidden = !socket;
  responsePane.dataset.kind = kind;

  const defs = requestTabs(kind);
  const key = JSON.stringify(defs);
  if (key === tabsKey) return;
  tabsKey = key;
  requestTabsHandle.setTabs(defs);
  // 换了类型就从该类型的第一个页签开始（HTTP 仍是 Header）。
  requestTabsHandle.select(firstTab(kind));
  showRequestTab(requestTabsHandle.activeId());
}

applyKindLayout(currentKind);

// kind 变化或整份编辑器被替换（新建 / 载入条目 / 载入工作区）时，活连接先断开。
function syncEditor(): void {
  const request = getRequest();
  const replaced = request !== currentRequest;
  currentRequest = request;
  if (replaced) stopSocketIfActive();
  if (request.kind === currentKind) return;
  currentKind = request.kind;
  stopSocketIfActive();
  applyKindLayout(currentKind);
  // 两套响应区共用一个错误条：换类型后上一类请求的错误不该还挂着。
  if (getResponse().error !== null) updateResponse({ error: null });
}

subscribe(syncEditor);

mountResponseView(resTabsHost, resContent);
mountAssertionsPanel(assertPanel);
mountSocketPanel(socketResTabs, socketResContent);
applyKindLayout(currentKind);

// WS/TCP 的自动入册：open 时收一次，断开时再刷新一次；连接被拒等失败也收
//（与 HTTP 发送「无论成败都收」一致）。切换 kind / 载入其它条目会先断开，
// 那次断开时编辑器 URL 与连接对不上，不会误收刚载入的请求。
let lastSocketStatus = getSocketSession().status;

subscribeSocket(() => {
  const session = getSocketSession();
  const status = session.status;
  if (status === lastSocketStatus) return;
  lastSocketStatus = status;
  if (status === "open") {
    rememberCurrentRequest();
    return;
  }
  if (status !== "closed") return;
  const request = getRequest();
  if (request.kind === session.kind && request.url.trim() === session.url) rememberCurrentRequest();
});

mountWorkspaceSidebar(sidebar, {
  open: () => void openWorkspace(),
  save: () => void saveWorkspace(false),
  saveAs: () => void saveWorkspace(true),
  newRequest: (kind) => newRequest(kind),
  load: (id) => loadEntry(id),
  remove: (id) => deleteEntry(id),
  rename: (id, name) => renameEntry(id, name),
  renameWorkspace: (name) => renameWorkspace(name),
  importBand: (payload) => {
    if ("error" in payload) reportWorkspaceError(new Error(payload.error));
    else void importDroppedBand(payload.base64);
  },
});
