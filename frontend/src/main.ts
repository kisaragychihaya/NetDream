import { Channel, onFileDrop } from "mygo-runtime";
import { runAssertions, type AssertionRule } from "./assert";
import { mountAssertionsPanel } from "./components/assertionsPanel";
import { mountAuthEditor } from "./components/authEditor";
import { mountBandTrustDialog } from "./components/bandTrust";
import { mountBodyEditor } from "./components/bodyEditor";
import { mountComposerPanel } from "./components/composerPanel";
import { mountConfirmDialog } from "./components/closeConfirm";
import { mountFirstRunDialog } from "./components/firstRun";
import { mountGrpcComposer } from "./components/grpcComposer";
import { mountGrpcMethods } from "./components/grpcMethods";
import { mountGrpcPanel } from "./components/grpcPanel";
import { mountKVTable } from "./components/kvTable";
import { mountMarkdownEditor } from "./components/markdownEditor";
import { mountResponseView } from "./components/responseView";
import { mountSaveDialog } from "./components/saveDialog";
import { mountScriptEditor } from "./components/scriptEditor";
import { mountSettings } from "./components/settings";
import { mountSocketComposer, mountSocketSettings } from "./components/socketComposer";
import { mountSocketEventsEditor } from "./components/socketEventsEditor";
import { mountSocketPanel } from "./components/socketPanel";
import { createTabs, type TabDef } from "./components/tabs";
import { mountUrlBar } from "./components/urlBar";
import { mountWorkspaceSidebar } from "./components/workspaceSidebar";
import { applyStatic, onLangChange, t } from "./i18n";
import type { APIRequest, SavedRequest, SSEMessage, WorkspaceFile, WorkspaceOpen } from "./mygo";
import { IdentityService, RequestService, WorkspaceService, events, type WorkspaceData } from "./mygo";
import { isMyGo } from "./runtime";
import { requireScriptTrust } from "./scriptGate";
import { mountExamplePicker, type ScriptExampleContext } from "./scriptExamples";
import { runComposer, stopComposer, stopComposerIfActive } from "./composer";
import {
  disconnectGrpc,
  getGrpcSession,
  grpcCall,
  stopGrpcIfActive,
  subscribeGrpc,
} from "./grpc";
import {
  connectSocket,
  disconnectSocket,
  getSocketSession,
  stopSocketIfActive,
  subscribeSocket,
} from "./socket";
import {
  addFolder,
  buildRequest,
  deleteEntry,
  deleteFolder,
  emptyRow,
  getRequest,
  getResponse,
  getVars,
  getWorkspace,
  getWorkspaceGeneration,
  getWorkspaceRevision,
  isWorkspaceDirty,
  loadEntry,
  loadSavedRequest,
  loadWorkspace,
  moveRequest,
  newComposerDocument,
  newMarkdownDocument,
  newRequest,
  newWorkspace,
  pushStreamEvent,
  renameEntry,
  renameFolder,
  renameWorkspace,
  setCachedIdentity,
  setFormFilePath,
  setStreamChannel,
  setVars,
  setWorkspaceDirty,
  setWorkspaceEncrypted,
  setWorkspacePath,
  snapshotCurrent,
  subscribe,
  updateRequest,
  updateResponse,
  upsertEntry,
  type RequestKind,
  type RequestState,
} from "./state";

