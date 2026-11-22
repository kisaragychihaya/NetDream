/**
 * Composer 编排的响应区（kind="composer"）：实时控制台。
 *
 * 一行 = 时间 + 类型徽标 + 内容：
 * - 日志：脚本 console 输出，按级别着色（log 灰 / warn 橙 / error 红）；
 * - 断言：✓ 绿 / ✗ 红，失败附原因；
 * - 请求：`▶ 条目名` 开始灰行、`✓ 条目名 200 · 123ms` 结束行（状态码徽标 + 耗时），
 *   出错时红行并带错误原因；
 * - 汇总：运行结束的 n 断言 / 通过 / 失败，Reason 非空时红字跟在后面；
 * - 系统：本地校验失败、终止、通道兜底收尾。
 *
 * 结构与 grpcPanel 一致（工具栏 状态徽章 + 行计数 + 清空 + 复制全部、自动滚底、
 * 行上限 500，样式复用 sock-row / sock-dir / sock-code 那套）。
 */
import { onLangChange, t } from "../i18n";
import {
  clearComposerLog,
  COMPOSER_LOG_LIMIT,
  getComposerSession,
  subscribeComposer,
  type ComposerLogEntry,
  type ComposerSession,
  type ComposerStatus,
} from "../composer";
import { getResponse, subscribe } from "../state";
import { createTabs, type TabDef } from "./tabs";

type PanelTabId = "log";

const STATUS_TEXT: Record<ComposerStatus, string> = {
  idle: "composer.statusIdle",
  running: "composer.statusRunning",
  done: "composer.statusDone",
};

