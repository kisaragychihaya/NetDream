import { onLangChange, t } from "../i18n";
import type { KeyValue } from "../mygo";
import {
  clearSocketLog,
  getSocketSession,
  logText,
  SOCKET_LOG_LIMIT,
  subscribeSocket,
  type SocketLogEntry,
  type SocketSession,
  type SocketStatus,
} from "../socket";
import { getRequest, getResponse, subscribe, type RequestKind } from "../state";
import { createTabs, type TabDef } from "./tabs";

type PanelTabId = "log" | "headers";

const STATUS_TEXT: Record<SocketStatus, string> = {
  idle: "socket.idle",
  connecting: "socket.connecting",
  open: "socket.open",
  closed: "socket.closed",
};

const DIRECTION_TEXT: Record<SocketLogEntry["direction"], string> = {
  send: "socket.dirSend",
  receive: "socket.dirReceive",
  system: "socket.dirSystem",
};

interface LogShell {
  badge: HTMLElement;
  protocol: HTMLElement;
  count: HTMLElement;
  banner: HTMLElement;
  scroll: HTMLElement;
  rows: HTMLElement;
}

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  textContent?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className !== undefined) node.className = className;
  if (textContent !== undefined) node.textContent = textContent;
  return node;
}

function timeText(at: number): string {
  const date = new Date(at);
  const pad = (value: number): string => String(value).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function rowFor(entry: SocketLogEntry): HTMLElement {
  const classes = ["sock-row", entry.direction];
  if (entry.tone !== undefined) classes.push(entry.tone);
  const row = el("div", classes.join(" "));
  row.append(el("span", "sock-time", timeText(entry.at)));
  row.append(el("span", "sock-dir", t(DIRECTION_TEXT[entry.direction])));
  if (entry.isBinary) row.append(el("span", "sock-b64", "base64"));
  const payload = document.createElement("pre");
  payload.className = "sock-payload";
  payload.textContent = logText(entry);
  row.append(payload);
  return row;
}

function copyText(text: string): Promise<boolean> {
  if (text === "") return Promise.resolve(false);
  const fallback = (): boolean => {
    const area = document.createElement("textarea");
    area.value = text;
    area.className = "copy-helper";
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
  };
  try {
    return navigator.clipboard
      .writeText(text)
      .then(() => true)
      .catch(() => fallback());
  } catch {
    return Promise.resolve(fallback());
  }
}

function tabsFor(kind: RequestKind, session: SocketSession): TabDef[] {
  const defs: TabDef[] = [{ id: "log", label: t("socket.live") }];
  if (kind !== "tcp") {
    defs.push({ id: "headers", label: t("response.headers"), count: session.handshakeHeaders?.length ?? 0 });
  }
  return defs;
}

export function mountSocketPanel(tabsHost: HTMLElement, contentHost: HTMLElement): () => void {
  let active: PanelTabId = "log";
  let tabKey = "";
  let showing: PanelTabId | null = null;
  let shell: LogShell | null = null;
  let headersRoot: HTMLElement | null = null;
  let headersKey = "";
  let renderedLastId: string | null = null;

  const drawTabs = (defs: TabDef[]): void => {
    tabsHost.textContent = "";
    createTabs(tabsHost, defs, active, (id) => {
      if (id !== "log" && id !== "headers") return;
      if (id === active) return;
      active = id;
      render();
    });
  };

  const buildLog = (): LogShell => {
    contentHost.textContent = "";
    headersRoot = null;

    const root = el("div", "res-tab");
    const toolbar = el("div", "res-toolbar");

    const badge = el("span", "sock-state");
    const protocol = el("span", "meta sock-protocol");
    protocol.hidden = true;
    const count = el("span", "meta sock-count");
    const spacer = el("span", "spacer");
    const clear = el("button", "copy-btn", t("common.clear"));
    clear.type = "button";
    clear.title = t("socket.clearLogTitle");
    const copy = el("button", "copy-btn", t("socket.copyAll"));
    copy.type = "button";
    copy.title = t("socket.copyAllTitle");
    toolbar.append(badge, protocol, count, spacer, clear, copy);

    const banner = el("div", "res-error");
    banner.hidden = true;

    const scroll = el("div", "sock-log res-scroll");
    const rows = el("div", "sock-rows");
    scroll.append(rows);

    root.append(toolbar, banner, scroll);
    contentHost.append(root);
    shell = { badge, protocol, count, banner, scroll, rows };
    renderedLastId = null;

    clear.addEventListener("click", () => clearSocketLog());
    copy.addEventListener("click", () => {
      const session = getSocketSession();
      const text = session.log
        .map((entry) => {
          const tag = t(DIRECTION_TEXT[entry.direction]) + (entry.isBinary ? " base64" : "");
          return `${timeText(entry.at)} [${tag}] ${logText(entry)}`;
        })
        .join("\n");
      void copyText(text).then((ok) => {
        copy.textContent =
          text === "" ? t("socket.noLogs") : ok ? t("common.copied") : t("common.copyFailed");
        window.setTimeout(() => {
          copy.textContent = t("socket.copyAll");
        }, 1200);
      });
    });

    return shell;
  };

  const buildHeaders = (): HTMLElement => {
    contentHost.textContent = "";
    shell = null;
    headersKey = "";
    const root = el("div", "res-tab");
    const scroll = el("div", "res-scroll");
    root.append(scroll);
    contentHost.append(root);
    headersRoot = scroll;
    return scroll;
  };

  const appendRows = (session: SocketSession): void => {
    if (shell === null) return;
    const log = session.log;
    let start = 0;
    if (renderedLastId !== null) {
      const index = log.findIndex((entry) => entry.id === renderedLastId);
      // 清空或重连后日志换了内容：整段重画。
      if (index < 0 && shell.rows.childElementCount > 0) shell.rows.textContent = "";
      else if (index >= 0) start = index + 1;
    }
    for (let index = start; index < log.length; index += 1) {
      const entry = log[index];
      if (entry === undefined) continue;
      shell.rows.append(rowFor(entry));
    }
    // 日志数组封顶后，行也要跟着淘汰，长时间连接才不会把 DOM 撑大。
    while (shell.rows.childElementCount > SOCKET_LOG_LIMIT) shell.rows.firstElementChild?.remove();
    const last = log.length > 0 ? log[log.length - 1] : undefined;
    renderedLastId = last === undefined ? null : last.id;
  };

  const renderLog = (session: SocketSession, kind: RequestKind): void => {
    if (shell === null) buildLog();
    const view = shell;
    if (view === null) return;

    view.badge.textContent = t(STATUS_TEXT[session.status]);
    view.badge.dataset.state = session.status;
    const protocol = session.protocol === "" ? "" : t("socket.protocolTag", { protocol: session.protocol });
    view.protocol.textContent = protocol;
    view.protocol.hidden = protocol === "";
    view.count.textContent = t("socket.logCount", { count: session.log.length });
    view.count.title = kind === "tcp" ? t("socket.tcpConnection") : t("socket.wsConnection");

    const error = getResponse().error;
    view.banner.textContent = error ?? "";
    view.banner.hidden = error === null;

    const atBottom = view.scroll.scrollHeight - view.scroll.scrollTop - view.scroll.clientHeight < 24;
    appendRows(session);
    if (atBottom) view.scroll.scrollTop = view.scroll.scrollHeight;
  };

  const renderHeaders = (session: SocketSession): void => {
    let scroll = headersRoot;
    if (scroll === null) scroll = buildHeaders();
    const headers: KeyValue[] = session.handshakeHeaders ?? [];
    const key = JSON.stringify([session.status, headers]);
    if (key === headersKey) return;
    headersKey = key;
    scroll.textContent = "";
    if (headers.length === 0) {
      scroll.append(
        el(
          "div",
          "empty-state",
          session.status === "open" ? t("socket.noHandshakeHeaders") : t("socket.handshakeHint"),
        ),
      );
      return;
    }
    const wrap = el("div", "table-wrap");
    const table = document.createElement("table");
    table.className = "data-table";
    const thead = document.createElement("thead");
    const headRow = document.createElement("tr");
    for (const label of [t("common.key"), t("common.value")]) {
      const cell = document.createElement("th");
      cell.textContent = label;
      headRow.append(cell);
    }
    thead.append(headRow);
    const tbody = document.createElement("tbody");
    for (const header of headers) {
      const row = document.createElement("tr");
      const key = document.createElement("td");
      key.className = "key-col";
      key.textContent = header.key;
      const value = document.createElement("td");
      value.textContent = header.value;
      row.append(key, value);
      tbody.append(row);
    }
    table.append(thead, tbody);
    wrap.append(table);
    scroll.append(wrap);
  };

  function render(): void {
    const kind = getRequest().kind;
    const session = getSocketSession();

    const defs = tabsFor(kind, session);
    const key = JSON.stringify(defs);
    if (key !== tabKey) {
      tabKey = key;
      if (!defs.some((tab) => tab.id === active)) active = "log";
      drawTabs(defs);
    }

    if (active === "headers") {
      if (showing !== "headers") showing = "headers";
      renderHeaders(session);
      return;
    }
    if (showing !== "log") showing = "log";
    renderLog(session, kind);
  }

  render();

  const unsubscribeState = subscribe(render);
  const unsubscribeSocket = subscribeSocket(render);
  // 语言切换：状态徽标、日志行、握手头都重画；日志本身在 session 里，不丢。
  const unsubscribeLang = onLangChange(() => {
    shell = null;
    headersRoot = null;
    tabKey = "";
    headersKey = "";
    renderedLastId = null;
    showing = null;
    render();
  });

  return () => {
    unsubscribeState();
    unsubscribeSocket();
    unsubscribeLang();
    tabsHost.textContent = "";
    contentHost.textContent = "";
  };
}