// 页签文案随语言变，所以每次按当前语言现建；id 与顺序是固定的。
function requestTabs(kind: RequestKind): TabDef[] {
  switch (kind) {
    case "markdown":
      // Markdown 条目没有请求页签：主区整个让给文档编辑器。
      return [];
    case "composer":
      // Composer 编排同样只有一整页（脚本编辑器），页签整条收起。
      return [];
    case "websocket":
      return [
        { id: "messages", label: t("tabs.messages") },
        { id: "headers", label: t("tabs.wsHeaders") },
        { id: "query", label: t("tabs.wsQuery") },
        { id: "pre-script", label: t("tabs.preConnectScript") },
        { id: "send-script", label: t("tabs.sendScript") },
        { id: "receive-script", label: t("tabs.receiveScript") },
        { id: "settings", label: t("tabs.settings") },
      ];
    case "socketio":
      return [
        { id: "messages", label: t("tabs.messages") },
        { id: "events", label: t("tabs.events") },
        { id: "headers", label: t("tabs.wsHeaders") },
        { id: "query", label: t("tabs.wsQuery") },
        { id: "pre-script", label: t("tabs.preConnectScript") },
        { id: "send-script", label: t("tabs.sendScript") },
        { id: "receive-script", label: t("tabs.receiveScript") },
        { id: "settings", label: t("tabs.settings") },
      ];
    case "grpc":
      // 方法与消息各有独立视图（socket 的发送消息页是另一套编辑器），
      // Metadata 复用键值表格但单独挂一份（行数据仍是 headers）。
      return [
        { id: "grpc-methods", label: t("tabs.grpcMethods") },
        { id: "grpc-messages", label: t("tabs.grpcMessages") },
        { id: "grpc-metadata", label: t("tabs.grpcMetadata") },
        { id: "settings", label: t("tabs.settings") },
      ];
    case "tcp":
      return [
        { id: "messages", label: t("tabs.messages") },
        { id: "pre-script", label: t("tabs.preConnectScript") },
        { id: "send-script", label: t("tabs.sendScript") },
        { id: "receive-script", label: t("tabs.receiveScript") },
      ];
    default:
      return [
        { id: "headers", label: t("tabs.headers") },
        { id: "query", label: t("tabs.query") },
        { id: "path", label: t("tabs.path") },
        { id: "body", label: t("tabs.body") },
        { id: "auth", label: t("tabs.auth") },
        { id: "cookies", label: t("tabs.cookies") },
        { id: "pre", label: t("tabs.preScript") },
        { id: "post", label: t("tabs.postScript") },
      ];
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
for (const id of [
  "headers",
  "query",
  "path",
  "body",
  "auth",
  "cookies",
  "pre",
  "post",
  "messages",
  "events",
  "pre-script",
  "send-script",
  "receive-script",
  "grpc-methods",
  "grpc-messages",
  "grpc-metadata",
  "settings",
  "markdown",
  "composer",
]) {
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

// 脚本编辑器的惰性创建表：页签第一次显示时才 ensure（Monaco 很重，见
// components/scriptEditor.ts）。表先声明，挂载后再填空，避免 showRequestTab 在
// 编辑器挂载前被调用时踩到未初始化绑定。
const lazyViews = new Map<string, { ensure: () => void }>();

function showRequestTab(id: string): void {
  for (const [key, view] of views) view.hidden = key !== id;
  lazyViews.get(id)?.ensure();
}

// WS/TCP/Socket.IO 的自动入册：连接事件里已经核对过 kind 与 URL，收的就是当前
// 编辑器里的那条请求。HTTP/SSE 在发送开始时按发送时快照入册（见 send）。
function rememberCurrentRequest(): void {
  upsertEntry(snapshotCurrent());
}

/** 一次发送的不可变身份：回调、停止与完成路径都凭它判断结果该不该写进界面。 */
interface Execution {
  /** 单调自增的执行号。 */
  id: number;
  /** 发送时的编辑器对象；换条目/载入工作区会换掉它（state 的 applyRequest）。 */
  editor: RequestState;
  /** 实际发出的请求（含发送时的变量快照）。 */
  request: APIRequest;
  /** 发送时的条目快照：自动入册保存它，而不是完成时的编辑器。 */
  entry: SavedRequest;
  /** 发送时的断言规则快照：结果按它算，不按完成时的编辑器。 */
  assertions: AssertionRule[];
  hasScripts: boolean;
}

let executionSeq = 0;
// 当前在途的执行。同一时刻只发一次（发送按钮禁用），但换编辑器时这里会被清空：
// 上一次执行仍在后端跑完，只是它的结果不再属于任何界面，也不再碰工作区。
let inFlight: Execution | null = null;

// 结果是否还属于当前界面：执行号仍是当前在途的那次，且编辑器没被换掉（切条目、
// 载入工作区、新建请求都会换掉编辑器对象）。旧执行的结果一律丢弃，不污染新界面。
function stillCurrent(execution: Execution): boolean {
  return inFlight !== null && inFlight.id === execution.id && getRequest() === execution.editor;
}

// 换编辑器时把上一轮的响应清成初始态：在途的是上一条请求，它的结果不会再来这里。
function resetResponse(): void {
  updateResponse({
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
  });
}

async function send(): Promise<void> {
  if (inFlight !== null) return;
  // Markdown 文档与 Composer 编排没有发送语义（顶栏的按钮是别的动作，这里防的是
  // 快捷键等旁路入口）。
  if (getRequest().kind === "markdown" || getRequest().kind === "composer") return;
  if (getRequest().url.trim() === "") {
    document.querySelector<HTMLInputElement>("#url")?.focus();
    return;
  }

  const editor = getRequest();
  const hasScripts = editor.preScript.trim() !== "" || editor.postScript.trim() !== "";
  // 打开的 .band 里的脚本能读变量、请求头与请求体，把数据改写进 URL 再由后端
  // 附加认证发出去：本次载入没确认过信任之前一条都不发。确认后从 send() 重来，
  // 按当时的编辑器与工作区重新判断（信任门与 socket 连接共用，见 scriptGate.ts）。
  if (hasScripts && !requireScriptTrust(editor, () => void send())) return;

  const execution: Execution = {
    id: (executionSeq += 1),
    editor,
    request: buildRequest(),
    entry: snapshotCurrent(),
    assertions: editor.assertions.map((rule) => ({ ...rule })),
    hasScripts: editor.preScript !== "" || editor.postScript !== "",
  };

  // 入册一次就在此刻，之后 open/完成都不再碰工作区：等待期间的编辑由编辑器与
  // 保存快照负责，后台执行（哪怕用户已经切走）也不会往别的工作区里写东西。
  // 预脚本失败也算执行过——与「无论成败都收」一致。
  upsertEntry(execution.entry);
  inFlight = execution;

  const channel = new Channel<SSEMessage>((message) => {
    if (!stillCurrent(execution)) return;
    if (message.open) {
      updateResponse({ streaming: true, open: message.open, response: null, events: [] });
    } else if (message.event) {
      pushStreamEvent(message.event);
    }
  });
  channel.onclose = () => {
    // 换条目后旧流关闭不该动新界面的流式状态。
    if (!stillCurrent(execution)) return;
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
    const response = await RequestService.send(execution.request, channel);
    const current = stillCurrent(execution);
    if (inFlight === execution) {
      // 通道与在途句柄只在这次执行还占着它们时解绑：别把后一次发送的通道清掉。
      inFlight = null;
      setStreamChannel(null);
    }
    // 已经切到别的请求（或另一个工作区）：这次执行的结果不进界面，也绝不写工作区
    // ——它的请求在发送时就已经入册了。
    if (!current) return;
    // 脚本写回的变量覆盖界面的变量表。后端保证无脚本时原样返回快照，
    // 所以带脚本时无条件下发——脚本删掉全部变量时空对象也要生效。
    if (execution.hasScripts || Object.keys(response.vars).length > 0) setVars(response.vars);
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
        error: t("response.preScriptFailed", { error: response.preScriptError }),
        results: [],
        ...script,
      });
    } else {
      updateResponse({
        sending: false,
        streaming: false,
        response,
        error: null,
        results: runAssertions(execution.assertions, response),
        ...script,
      });
    }
  } catch (error) {
    const current = stillCurrent(execution);
    if (inFlight === execution) {
      inFlight = null;
      setStreamChannel(null);
    }
    if (!current) return;
    const stopped = getResponse().stopped;
    updateResponse({
      sending: false,
      streaming: false,
      response: null,
      error: stopped ? null : error instanceof Error ? error.message : String(error),
      results: [],
    });
  }
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
  return { name: state.name, requests: state.entries, vars: getVars(), folders: state.folders };
}

