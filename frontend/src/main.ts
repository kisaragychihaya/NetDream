import { Channel, onFileDrop } from "mygo-runtime";
import { runAssertions } from "./assert";
import { mountAssertionsPanel } from "./components/assertionsPanel";
import { mountAuthEditor } from "./components/authEditor";
import { mountBodyEditor } from "./components/bodyEditor";
import { mountKVTable } from "./components/kvTable";
import { mountResponseView } from "./components/responseView";
import { mountScriptEditor } from "./components/scriptEditor";
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
  loadVars,
  loadWorkspace,
  newRequest,
  pushStreamEvent,
  renameEntry,
  renameWorkspace,
  setStreamChannel,
  setVars,
  setWorkspacePath,
  snapshotCurrent,
  subscribe,
  updateRequest,
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
    scriptLogs: [],
    scriptTests: [],
    preScriptError: null,
    postScriptError: null,
  });

  try {
    const response = await RequestService.send(request, channel);
    setStreamChannel(null);
    // 脚本写回的变量覆盖界面的变量表。后端保证无脚本时原样返回快照，
    // 所以带脚本时无条件下发——脚本删掉全部变量时空对象也要生效。
    const hadScripts = request.preScript !== "" || request.postScript !== "";
    if (hadScripts || Object.keys(response.vars).length > 0) setVars(response.vars);
    const script = {
      scriptLogs: response.scriptLogs,
      scriptTests: response.scriptTests,
      preScriptError: response.preScriptError === "" ? null : response.preScriptError,
      postScriptError: response.postScriptError === "" ? null : response.postScriptError,
    };
    if (response.preScriptError !== "") {
      // 预脚本出错时请求没有发出：按失败展示，不当成普通响应。
      updateResponse({
        sending: false,
        streaming: false,
        response: null,
        error: `预执行脚本出错：${response.preScriptError}`,
        results: [],
        ...script,
      });
    } else {
      updateResponse({
        sending: false,
        streaming: false,
        response,
        error: null,
        results: runAssertions(getRequest().assertions, response),
        ...script,
      });
    }
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

// 拖入的 .band：mygo 原生拖放投递绝对路径，按路径读盘，「保存」直接写回原文件。
async function openDroppedWorkspace(paths: string[]): Promise<void> {
  const path = paths.find((item) => item.toLowerCase().endsWith(".band"));
  if (path === undefined) {
    reportWorkspaceError(new Error("拖入的不是 .band 工作区文件"));
    return;
  }
  try {
    const file = await WorkspaceService.openPath(path);
    clearWorkspaceError();
    loadWorkspace(file);
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

loadSavedRequest();
loadVars();

mountUrlBar(topbar, {
  send: () => void send(),
  connect: () => connectSocket(),
  disconnect: () => disconnectSocket(),
});

mountKVTable(viewOf("headers"), {
  rows: () => getRequest().headers,
  append: () => {
    updateRequest((state) => {
      state.headers = [...state.headers, emptyRow()];
    });
  },
  remove: (index) => {
    updateRequest((state) => {
      state.headers = state.headers.filter((_, position) => position !== index);
    });
  },
  addLabel: "+ 添加请求头",
});

mountKVTable(viewOf("query"), {
  rows: () => getRequest().params,
  append: () => {
    updateRequest((state) => {
      state.params = [...state.params, emptyRow()];
    });
  },
  remove: (index) => {
    updateRequest((state) => {
      state.params = state.params.filter((_, position) => position !== index);
    });
  },
  addLabel: "+ 添加参数",
  hint: "启用的参数会拼接到 URL 的查询字符串中。",
});

mountKVTable(viewOf("path"), {
  rows: () => getRequest().pathParams,
  append: () => {
    updateRequest((state) => {
      state.pathParams = [...state.pathParams, emptyRow()];
    });
  },
  remove: (index) => {
    updateRequest((state) => {
      state.pathParams = state.pathParams.filter((_, position) => position !== index);
    });
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
  append: () => {
    updateRequest((state) => {
      state.cookies = [...state.cookies, emptyRow()];
    });
  },
  remove: (index) => {
    updateRequest((state) => {
      state.cookies = state.cookies.filter((_, position) => position !== index);
    });
  },
  addLabel: "+ 添加 Cookie",
});

mountScriptEditor(
  viewOf("pre"),
  () => getRequest().preScript,
  (value) => {
    updateRequest((state) => {
      state.preScript = value;
    });
  },
  subscribe,
);

mountScriptEditor(
  viewOf("post"),
  () => getRequest().postScript,
  (value) => {
    updateRequest((state) => {
      state.postScript = value;
    });
  },
  subscribe,
);

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

// 拖入 .band 由原生层接管，页面不消费 DOM drop 事件：订阅一次拿绝对路径。
onFileDrop((drop) => {
  void openDroppedWorkspace(drop.paths);
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
});
