import { onLangChange, t } from "../i18n";
import type { APIResponse, SavedExample, ScriptLog, ScriptTest, SSEEvent, SSEOpen } from "../mygo";
import {
  deleteExample,
  getRequest,
  getResponse,
  saveCurrentExample,
  stopStream,
  subscribe,
  type ResponseState,
} from "../state";
import { createTabs, type TabDef, type TabsHandle } from "./tabs";

type ResponseTabId = "body" | "headers" | "cookies" | "actual" | "example" | "console";
type ViewMode = "pretty" | "raw" | "preview";

const TAB_LABELS: { id: ResponseTabId; labelKey: string }[] = [
  { id: "body", labelKey: "response.live" },
  { id: "headers", labelKey: "response.headers" },
  { id: "cookies", labelKey: "response.cookies" },
  { id: "actual", labelKey: "response.actual" },
  { id: "example", labelKey: "response.example" },
  { id: "console", labelKey: "response.console" },
];

const VIEW_MODES: { value: ViewMode; labelKey: string }[] = [
  { value: "pretty", labelKey: "response.pretty" },
  { value: "raw", labelKey: "response.raw" },
  { value: "preview", labelKey: "response.preview" },
];

interface StreamRefs {
  tbody: HTMLElement;
  scroll: HTMLElement;
  count: HTMLElement;
  placeholder: HTMLElement | null;
  rendered: number;
}