// 三键确认框是单例：对话框已经打开时，任何守卫的新触发都被忽略。
const confirmDialog = mountConfirmDialog({ save: () => saveWorkspace(false) });

interface UnsavedGuard {
  /** 放弃未保存改动那一键的文案键。 */
  discardKey: string;
  /** 取消当前操作那一键的文案键。 */
  cancelKey: string;
  /** 副文案的文案键；缺省用通用的「保存 / 不保存 / 取消」说明。 */
  noteKey?: string;
}

// 未保存守卫：工作区干净就直接执行；脏时先问，保存成功或用户明确放弃后才执行。
function guardUnsaved(action: () => void, guard: UnsavedGuard): void {
  if (!isWorkspaceDirty()) {
    action();
    return;
  }
  confirmDialog.open({
    titleKey: "dialog.unsavedTitle",
    noteKey: guard.noteKey,
    saveKey: "dialog.save",
    discardKey: guard.discardKey,
    cancelKey: guard.cancelKey,
    proceed: action,
  });
}

const OPEN_UNSAVED_GUARD: UnsavedGuard = { discardKey: "dialog.discard", cancelKey: "dialog.cancel" };
const NEW_WORKSPACE_GUARD: UnsavedGuard = {
  discardKey: "dialog.cancelSave",
  cancelKey: "dialog.cancelNew",
  noteKey: "dialog.newWorkspaceNote",
};

