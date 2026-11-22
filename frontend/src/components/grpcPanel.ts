import { onLangChange, t } from "../i18n";
import type { KeyValue } from "../mygo";
import {
  clearGrpcLog,
  getGrpcSession,
  GRPC_LOG_LIMIT,
  logText,
  subscribeGrpc,
  type GrpcLogEntry,
  type GrpcSession,
  type GrpcStatus,
} from "../grpc";
import { getResponse, subscribe } from "../state";
import { createTabs, type TabDef } from "./tabs";

type PanelTabId = "log" | "headers";

const STATUS_TEXT: Record<GrpcStatus, string> = {
  idle: "grpc.statusIdle",
  connecting: "grpc.statusConnecting",
  open: "grpc.statusOpen",
  closed: "grpc.statusClosed",
};

const DIRECTION_TEXT: Record<GrpcLogEntry["direction"], string> = {
  send: "socket.dirSend",
  receive: "socket.dirReceive",
  system: "socket.dirSystem",
};

interface LogShell {
  badge: HTMLElement;
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

function rowFor(entry: GrpcLogEntry): HTMLElement {
  const classes = ["sock-row", entry.direction];
  if (entry.tone !== undefined) classes.push(entry.tone);
  const row = el("div", classes.join(" "));
  row.append(el("span", "sock-time", timeText(entry.at)));
  row.append(el("span", "sock-dir", t(DIRECTION_TEXT[entry.direction])));
  if (entry.status !== undefined) {
    const code = el("span", "sock-code", entry.status);
    code.dataset.ok = entry.ok === true ? "true" : "false";
    row.append(code);
  }
  const payload = document.createElement("pre");
  payload.className = "sock-payload";
  payload.textContent = logText(entry);
  row.append(payload);
  if (entry.trailers !== undefined) {
    for (const item of entry.trailers) {
      const trailer = el("span", "sock-trailer", `${item.key}: ${item.value}`);
      trailer.title = `${item.key}: ${item.value}`;
      row.append(trailer);
    }
  }
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

function tabsFor(session: GrpcSession): TabDef[] {
  return [
    { id: "log", label: t("socket.live") },
    {
      id: "headers",
      label: t("response.headers"),
      count: session.metadata.length + session.trailers.length,
    },
  ];
}

/**
 * gRPC 的响应区（kind="grpc"）：实时响应 = 会话日志（时间 + 发送/接收徽章 +
 * 结束行的状态码徽标 + 转义 pre；close 行附 trailers），响应头 = 调用元数据与
 * trailers 两组表格。样式类与 socketPanel 一致。
 */
export function mountGrpcPanel(tabsHost: HTMLElement, contentHost: HTMLElement): () => void {
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
    const count = el("span", "meta sock-count");
    count.title = t("grpc.connection");
    const spacer = el("span", "spacer");
    const clear = el("button", "copy-btn", t("common.clear"));
    clear.type = "button";
    clear.title = t("socket.clearLogTitle");
    const copy = el("button", "copy-btn", t("socket.copyAll"));
    copy.type = "button";
    copy.title = t("socket.copyAllTitle");
    toolbar.append(badge, count, spacer, clear, copy);

    const banner = el("div", "res-error");
    banner.hidden = true;

    const scroll = el("div", "sock-log res-scroll");
    const rows = el("div", "sock-rows");
    scroll.append(rows);

    root.append(toolbar, banner, scroll);
    contentHost.append(root);
    shell = { badge, count, banner, scroll, rows };
    renderedLastId = null;

    clear.addEventListener("click", () => clearGrpcLog());
    copy.addEventListener("click", () => {
      const session = getGrpcSession();
      const text = session.log
        .map((entry) => {
          const tags = [t(DIRECTION_TEXT[entry.direction])];
          if (entry.status !== undefined) tags.push(entry.status);
          const lines = [`${timeText(entry.at)} [${tags.join(" ")}] ${logText(entry)}`];
          for (const item of entry.trailers ?? []) lines.push(`${item.key}: ${item.value}`);
          return lines.join("\n");
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

  const appendRows = (session: GrpcSession): void => {
    if (shell === null) return;
    const log = session.log;
    let start = 0;
    if (renderedLastId !== null) {
      const index = log.findIndex((entry) => entry.id === renderedLastId);
      // 清空或重新调用后日志换了内容：整段重画。
      if (index < 0 && shell.rows.childElementCount > 0) shell.rows.textContent = "";
      else if (index >= 0) start = index + 1;
    }
    for (let index = start; index < log.length; index += 1) {
      const entry = log[index];
      if (entry === undefined) continue;
      shell.rows.append(rowFor(entry));
    }
    // 日志数组封顶后，行也要跟着淘汰，长时间调用才不会把 DOM 撑大。
    while (shell.rows.childElementCount > GRPC_LOG_LIMIT) shell.rows.firstElementChild?.remove();
    const last = log.length > 0 ? log[log.length - 1] : undefined;
    renderedLastId = last === undefined ? null : last.id;
  };

  const renderLog = (session: GrpcSession): void => {
    if (shell === null) buildLog();
    const view = shell;
    if (view === null) return;

    view.badge.textContent = t(STATUS_TEXT[session.status]);
    view.badge.dataset.state = session.status;
    view.count.textContent = t("socket.logCount", { count: session.log.length });

    const error = getResponse().error;
    view.banner.textContent = error ?? "";
    view.banner.hidden = error === null;

    const atBottom = view.scroll.scrollHeight - view.scroll.scrollTop - view.scroll.clientHeight < 24;
    appendRows(session);
    if (atBottom) view.scroll.scrollTop = view.scroll.scrollHeight;
  };

  const headersGroup = (
    parent: HTMLElement,
    title: string,
    values: readonly KeyValue[],
    emptyKey: string,
  ): void => {
    parent.append(el("div", "res-group-title", title));
    if (values.length === 0) {
      parent.append(el("div", "empty-state", t(emptyKey)));
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
    for (const item of values) {
      const row = document.createElement("tr");
      const key = document.createElement("td");
      key.className = "key-col";
      key.textContent = item.key;
      const value = document.createElement("td");
      value.textContent = item.value;
      row.append(key, value);
      tbody.append(row);
    }
    table.append(thead, tbody);
    wrap.append(table);
    parent.append(wrap);
  };

  const renderHeaders = (session: GrpcSession): void => {
    let scroll = headersRoot;
    if (scroll === null) scroll = buildHeaders();
    const key = JSON.stringify([session.status, session.metadata, session.trailers]);
    if (key === headersKey) return;
    headersKey = key;
    scroll.textContent = "";

    const started = session.status !== "idle";
    const hasAny = session.metadata.length > 0 || session.trailers.length > 0;
    if (!hasAny) {
      scroll.append(
        el("div", "empty-state", t(started ? "grpc.noMetadata" : "grpc.metadataHint")),
      );
      return;
    }
    headersGroup(scroll, t("grpc.metadata"), session.metadata, "grpc.noMetadata");
    headersGroup(scroll, t("grpc.trailers"), session.trailers, "grpc.noTrailers");
  };

  function render(): void {
    const session = getGrpcSession();

    const defs = tabsFor(session);
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
    renderLog(session);
  }

  render();

  const unsubscribeState = subscribe(render);
  const unsubscribeGrpc = subscribeGrpc(render);
  // 语言切换：状态徽标、日志行、元数据组标题都重画；日志本身在会话里，不丢。
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
    unsubscribeGrpc();
    unsubscribeLang();
    tabsHost.textContent = "";
    contentHost.textContent = "";
  };
}