const TYPE_TEXT: Record<ComposerLogEntry["type"], string> = {
  log: "composer.typeLog",
  test: "composer.typeTest",
  request: "composer.typeRequest",
  done: "composer.typeDone",
  system: "composer.typeSystem",
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

/** 行正文（不含状态码徽标与耗时：那两个是独立的小徽标）。 */
function contentText(entry: ComposerLogEntry): string {
  switch (entry.type) {
    case "test": {
      const mark = entry.pass === true ? "✓" : "✗";
      const error = entry.error === undefined || entry.error === "" ? "" : ` · ${entry.error}`;
      return `${mark} ${entry.text}${error}`;
    }
    case "request": {
      if (entry.phase === "start") return `▶ ${entry.text}`;
      const failed = entry.error !== undefined && entry.error !== "";
      const error = failed ? ` · ${entry.error}` : "";
      return `${failed ? "✗" : "✓"} ${entry.text}${error}`;
    }
    case "done":
      return t("composer.doneSummary", {
        tests: entry.tests ?? 0,
        passed: entry.passed ?? 0,
        failed: entry.failed ?? 0,
      });
    default:
      return entry.text;
  }
}

/** 复制到剪贴板的纯文本：状态码、耗时、汇总原因都带上，脱离界面也能读。 */
function plainText(entry: ComposerLogEntry): string {
  const parts = [`${timeText(entry.at)} [${t(TYPE_TEXT[entry.type])}] ${contentText(entry)}`];
  if (entry.type === "request" && entry.phase === "end" && entry.status !== undefined) {
    parts.push(String(entry.status));
  }
  if (entry.type === "request" && entry.phase === "end" && entry.timeMS !== undefined) {
    parts.push(`· ${entry.timeMS}ms`);
  }
  if (entry.type === "done" && entry.reason !== undefined && entry.reason !== "") {
    parts.push(`· ${entry.reason}`);
  }
  return parts.join(" ");
}

function rowFor(entry: ComposerLogEntry): HTMLElement {
  const classes = ["sock-row", "cmp-row", entry.type];
  if (entry.tone !== undefined) classes.push(entry.tone);
  const row = el("div", classes.join(" "));
  row.append(el("span", "sock-time", timeText(entry.at)));
  row.append(el("span", "sock-dir", t(TYPE_TEXT[entry.type])));

  const payload = document.createElement("pre");
  payload.className = "sock-payload";
  payload.textContent = contentText(entry);
  row.append(payload);

  // 请求结束行：状态码徽标（2xx/3xx 绿、4xx/5xx 红）+ 耗时。
  if (entry.type === "request" && entry.phase === "end") {
    if (entry.status !== undefined) {
      const code = el("span", "sock-code", String(entry.status));
      code.dataset.ok = entry.status < 400 && (entry.error ?? "") === "" ? "true" : "false";
      row.append(code);
    }
    if (entry.timeMS !== undefined) row.append(el("span", "cmp-time", `${entry.timeMS}ms`));
  }
  // 汇总行的失败原因：只有原因本身红，统计数字保持常规色。
  if (entry.type === "done" && entry.reason !== undefined && entry.reason !== "") {
    row.append(el("span", "cmp-reason", entry.reason));
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

export function mountComposerPanel(tabsHost: HTMLElement, contentHost: HTMLElement): () => void {
  const active: PanelTabId = "log";
  let tabKey = "";
  let shell: LogShell | null = null;
  let renderedLastId: string | null = null;

  const drawTabs = (defs: TabDef[]): void => {
    tabsHost.textContent = "";
    // 只有一个页签（实时控制台），点击无需做任何事：渲染入口只有这一个。
    createTabs(tabsHost, defs, active, () => undefined);
  };

  const buildLog = (): LogShell => {
    contentHost.textContent = "";

    const root = el("div", "res-tab");
    const toolbar = el("div", "res-toolbar");

    const badge = el("span", "sock-state");
    const count = el("span", "meta sock-count");
    count.title = t("composer.console");
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

    clear.addEventListener("click", () => clearComposerLog());
    copy.addEventListener("click", () => {
      const session = getComposerSession();
      const text = session.log.map(plainText).join("\n");
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

  const appendRows = (session: ComposerSession): void => {
    if (shell === null) return;
    const log = session.log;
    let start = 0;
    if (renderedLastId !== null) {
      const index = log.findIndex((entry) => entry.id === renderedLastId);
      // 清空或重新运行后日志换了内容：整段重画。
      if (index < 0 && shell.rows.childElementCount > 0) shell.rows.textContent = "";
      else if (index >= 0) start = index + 1;
    }
    for (let index = start; index < log.length; index += 1) {
      const entry = log[index];
      if (entry === undefined) continue;
      shell.rows.append(rowFor(entry));
    }
    // 日志数组封顶后，行也要跟着淘汰，长时间运行才不会把 DOM 撑大。
    while (shell.rows.childElementCount > COMPOSER_LOG_LIMIT) shell.rows.firstElementChild?.remove();
    const last = log.length > 0 ? log[log.length - 1] : undefined;
    renderedLastId = last === undefined ? null : last.id;
  };

  const render = (): void => {
    const session = getComposerSession();

    const defs: TabDef[] = [{ id: "log", label: t("socket.live") }];
    const key = JSON.stringify(defs);
    if (key !== tabKey) {
      tabKey = key;
      drawTabs(defs);
    }

    if (shell === null) buildLog();
    const view = shell;
    if (view === null) return;

    view.badge.textContent = t(STATUS_TEXT[session.status]);
    view.badge.dataset.state = session.status;
    view.count.textContent = t("socket.logCount", { count: session.log.length });

    const error = getResponse().error;
    view.banner.textContent = error ?? "";
    view.banner.hidden = error === null;

    // 自动滚底：用户没往上翻时才跟到底部（与 socket/grpc 面板一致）。
    const atBottom = view.scroll.scrollHeight - view.scroll.scrollTop - view.scroll.clientHeight < 24;
    appendRows(session);
    if (atBottom) view.scroll.scrollTop = view.scroll.scrollHeight;
  };

  render();

  const unsubscribeState = subscribe(render);
  const unsubscribeComposer = subscribeComposer(render);
  // 语言切换：状态徽标、类型徽标、行正文（汇总/断言）都按当前语言重算；日志本身
  // 在会话里，不丢。
  const unsubscribeLang = onLangChange(() => {
    shell = null;
    tabKey = "";
    renderedLastId = null;
    render();
  });

  return () => {
    unsubscribeState();
    unsubscribeComposer();
    unsubscribeLang();
    tabsHost.textContent = "";
    contentHost.textContent = "";
  };
}