// 保存选项弹窗：挑好路径之后问「加密吗、写给谁」。它叠在未保存守卫上面（保存
// 成功后守卫才继续），所以自带更高的层级。
const saveDialog = mountSaveDialog();

// 首次运行的身份窗：本机还没有身份时启动就弹，且不能绕过（见组件文件头）。
const firstRunDialog = mountFirstRunDialog({
  ready: (identity) => setCachedIdentity(identity),
});

// 打开的 v2 .band 由未知身份签名时，后端只解到 header 就挂起等用户答复：
// 「信任一次 / 信任公钥」走 resolveTrust 拿回 WorkspaceFile，「取消」走
// dismissTrust 丢弃挂起的读盘（不加载）。
const bandTrustDialog = mountBandTrustDialog({
  resolve: (fingerprint, remember) => {
    void (async () => {
      try {
        const file = await WorkspaceService.resolveTrust(fingerprint, remember);
        loadOpened(file);
      } catch (error) {
        // 指纹对不上（挂起槽已被别的读盘换掉）或读取失败：中止加载，让用户重开。
        reportWorkspaceError(error);
      }
    })();
  },
  dismiss: () => {
    void WorkspaceService.dismissTrust().catch(() => {
      // 丢弃挂起数据失败没有补救办法：下次打开会重新挂起一份。
    });
  },
});

function startNewWorkspace(): void {
  clearWorkspaceError();
  newWorkspace();
}

// 读成功的 .band：选好文件之后才问是否保存（中途取消选择文件不该丢掉当前改动），
// 这一步与信任弹窗无关——两个入口（对话框、拖入）与 resolveTrust 都汇到这里。
function loadOpened(file: WorkspaceFile): void {
  if (file.path === "") return;
  guardUnsaved(() => {
    clearWorkspaceError();
    loadWorkspace(file);
  }, OPEN_UNSAVED_GUARD);
}

// Open/OpenPath 的返回有两种形态：file 可以直接加载；trust 说明签发者未知（数据
// 还没解析，等用户在弹窗里答复）；两个都空 = 用户在文件对话框里取消了。
function handleOpenResult(result: WorkspaceOpen): void {
  if (result.file !== undefined) {
    loadOpened(result.file);
    return;
  }
  if (result.trust !== undefined) bandTrustDialog.open(result.trust);
}

async function openWorkspace(): Promise<void> {
  // 信任弹窗挂着时又触发打开：忽略这次（后端只留得下一份挂起的读盘，第二次
  // 打开会把第一份换掉，正在显示的指纹就对不上了）。
  if (bandTrustDialog.isOpen()) return;
  try {
    handleOpenResult(await WorkspaceService.open());
  } catch (error) {
    reportWorkspaceError(error);
  }
}

// 拖入落在 form-data 的文件控件上时，把第一个路径填进那一行（不打开工作区）。
// drop.x / drop.y 是页面视口 CSS 像素，与 clientX/clientY 同义，可直接喂 elementFromPoint。
function dropOntoFormField(x: number, y: number, paths: string[]): boolean {
  const holder = document.elementFromPoint(x, y)?.closest<HTMLElement>("[data-file-drop-index]") ?? null;
  const path = paths[0];
  if (holder === null || path === undefined) return false;
  const index = Number(holder.dataset.fileDropIndex);
  if (!Number.isInteger(index)) return false;
  setFormFilePath(index, path);
  return true;
}

// 拖入的 .band：mygo 原生拖放投递绝对路径，按路径读盘，「保存」直接写回原文件。
async function openDroppedWorkspace(paths: string[]): Promise<void> {
  // 信任弹窗挂着时忽略新的拖入（同 openWorkspace：挂起槽只留一份读盘）。
  if (bandTrustDialog.isOpen()) return;
  const path = paths.find((item) => item.toLowerCase().endsWith(".band"));
  if (path === undefined) {
    reportWorkspaceError(new Error(t("sidebar.droppedNotBand")));
    return;
  }
  try {
    handleOpenResult(await WorkspaceService.openPath(path));
  } catch (error) {
    reportWorkspaceError(error);
  }
}