export function mountResponseView(tabsHost: HTMLElement, contentHost: HTMLElement): () => void {
  let active: ResponseTabId = "body";
  let mode: ViewMode = "pretty";
  let lastResponse: APIResponse | null = null;
  let lastError: string | null = null;
  let lastSending = false;
  let lastStreaming = false;
  let lastOpen: SSEOpen | null = null;
  let lastStopped = false;
  let lastEventsCount = 0;
  let lastScriptLogs: readonly ScriptLog[] = getResponse().scriptLogs;
  let lastScriptTests: readonly ScriptTest[] = getResponse().scriptTests;
  let lastPreScriptError: string | null = getResponse().preScriptError;
  let lastPostScriptError: string | null = getResponse().postScriptError;
  // 响应示例在编辑器状态（getRequest）里，响应区自己跟：换了就要重画示例页。
  let lastExamples: readonly SavedExample[] = getRequest().examples;
  let streamRefs: StreamRefs | null = null;

  const tabDefs = (state: ResponseState): TabDef[] => {
    const headers = state.open?.headers ?? state.response?.headers;
    return TAB_LABELS.map((tab) => ({
      id: tab.id,
      label: t(tab.labelKey),
      ...(tab.id === "headers" ? { count: headers?.length ?? 0 } : {}),
    }));
  };

  const currentStreamRefs = (): StreamRefs | null => streamRefs;

  const render = (): void => {
    const state = getResponse();
    streamRefs = null;
    contentHost.textContent = "";
    contentHost.append(renderTab(active, state));
    // A detached view has no layout: scroll it to its events once it is shown.
    const refs = currentStreamRefs();
    if (refs !== null) refs.scroll.scrollTop = refs.scroll.scrollHeight;
  };

  const tabs: TabsHandle = createTabs(tabsHost, tabDefs(getResponse()), active, (id) => {
    const next = TAB_LABELS.find((tab) => tab.id === id);
    if (!next) return;
    active = next.id;
    render();
  });
  function renderTab(id: ResponseTabId, state: ResponseState): HTMLElement {
    switch (id) {
      case "body":
        return bodyTab(state);
      case "headers":
        return headersTab(state.response, state.open);
      case "cookies":
        return cookiesTab(state.response);
      case "actual":
        return actualTab(state.response);
      case "console":
        return consoleTab(state);
      case "example":
        return exampleTab();
      default:
        // 页签集合固定六项，走不到；兜底回实时响应。
        return bodyTab(state);
    }
  }

  // 响应示例页：单条示例的只读展示（状态/时间 + 美化/原生/预览 + 复制 + 大叉删除）。
  // examples 是列表结构，目前界面只存一条（[0]），为多示例扩展预留。
  function exampleTab(): HTMLElement {
    const example = getRequest().examples[0];
    if (example === undefined) {
      const root = resTabRoot();
      const scroll = document.createElement("div");
      scroll.className = "res-scroll";
      scroll.append(emptyState(t("response.exampleEmpty")));
      root.append(scroll);
      return root;
    }

    const root = resTabRoot();
    const toolbar = document.createElement("div");
    toolbar.className = "res-toolbar";

    const badge = document.createElement("span");
    badge.className = `status-badge s${Math.floor(example.response.status / 100)}`;
    badge.textContent = example.response.statusText || String(example.response.status);
    toolbar.append(badge);
    if (example.response.proto) toolbar.append(protoText(example.response.proto));
    toolbar.append(metaText(`${example.response.timeMS} ms`));
    toolbar.append(metaText(formatBytes(example.response.sizeBytes)));
    toolbar.append(metaText(example.name.trim() !== "" ? example.name : t("response.example")));
    toolbar.append(metaText(formatTime(example.savedAt)));

    const spacer = document.createElement("span");
    spacer.className = "spacer";
    toolbar.append(spacer);

    toolbar.append(viewModeSeg());
    toolbar.append(copyButton(example.response.body));

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "example-del";
    remove.textContent = "×";
    remove.title = t("response.exampleDelete");
    remove.setAttribute("aria-label", t("response.exampleDelete"));
    remove.dataset.i18nAttr = "title:response.exampleDelete;aria-label:response.exampleDelete";
    remove.addEventListener("click", () => deleteExample(example.id));
    toolbar.append(remove);

    root.append(toolbar, bodyScroll(example.response, null, false));
    return root;
  }

  // 美化 / 原生 / 预览 分段（实时响应与响应示例共用）。
  function viewModeSeg(): HTMLElement {
    const seg = document.createElement("div");
    seg.className = "seg";
    for (const option of VIEW_MODES) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "seg-btn";
      button.classList.toggle("active", mode === option.value);
      button.textContent = t(option.labelKey);
      button.addEventListener("click", () => {
        if (mode === option.value) return;
        mode = option.value;
        render();
      });
      seg.append(button);
    }
    return seg;
  }

  // 复制按钮；body 为 null（还没响应）时禁用。点击后短暂反馈结果。
  function copyButton(body: string | null): HTMLButtonElement {
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "copy-btn";
    copy.textContent = t("common.copy");
    copy.disabled = body === null;
    copy.addEventListener("click", () => {
      if (body === null) return;
      void copyText(body).then((ok) => {
        if (!copy.isConnected) return;
        copy.textContent = ok ? t("common.copied") : t("common.copyFailed");
        window.setTimeout(() => {
          if (copy.isConnected) copy.textContent = t("common.copy");
        }, 1200);
      });
    });
    return copy;
  }

  // 响应体滚动区（错误条 / 空态 / 沙箱预览 / 美化高亮，三处共用）。
  function bodyScroll(response: APIResponse | null, error: string | null, sending: boolean): HTMLElement {
    const scroll = document.createElement("div");
    scroll.className = "res-scroll";

    if (error) {
      const box = document.createElement("div");
      box.className = "res-error";
      box.textContent = error;
      scroll.append(box);
    } else if (!response) {
      scroll.append(emptyState(sending ? t("response.sending") : t("response.emptyHint")));
    } else if (mode === "preview") {
      const frame = document.createElement("iframe");
      frame.className = "res-preview";
      // Scripts may run, but without allow-same-origin the page keeps an
      // opaque origin: it cannot read the app, its storage or its cookies.
      frame.setAttribute("sandbox", "allow-scripts");
      frame.srcdoc = previewDocument(response);
      scroll.append(frame);
    } else {
      const pre = document.createElement("pre");
      pre.className = "res-body";
      if (response.body === "") {
        pre.textContent = t("response.emptyBody");
        pre.classList.add("is-empty");
      } else if (mode === "raw") {
        pre.textContent = response.body;
      } else {
        pre.innerHTML = prettyBody(response.body);
      }
      scroll.append(pre);
    }

    return scroll;
  }

  // 实时响应工具条上的「存为响应示例」图标按钮（无响应时禁用）。
  function saveExampleButton(response: APIResponse | null): HTMLButtonElement {
    const save = document.createElement("button");
    save.type = "button";
    save.className = "save-example-btn";
    save.disabled = response === null;
    save.title = t("response.exampleSave");
    save.setAttribute("aria-label", t("response.exampleSave"));
    save.dataset.i18nAttr = "title:response.exampleSave;aria-label:response.exampleSave";
    save.innerHTML =
      '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg>';
    save.addEventListener("click", () => {
      if (response === null) return;
      saveCurrentExample();
    });
    return save;
  }

  // 控制台 = 前后脚本的 console.* 输出 + 两条脚本错误行。
  function consoleTab(state: ResponseState): HTMLElement {
    const root = resTabRoot();

    const toolbar = document.createElement("div");
    toolbar.className = "res-toolbar";
    toolbar.append(metaText(t("response.logCount", { count: state.scriptLogs.length })));
    const spacer = document.createElement("span");
    spacer.className = "spacer";
    toolbar.append(spacer);
    root.append(toolbar);

    const scroll = document.createElement("div");
    scroll.className = "res-scroll console-scroll";

    const lines: HTMLElement[] = [];
    if (state.preScriptError !== null) {
      lines.push(consoleLine("error", null, t("response.preScriptError", { error: state.preScriptError })));
    }
    for (const log of state.scriptLogs) {
      lines.push(consoleLine(scriptLevel(log.level), log.at, log.text));
    }
    if (state.postScriptError !== null) {
      lines.push(consoleLine("error", null, t("response.postScriptError", { error: state.postScriptError })));
    }

    if (lines.length === 0) {
      scroll.append(emptyState(t("response.noLogs")));
    } else {
      for (const line of lines) scroll.append(line);
    }

    root.append(scroll);
    return root;
  }

  function bodyTab(state: ResponseState): HTMLElement {
    const open = state.open;
    if (open !== null) return streamTab(open, state);
    return regularBodyTab(state.response, state.error, state.sending);
  }

  function regularBodyTab(response: APIResponse | null, error: string | null, sending: boolean): HTMLElement {
    const root = resTabRoot();

    const toolbar = document.createElement("div");
    toolbar.className = "res-toolbar";

    if (response) {
      const badge = document.createElement("span");
      badge.className = `status-badge s${Math.floor(response.status / 100)}`;
      badge.textContent = response.statusText || String(response.status);
      toolbar.append(badge);
      if (response.proto) toolbar.append(protoText(response.proto));
      toolbar.append(metaText(`${response.timeMS} ms`));
      toolbar.append(metaText(formatBytes(response.sizeBytes)));
      if (response.truncated) {
        const truncated = document.createElement("span");
        truncated.className = "badge-warn";
        truncated.textContent = t("response.truncated");
        toolbar.append(truncated);
      }
    } else {
      toolbar.append(
        metaText(
          sending ? t("response.sending") : error ? t("response.requestFailed") : t("response.notSent"),
        ),
      );
    }

    const spacer = document.createElement("span");
    spacer.className = "spacer";
    toolbar.append(spacer);

    toolbar.append(viewModeSeg());
    toolbar.append(saveExampleButton(response));
    toolbar.append(copyButton(response === null ? null : response.body));

    root.append(toolbar);
    root.append(bodyScroll(response, error, sending));
    return root;
  }

  function streamTab(open: SSEOpen, state: ResponseState): HTMLElement {
    const { response, streaming, stopped, events, error } = state;
    const root = resTabRoot();

    const toolbar = document.createElement("div");
    toolbar.className = "res-toolbar";

    const status = response?.status ?? open.status;
    const badge = document.createElement("span");
    badge.className = `status-badge s${Math.floor(status / 100)}`;
    badge.textContent = response?.statusText || open.statusText || String(status);
    toolbar.append(badge);

    const proto = response?.proto || open.proto;
    if (proto) toolbar.append(protoText(proto));

    if (streaming) toolbar.append(metaText(t("response.streaming")));
    else if (response) toolbar.append(metaText(`${response.timeMS} ms`));
    else toolbar.append(metaText(stopped ? t("response.stopped") : t("response.streamEnded")));

    if (response && response.sizeBytes > 0) toolbar.append(metaText(formatBytes(response.sizeBytes)));

    const spacer = document.createElement("span");
    spacer.className = "spacer";
    toolbar.append(spacer);

    const stopButton = document.createElement("button");
    stopButton.type = "button";
    stopButton.className = "stop-btn";
    if (streaming) {
      stopButton.textContent = t("common.stop");
      stopButton.addEventListener("click", () => stopStream());
    } else {
      stopButton.textContent = stopped ? t("response.stopped") : t("response.ended");
      stopButton.disabled = true;
    }
    toolbar.append(stopButton);

    root.append(toolbar);

    const meta = document.createElement("div");
    meta.className = `stream-meta${streaming ? "" : stopped ? " stopped" : " ended"}`;

    const stateLabel = document.createElement("span");
    stateLabel.className = "stream-state";
    const dot = document.createElement("span");
    dot.className = "stream-dot";
    const stateText = document.createElement("span");
    stateText.textContent = streaming
      ? t("response.streamingShort")
      : stopped
        ? t("response.stopped")
        : t("response.streamEnded");
    stateLabel.append(dot, stateText);

    const headersLabel = document.createElement("span");
    headersLabel.textContent = t("response.headerCount", { count: open.headers.length });

    const count = document.createElement("span");
    count.className = "stream-count";
    count.textContent = t("response.eventCount", { count: events.length });

    meta.append(stateLabel, headersLabel, count);
    root.append(meta);

    if (error) {
      const box = document.createElement("div");
      box.className = "res-error";
      box.textContent = error;
      root.append(box);
    }

    const scroll = document.createElement("div");
    scroll.className = "res-scroll stream-scroll";

    const table = document.createElement("table");
    table.className = "data-table stream-table";
    const thead = document.createElement("thead");
    const headRow = document.createElement("tr");
    for (const label of ["#", "ID", t("response.columnEvent"), "Data"]) {
      const cell = document.createElement("th");
      cell.textContent = label;
      headRow.append(cell);
    }
    thead.append(headRow);

    const tbody = document.createElement("tbody");
    table.append(thead, tbody);

    let placeholder: HTMLElement | null = null;
    if (events.length === 0) {
      placeholder = document.createElement("tr");
      placeholder.className = "stream-empty";
      const cell = document.createElement("td");
      cell.colSpan = 4;
      cell.textContent = streaming ? t("response.waitingEvents") : t("response.noEvents");
      placeholder.append(cell);
      tbody.append(placeholder);
    } else {
      for (const event of events) tbody.append(eventRow(event));
    }

    scroll.append(table);
    root.append(scroll);

    streamRefs = { tbody, scroll, count, placeholder, rendered: events.length };
    scroll.scrollTop = scroll.scrollHeight;
    return root;
  }

  function appendStreamEvents(): void {
    const refs = streamRefs;
    if (refs === null) return;
    const events = getResponse().events;
    if (events.length < refs.rendered) {
      render();
      return;
    }
    for (let index = refs.rendered; index < events.length; index += 1) {
      const event = events[index];
      if (event === undefined) continue;
      if (refs.placeholder !== null) {
        refs.placeholder.remove();
        refs.placeholder = null;
      }
      refs.tbody.append(eventRow(event));
    }
    refs.rendered = events.length;
    refs.count.textContent = t("response.eventCount", { count: events.length });
    refs.scroll.scrollTop = refs.scroll.scrollHeight;
  }

  function headersTab(response: APIResponse | null, open: SSEOpen | null): HTMLElement {
    const root = resTabRoot();
    const headers = response?.headers ?? open?.headers;
    if (headers === undefined) {
      root.append(emptyState(t("response.headersEmpty")));
      return root;
    }
    if (headers.length === 0) {
      root.append(emptyState(t("response.noHeaders")));
      return root;
    }
    const rows = headers.map((header) => [header.key, header.value]);
    root.append(dataTable([t("common.key"), t("common.value")], rows, "key-col"));
    return root;
  }

  function cookiesTab(response: APIResponse | null): HTMLElement {
    const root = resTabRoot();
    if (!response) {
      root.append(emptyState(t("response.cookiesEmpty")));
      return root;
    }
    if (response.cookies.length === 0) {
      root.append(emptyState(t("response.noCookies")));
      return root;
    }
    const rows = response.cookies.map((cookie) => [
      cookie.name,
      cookie.value,
      cookie.domain || "-",
      cookie.path || "-",
      cookie.expires || t("response.session"),
      cookie.secure ? t("common.yes") : t("common.no"),
      cookie.httpOnly ? t("common.yes") : t("common.no"),
    ]);
    root.append(
      dataTable(
        [
          t("cookies.name"),
          t("common.value"),
          t("cookies.domain"),
          t("cookies.path"),
          t("cookies.expires"),
          "Secure",
          "HttpOnly",
        ],
        rows,
        "key-col",
      ),
    );
    return root;
  }

  function actualTab(response: APIResponse | null): HTMLElement {
    const root = resTabRoot();
    if (!response) {
      root.append(emptyState(t("response.actualEmpty")));
      return root;
    }

    const view = document.createElement("div");
    view.className = "actual-view";

    view.append(sectionTitle(t("response.requestURL")));
    const url = document.createElement("div");
    url.className = "actual-url";
    url.textContent = response.finalURL || t("common.none");
    view.append(url);

    view.append(sectionTitle(t("response.requestHeaders", { count: response.sentHeaders.length })));
    if (response.sentHeaders.length > 0) {
      const rows = response.sentHeaders.map((header) => [header.key, header.value]);
      view.append(dataTable([t("common.key"), t("common.value")], rows, "key-col"));
    } else {
      view.append(noteText(t("response.noRequestHeaders")));
    }

    view.append(sectionTitle(t("response.requestBody")));
    const body = document.createElement("pre");
    body.className = "res-pre";
    body.textContent = response.sentBody || t("response.noRequestBody");
    view.append(body);

    root.append(view);
    return root;
  }

  const unsubscribe = subscribe(() => {
    const next = getResponse();
    const responseChanged = next.response !== lastResponse;
    const openChanged = next.open !== lastOpen;
    const errorChanged = next.error !== lastError;
    const sendingChanged = next.sending !== lastSending;
    const streamingChanged = next.streaming !== lastStreaming;
    const stoppedChanged = next.stopped !== lastStopped;
    const eventsChanged = next.events.length !== lastEventsCount;
    const logsChanged = next.scriptLogs !== lastScriptLogs;
    const testsChanged = next.scriptTests !== lastScriptTests;
    const scriptErrorChanged =
      next.preScriptError !== lastPreScriptError || next.postScriptError !== lastPostScriptError;
    const nextExamples = getRequest().examples;
    const examplesChanged = nextExamples !== lastExamples;

    lastResponse = next.response;
    lastOpen = next.open;
    lastError = next.error;
    lastSending = next.sending;
    lastStreaming = next.streaming;
    lastStopped = next.stopped;
    lastEventsCount = next.events.length;
    lastScriptLogs = next.scriptLogs;
    lastScriptTests = next.scriptTests;
    lastPreScriptError = next.preScriptError;
    lastPostScriptError = next.postScriptError;
    lastExamples = nextExamples;

    if (
      !responseChanged &&
      !openChanged &&
      !errorChanged &&
      !sendingChanged &&
      !streamingChanged &&
      !stoppedChanged &&
      !eventsChanged &&
      !logsChanged &&
      !testsChanged &&
      !scriptErrorChanged &&
      !examplesChanged
    ) {
      return;
    }

    if (responseChanged || openChanged) tabs.setTabs(tabDefs(next));

    const structural =
      responseChanged ||
      openChanged ||
      errorChanged ||
      sendingChanged ||
      streamingChanged ||
      stoppedChanged ||
      logsChanged ||
      scriptErrorChanged ||
      examplesChanged;
    if (!structural) {
      if (active !== "body") return;
      if (streamRefs !== null) appendStreamEvents();
      else render();
      return;
    }

    render();
  });

  render();

  // 语言切换：页签文案变了要重新下发，内容按当前语言重画（响应都在 state 里）。
  const unsubscribeLang = onLangChange(() => {
    tabs.setTabs(tabDefs(getResponse()));
    render();
  });

  return () => {
    unsubscribe();
    unsubscribeLang();
    tabsHost.textContent = "";
    contentHost.textContent = "";
  };
}