// 保存队列的尾巴：每次保存排在上一次后面，串行写盘。
let saveChain: Promise<unknown> = Promise.resolve();

async function saveWorkspace(forceSaveAs: boolean): Promise<boolean> {
  // 串行化：连点「保存」时后来的一次要等前一次写完再取快照，旧数据不会覆盖新数据。
  const run = saveChain.then(
    () => performSave(forceSaveAs),
    () => performSave(forceSaveAs),
  );
  saveChain = run.catch(() => undefined);
  return run;
}

async function performSave(forceSaveAs: boolean): Promise<boolean> {
  upsertEntry(snapshotCurrent());
  // 保存开始时的身份与版本：身份判断「还是不是这份工作区」，版本判断「期间有没有改动」。
  const generation = getWorkspaceGeneration();
  const revision = getWorkspaceRevision();
  try {
    // 有 path 直接用；没有（或用户点了「另存为」）先挑位置。AskSavePath 只挑位置，
    // 加密与否在后面的保存选项弹窗里定。
    const current = getWorkspace().path;
    let path = current;
    if (path === null || forceSaveAs) {
      path = await WorkspaceService.askSavePath(getWorkspace().name);
      // 保存对话框被取消：什么都没写，调用方不能当成已保存。
      if (path === "") return false;
      // 挑路径期间工作区被换掉：别把新工作区绑到刚写的文件上，也别拿旧工作区的
      // 加密状态去问选项。
      if (getWorkspaceGeneration() !== generation) return false;
    }
    const encrypted = getWorkspace().encrypted;
    const options = await saveDialog.ask({
      encrypted,
      // 只有写回当前这份文件才谈得上「沿用原信封」（后端按路径记着它）。
      canReuse: encrypted && path === current,
    });
    // 用户在保存选项里取消了：不写盘。
    if (options === null) return false;
    await WorkspaceService.save(path, workspaceData(), options);
    // 对话框开着时工作区被换掉的话，别把新工作区绑到刚写的文件上。
    if (getWorkspaceGeneration() === generation) {
      if (getWorkspace().path !== path) setWorkspacePath(path);
      // 写进去的信封就是这次的加密选择：加密 = 新信封或沿用的原信封，明文 = 没有。
      setWorkspaceEncrypted(options.encrypt);
    }
    clearWorkspaceError();
    // 写盘成功了，但期间又有改动（或工作区已经换掉）：脏状态保留，且这次保存
    // 不代表「已经把当前状态存好」，守卫不能继续——否则新改动会被关窗/换工作区丢掉。
    if (getWorkspaceRevision() !== revision) return false;
    setWorkspaceDirty(false);
    return true;
  } catch (error) {
    reportWorkspaceError(error);
    return false;
  }
}

loadSavedRequest();

// 变量改为随工作区（.band 内的 vars.json）保存后，清掉旧版本留在本地的快照。
try {
  localStorage.removeItem("netdream.vars");
} catch {
  // 存储被禁用时没有可清理的东西。
}

mountUrlBar(topbar, {
  send: () => void send(),
  connect: () => connectSocket(),
  disconnect: () => disconnectSocket(),
  grpcCall: () => grpcCall(),
  grpcStop: () => disconnectGrpc(),
  // Composer：右侧按钮在「运行 / 终止」之间切换。
  composerRun: () => runComposer(),
  composerStop: () => stopComposer(),
  // 文档的「保存」= 收进左侧工作区（更新条目内容、工作区置脏）；
  // 写成 .band 文件仍归侧栏的保存/另存。
  save: () => upsertEntry(snapshotCurrent()),
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
  addLabel: () => t("headers.add"),
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
  addLabel: () => t("query.add"),
  hint: () => t("query.hint"),
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
  addLabel: () => t("path.add"),
  hint: () => t("path.hint"),
});

mountBodyEditor(viewOf("body"));
mountAuthEditor(viewOf("auth"));
mountSocketComposer(viewOf("messages"));
mountSocketEventsEditor(viewOf("events"));
mountSocketSettings(viewOf("settings"));
mountGrpcMethods(viewOf("grpc-methods"));
mountGrpcComposer(viewOf("grpc-messages"));

// gRPC 的 Metadata 页：与 WS 的请求头同一份数据（意义是调用元数据），单独挂一份
// 表格只为了给「＋ 添加」换文案。
mountKVTable(viewOf("grpc-metadata"), {
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
  addLabel: () => t("grpc.addMetadata"),
  hint: () => t("grpc.metadataTableHint"),
});

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
  addLabel: () => t("cookies.add"),
});

// 脚本页顶部的 slim 工具行：目前只放「插入范例」。必须在 mountScriptEditor 之后
// 挂——编辑器挂载会清空宿主；prepend 保证工具行仍在编辑器上方。
function mountScriptToolbar(
  view: HTMLElement,
  context: ScriptExampleContext,
  editor: { insertText: (text: string, name: string) => void },
): void {
  const bar = document.createElement("div");
  bar.className = "script-toolbar";
  view.prepend(bar);
  // 选择器随页面常驻（工具行不会被重建），文档监听与语言订阅跟着页走。
  mountExamplePicker(bar, context, (code, name) => editor.insertText(code, name));
}

const preScriptEditor = mountScriptEditor(
  viewOf("pre"),
  () => getRequest().preScript,
  (value) => {
    updateRequest((state) => {
      state.preScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("pre"), "http-pre", preScriptEditor);

const postScriptEditor = mountScriptEditor(
  viewOf("post"),
  () => getRequest().postScript,
  (value) => {
    updateRequest((state) => {
      state.postScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("post"), "http-post", postScriptEditor);

lazyViews.set("pre", preScriptEditor);
lazyViews.set("post", postScriptEditor);

// 连接类 kind 的三个脚本页（WS/TCP/Socket.IO）：各挂一个 Monaco 实例，分别读写
// preScript（预连接脚本）/ sendScript / receiveScript。实例同样惰性创建，页签第一次
// 显示时才 ensure；HTTP 的两个脚本页仍是上面那一对，互不影响。
const socketPreScriptEditor = mountScriptEditor(
  viewOf("pre-script"),
  () => getRequest().preScript,
  (value) => {
    updateRequest((state) => {
      state.preScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("pre-script"), "socket-pre", socketPreScriptEditor);

const socketSendScriptEditor = mountScriptEditor(
  viewOf("send-script"),
  () => getRequest().sendScript,
  (value) => {
    updateRequest((state) => {
      state.sendScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("send-script"), "socket-send", socketSendScriptEditor);

const socketReceiveScriptEditor = mountScriptEditor(
  viewOf("receive-script"),
  () => getRequest().receiveScript,
  (value) => {
    updateRequest((state) => {
      state.receiveScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("receive-script"), "socket-receive", socketReceiveScriptEditor);

lazyViews.set("pre-script", socketPreScriptEditor);
lazyViews.set("send-script", socketSendScriptEditor);
lazyViews.set("receive-script", socketReceiveScriptEditor);

// Markdown 文档编辑器：实例惰性创建（ensure），内容双向绑定到当前条目的 markdown 字段。
const markdownEditor = mountMarkdownEditor(
  viewOf("markdown"),
  () => getRequest().markdown,
  (value) => {
    updateRequest((state) => {
      state.markdown = value;
    });
  },
  subscribe,
);

// Composer 编排：整页一个脚本编辑器（kind="composer" 时主区只显示它），
// 内容绑定到当前条目的 composerScript。顶部 slim 工具行给「插入范例」。
const composerEditor = mountScriptEditor(
  viewOf("composer"),
  () => getRequest().composerScript,
  (value) => {
    updateRequest((state) => {
      state.composerScript = value;
    });
  },
  subscribe,
);
mountScriptToolbar(viewOf("composer"), "composer", composerEditor);

let currentKind: RequestKind = getRequest().kind;
let currentRequest = getRequest();
let tabsKey = JSON.stringify(requestTabs(currentKind));

const requestTabsHandle = createTabs(reqTabsHost, requestTabs(currentKind), firstTab(currentKind), showRequestTab);
showRequestTab(firstTab(currentKind));

// gRPC、Composer 与 WS/TCP/Socket.IO 共用同一块响应区（#socket-res-*）：按 kind
// 挂对应面板。换面板时先卸载上一个（它退订并清空 DOM），日志各自留在自己的会话里。
let responsePanel: "socket" | "grpc" | "composer" | null = null;
let disposeResponsePanel: (() => void) | null = null;

function syncResponsePanel(kind: RequestKind): void {
  const want: "socket" | "grpc" | "composer" =
    kind === "grpc" ? "grpc" : kind === "composer" ? "composer" : "socket";
  if (responsePanel === want) return;
  disposeResponsePanel?.();
  if (want === "grpc") disposeResponsePanel = mountGrpcPanel(socketResTabs, socketResContent);
  else if (want === "composer") disposeResponsePanel = mountComposerPanel(socketResTabs, socketResContent);
  else disposeResponsePanel = mountSocketPanel(socketResTabs, socketResContent);
  responsePanel = want;
}

function applyKindLayout(kind: RequestKind): void {
  // Markdown 条目不是请求：请求页签与响应区整块收起，主区只剩全高的
  // Markdown 编辑器；顶栏保留——里面的 URL/发送换成文档名输入框（urlBar 自管）。
  const markdown = kind === "markdown";
  // Composer 编排也没有请求页签（单页脚本），但响应区要留着显示实时控制台。
  reqTabsHost.hidden = markdown || kind === "composer";
  responsePane.hidden = markdown;

  const socket = !markdown && kind !== "http";
  resTabsHost.hidden = socket;
  resContent.hidden = socket;
  assertPanel.hidden = socket;
  socketResTabs.hidden = !socket;
  socketResContent.hidden = !socket;
  responsePane.dataset.kind = kind;
  // 响应区的面板跟着 kind 换（HTTP 下面整块隐藏，不必挂）。
  if (socket) syncResponsePanel(kind);

  if (markdown) {
    // Vditor 工具栏的气泡向上弹出（z-index 很高），会被 #req-panel 的
    // overflow: auto 沿内边距裁掉；文档模式编辑器高度算死、滚动在编辑器内部，
    // 面板不需要滚动条，放开裁剪让气泡完整飘到顶栏上方。
    reqPanel.style.overflow = "visible";
    markdownEditor.ensure();
    for (const [key, view] of views) view.hidden = key !== "markdown";
    return;
  }
  if (reqPanel.style.overflow !== "") reqPanel.style.overflow = "";

  if (kind === "composer") {
    // 单页脚本编辑器：页签整条收起（`[hidden]` 已把它藏掉），只留 composer 视图。
    composerEditor.ensure();
    for (const [key, view] of views) view.hidden = key !== "composer";
    return;
  }

  const defs = requestTabs(kind);
  const key = JSON.stringify(defs);
  if (key !== tabsKey) {
    tabsKey = key;
    requestTabsHandle.setTabs(defs);
    // 换了类型就从该类型的第一个页签开始（HTTP 仍是 Header）。
    requestTabsHandle.select(firstTab(kind));
  }
  // 从 Markdown 切回来时各请求页签处于全 hidden 状态，统一刷回可见性。
  showRequestTab(requestTabsHandle.activeId());
}

applyKindLayout(currentKind);

// kind 变化或整份编辑器被替换（新建 / 载入条目 / 载入工作区）时，活连接先断开。
function syncEditor(): void {
  const request = getRequest();
  const replaced = request !== currentRequest;
  currentRequest = request;
  if (replaced) {
    stopSocketIfActive();
    stopGrpcIfActive();
    // 编排跑在后台且结果只写当前条目：换了编辑器就停掉，迟到的事件按运行号丢弃。
    stopComposerIfActive();
    // 在途的 HTTP/SSE 请求属于上一个编辑器：断开通道联系后它的回调自行丢弃，
    // 停止按钮也只作用于当前编辑器的那次执行。后台执行不再碰工作区（请求在
    // 发送时就入册了），所以晚到的完成既不改界面也不写别的请求/工作区。
    if (inFlight !== null && inFlight.editor !== request) {
      inFlight = null;
      setStreamChannel(null);
      resetResponse();
    }
  }
  if (request.kind === currentKind) return;
  currentKind = request.kind;
  stopSocketIfActive();
  stopGrpcIfActive();
  stopComposerIfActive();
  applyKindLayout(currentKind);
  // 两套响应区共用一个错误条：换类型后上一类请求的错误不该还挂着。
  if (getResponse().error !== null) updateResponse({ error: null });
}

subscribe(syncEditor);

mountResponseView(resTabsHost, resContent);
mountAssertionsPanel(assertPanel);
// 响应区面板（socket / gRPC）由 applyKindLayout 按 kind 挂载。
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

// gRPC 的自动入册同一条规则（open 收一次、closed 再刷新一次），但归属判断用
// 执行隔离的那把尺：会话记着发起调用时的编辑器对象，换条目/载入工作区之后
// 迟到的会话通知既不改界面也不写工作区。
let lastGrpcStatus = getGrpcSession().status;

subscribeGrpc(() => {
  const session = getGrpcSession();
  const status = session.status;
  if (status === lastGrpcStatus) return;
  lastGrpcStatus = status;
  if (session.editor !== getRequest()) return;
  if (status === "open") {
    rememberCurrentRequest();
    return;
  }
  if (status !== "closed") return;
  const request = getRequest();
  if (request.kind === "grpc" && request.url.trim() === session.target) rememberCurrentRequest();
});

// 拖入 .band 由原生层接管，页面不消费 DOM drop 事件：订阅一次拿绝对路径。
// 落点落在 form-data 的文件控件上时改为把路径填进那一行，其余情况照旧打开工作区。
// 纯浏览器调试环境没有原生拖放（onFileDrop 会抛），订阅不上不影响其余界面。
try {
  onFileDrop((drop) => {
    if (dropOntoFormField(drop.x, drop.y, drop.paths)) return;
    void openDroppedWorkspace(drop.paths);
  });
} catch {
  // 非 MyGo 环境：跳过原生拖放订阅。
}

const settingsDialog = mountSettings();

mountWorkspaceSidebar(sidebar, {
  open: () => void openWorkspace(),
  save: () => void saveWorkspace(false),
  saveAs: () => void saveWorkspace(true),
  newWorkspace: () => guardUnsaved(startNewWorkspace, NEW_WORKSPACE_GUARD),
  newRequest: (kind) => {
    // Markdown 文档与 Composer 编排创建即入册（工作区列表里立即可见、可改名）；
    // 请求保持原语义：执行后才收进内存工作区。
    if (kind === "markdown") {
      newMarkdownDocument();
      return;
    }
    if (kind === "composer") {
      newComposerDocument();
      return;
    }
    newRequest(kind);
  },
  load: (id) => loadEntry(id),
  remove: (id) => deleteEntry(id),
  rename: (id, name) => renameEntry(id, name),
  addFolder: (parentId) => addFolder(parentId),
  removeFolder: (id) => deleteFolder(id),
  renameFolder: (id, name) => renameFolder(id, name),
  moveRequest: (requestId, folderId) => moveRequest(requestId, folderId),
  renameWorkspace: (name) => renameWorkspace(name),
  openSettings: () => settingsDialog.open(),
});

// 语言切换：静态标注由 applyStatic() 统一刷；各组件在自己的 mount 里注册
// onLangChange 重画自己生成的 DOM（返回的是卸载函数，不是刷新函数，所以由
// 组件自己订阅，主文件只负责页签文案——它在这里生成并下发给 createTabs）。
onLangChange(() => {
  applyStatic();
  // Markdown 文档与 Composer 编排没有请求页签，页签文案无需重建。
  if (requestTabs(currentKind).length === 0) return;
  const defs = requestTabs(currentKind);
  tabsKey = JSON.stringify(defs);
  requestTabsHandle.setTabs(defs);
});

function closeWindow(): void {
  void WorkspaceService.closeWindow().catch(() => {
    // 用户已经选择放弃更改，关窗失败没有别的补救办法。
  });
}

// 工作区脏时 Go 否决用户点窗口 X 的关闭并发来这条事件：问是否保存、放弃或返回。
// 事件本身已经代表「脏」，所以这里直接开框，不再过 guardUnsaved 的脏判断。
// 纯浏览器里没有 MyGo 运行时，订阅会抛错（并且让入口模块 errored、连带 Monaco 的
// 动态 chunk 全挂），所以先确认自己在窗口里；桌面端行为不变。
if (isMyGo()) {
  events.workspaceCloseRequested.on(() => {
    confirmDialog.open({
      titleKey: "dialog.unsavedTitle",
      noteKey: "dialog.unsavedNote",
      saveKey: "dialog.save",
      discardKey: "dialog.discard",
      cancelKey: "dialog.cancel",
      proceed: closeWindow,
    });
  });

  // 首次运行：本机还没有身份时先问一次（一次性，不可绕过）。取身份只是读一个
  // 本地文件，失败不阻塞界面——需要身份的功能自己会再报错。
  void IdentityService.get()
    .then((identity) => {
      setCachedIdentity(identity.status === "ready" ? identity : null);
      if (identity.status === "unset") firstRunDialog.open();
    })
    .catch(() => {
      // 读不到身份（配置目录不可写等）：留给设置页与保存弹窗各自报错。
    });
}