function resTabRoot(): HTMLElement {
  const root = document.createElement("div");
  root.className = "res-tab";
  return root;
}

function emptyState(message: string): HTMLElement {
  const box = document.createElement("div");
  box.className = "empty-state";
  box.textContent = message;
  return box;
}

type ScriptLevel = "log" | "warn" | "error";

function scriptLevel(value: string): ScriptLevel {
  return value === "warn" || value === "error" ? value : "log";
}

const LEVEL_LABELS: Record<ScriptLevel, string> = {
  log: "response.levelLog",
  warn: "response.levelWarn",
  error: "response.levelError",
};

function pad(value: number, width = 2): string {
  return String(value).padStart(width, "0");
}

// 脚本日志的时间戳按本地时钟显示到毫秒。
function formatClock(at: number): string {
  const date = new Date(at);
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

function consoleLine(level: ScriptLevel, at: number | null, text: string): HTMLElement {
  const line = document.createElement("div");
  line.className = `console-line ${level}`;

  const time = document.createElement("span");
  time.className = "console-time";
  time.textContent = at === null ? "—" : formatClock(at);

  const badge = document.createElement("span");
  badge.className = `console-level ${level}`;
  badge.textContent = t(LEVEL_LABELS[level]);

  const body = document.createElement("pre");
  body.className = "console-text";
  body.textContent = text;

  line.append(time, badge, body);
  return line;
}

function metaText(text: string): HTMLElement {
  const span = document.createElement("span");
  span.className = "meta";
  span.textContent = text;
  return span;
}

function protoText(proto: string): HTMLElement {
  const tag = document.createElement("span");
  tag.className = "proto-tag";
  tag.textContent = proto;
  return tag;
}

function eventRow(event: SSEEvent): HTMLTableRowElement {
  const row = document.createElement("tr");

  const n = document.createElement("td");
  n.className = "stream-n";
  n.textContent = String(event.n);

  const id = document.createElement("td");
  id.className = "stream-id";
  id.textContent = event.id || "—";

  const name = document.createElement("td");
  name.className = "stream-event";
  name.textContent = event.event || "message";

  const data = document.createElement("td");
  const pre = document.createElement("pre");
  pre.className = "stream-data";
  pre.textContent = event.data;
  data.append(pre);

  row.append(n, id, name, data);
  return row;
}

function noteText(text: string): HTMLElement {
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = text;
  return note;
}

function sectionTitle(text: string): HTMLElement {
  const heading = document.createElement("h4");
  heading.textContent = text;
  return heading;
}

function dataTable(headers: string[], rows: string[][], firstColumnClass?: string): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "table-wrap";

  const table = document.createElement("table");
  table.className = "data-table";

  const thead = document.createElement("thead");
  const headRow = document.createElement("tr");
  for (const label of headers) {
    const cell = document.createElement("th");
    cell.textContent = label;
    headRow.append(cell);
  }
  thead.append(headRow);

  const tbody = document.createElement("tbody");
  for (const row of rows) {
    const line = document.createElement("tr");
    row.forEach((value, index) => {
      const cell = document.createElement("td");
      cell.textContent = value;
      if (index === 0 && firstColumnClass) cell.className = firstColumnClass;
      line.append(cell);
    });
    tbody.append(line);
  }

  table.append(thead, tbody);
  wrap.append(table);
  return wrap;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

// 示例保存时间的展示格式（跟随系统区域设置）。
function formatTime(at: number): string {
  return new Date(at).toLocaleString();
}

function escapeHTML(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttribute(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

// previewDocument makes the body a standalone page: a <base> pointing at the
// request's URL lets relative resources (css, images, scripts) resolve, which
// srcdoc alone cannot do.
function previewDocument(response: APIResponse): string {
  const base = `<base href="${escapeAttribute(response.finalURL)}">`;
  const html = response.body;
  const head = /<head[^>]*>/i.exec(html);
  if (head?.[0]) {
    const at = head.index + head[0].length;
    return html.slice(0, at) + base + html.slice(at);
  }
  return base + html;
}

// Void elements never get a closing tag, so formatting doesn't indent after
// them; raw-text elements keep their inner text untouched.
const VOID_TAGS = new Set([
  "area", "base", "br", "col", "embed", "hr", "img", "input",
  "link", "meta", "param", "source", "track", "wbr",
]);

export function looksLikeHTML(body: string): boolean {
  return /<\/?[a-z][a-z0-9-]*(\s[^<>]*)?>|<!doctype|<!\!--/i.test(body);
}

interface HTMLToken {
  kind: "comment" | "doctype" | "tag" | "text";
  text: string;
}

function tokenizeHTML(html: string): HTMLToken[] {
  const tokens: HTMLToken[] = [];
  let i = 0;
  while (i < html.length) {
    if (html.startsWith("<!--", i)) {
      const end = html.indexOf("-->", i + 4);
      const stop = end === -1 ? html.length : end + 3;
      tokens.push({ kind: "comment", text: html.slice(i, stop) });
      i = stop;
      continue;
    }
    if (html.startsWith("<!", i)) {
      const end = html.indexOf(">", i + 2);
      const stop = end === -1 ? html.length : end + 1;
      tokens.push({ kind: "doctype", text: html.slice(i, stop) });
      i = stop;
      continue;
    }
    const nextIsTag = html.startsWith("</", i) || (/^<[a-z]/i.test(html.slice(i, i + 2)));
    if (nextIsTag) {
      // Read to ">" but skip ones inside quoted attribute values.
      let j = i + 1;
      let quote = "";
      while (j < html.length) {
        const ch = html[j] ?? "";
        if (quote !== "") {
          if (ch === quote) quote = "";
        } else if (ch === '"' || ch === "'") {
          quote = ch;
        } else if (ch === ">") {
          break;
        }
        j++;
      }
      const stop = Math.min(j + 1, html.length);
      tokens.push({ kind: "tag", text: html.slice(i, stop) });
      i = stop;
      continue;
    }
    const next = html.indexOf("<", i);
    const stop = next === -1 ? html.length : next;
    if (stop === i) {
      // A lone "<" that opens nothing is text, not a tag.
      tokens.push({ kind: "text", text: "<" });
      i += 1;
      continue;
    }
    tokens.push({ kind: "text", text: html.slice(i, stop) });
    i = stop;
  }
  return tokens;
}

function highlightTag(tag: string): string {
  const pattern =
    /(<\s*\/?\s*)([a-zA-Z][\w:-]*)|([\w:-]+)(=)("[^"]*"|'[^']*')|([\w:-]+)(?=\s|\/>|>)|(\/?>)/g;
  let out = "";
  let last = 0;
  for (const match of tag.matchAll(pattern)) {
    const index = match.index ?? 0;
    out += escapeHTML(tag.slice(last, index));
    if (match[1] !== undefined) {
      out += `<span class="html-punct">${escapeHTML(match[1])}</span><span class="html-tagname">${escapeHTML(match[2] ?? "")}</span>`;
    } else if (match[3] !== undefined) {
      out += `<span class="html-attr">${escapeHTML(match[3])}</span><span class="html-punct">${escapeHTML(match[4] ?? "")}</span><span class="html-value">${escapeHTML(match[5] ?? "")}</span>`;
    } else if (match[6] !== undefined) {
      out += `<span class="html-attr">${escapeHTML(match[6])}</span>`;
    } else {
      out += `<span class="html-punct">${escapeHTML(match[7] ?? "")}</span>`;
    }
    last = index + match[0].length;
  }
  out += escapeHTML(tag.slice(last));
  return out;
}

export function formatHTML(html: string): string {
  const tokens = tokenizeHTML(html);
  const lines: string[] = [];
  let depth = 0;
  const indent = (d: number) => "  ".repeat(Math.max(d, 0));

  for (const token of tokens) {
    if (token.kind === "text") {
      const text = token.text.trim();
      if (text === "") continue;
      // Multi-line text (script/style bodies) stays as-is, only the first
      // line is indented.
      const [first = "", ...rest] = text.split("\n");
      lines.push(indent(depth) + escapeHTML(first.trim()));
      for (const extra of rest) lines.push(escapeHTML(extra));
      continue;
    }
    if (token.kind === "comment") {
      lines.push(indent(depth) + `<span class="html-comment">${escapeHTML(token.text)}</span>`);
      continue;
    }
    if (token.kind === "doctype") {
      lines.push(indent(depth) + `<span class="html-doctype">${escapeHTML(token.text)}</span>`);
      continue;
    }
    const nameMatch = /^<\s*\/?\s*([a-zA-Z][\w:-]*)?/.exec(token.text);
    const closing = /^<\s*\//.test(token.text);
    const name = (nameMatch?.[1] ?? "").toLowerCase();
    const selfClosing = /\/\s*>$/.test(token.text);
    if (closing) depth = Math.max(depth - 1, 0);
    lines.push(indent(depth) + highlightTag(token.text));
    if (!closing && !selfClosing && !VOID_TAGS.has(name)) depth++;
  }
  return lines.join("\n");
}

function prettyBody(body: string): string {
  try {
    const parsed: unknown = JSON.parse(body);
    const pretty = JSON.stringify(parsed, null, 2);
    return pretty === undefined ? escapeHTML(body) : highlightJSON(pretty);
  } catch {
    if (looksLikeHTML(body)) return formatHTML(body);
    return escapeHTML(body);
  }
}

function highlightJSON(text: string): string {
  const pattern =
    /("(?:\\u[0-9a-fA-F]{4}|\\[^u]|[^\\"])*")(\s*:)?|\b(?:true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
  let out = "";
  let last = 0;

  for (const match of text.matchAll(pattern)) {
    const index = match.index ?? 0;
    const token = match[0];
    out += escapeHTML(text.slice(last, index));

    const stringPart = match[1];
    const colon = match[2];
    if (stringPart !== undefined && colon !== undefined) {
      out += `<span class="json-key">${escapeHTML(stringPart)}</span>${escapeHTML(colon)}`;
    } else if (stringPart !== undefined) {
      out += `<span class="json-string">${escapeHTML(stringPart)}</span>`;
    } else if (token === "true" || token === "false" || token === "null") {
      out += `<span class="json-bool">${escapeHTML(token)}</span>`;
    } else {
      out += `<span class="json-number">${escapeHTML(token)}</span>`;
    }

    last = index + token.length;
  }

  out += escapeHTML(text.slice(last));
  return out;
}

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return copyWithSelection(text);
  }
}

function copyWithSelection(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "-1000px";
  area.style.left = "-1000px";
  document.body.append(area);
  area.select();

  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }

  area.remove();
  return copied;
}
