import { onLangChange, t } from "../i18n";
import {
  METHODS,
  emptyRow,
  getVars,
  getWorkspace,
  setVars,
  subscribe,
  subscribeVars,
  type KVRow,
  type Method,
  type RequestKind,
} from "../state";
import type { SavedRequest } from "../mygo";
import { mountKVTable } from "./kvTable";

export interface WorkspaceHandlers {
  open: () => void;
  save: () => void;
  saveAs: () => void;
  /** 新建一个空工作区（脏时会先走未保存守卫）。 */
  newWorkspace: () => void;
  newRequest: (kind: RequestKind) => void;
  load: (id: string) => void;
  remove: (id: string) => void;
  rename: (id: string, name: string) => void;
  renameWorkspace: (name: string) => void;
  /** 打开设置页（左下角齿轮）。 */
  openSettings: () => void;
}

const METHOD_CLASSES: Record<Method, string> = {
  GET: "m-get",
  POST: "m-post",
  PUT: "m-put",
  PATCH: "m-patch",
  DELETE: "m-delete",
  HEAD: "m-head",
  OPTIONS: "m-options",
};

const UNTITLED_REQUEST = "sidebar.untitledRequest";

const NEW_OPTIONS: { kind: RequestKind; labelKey: string }[] = [
  { kind: "http", labelKey: "sidebar.newHttp" },
  { kind: "websocket", labelKey: "sidebar.newWebSocket" },
  { kind: "socketio", labelKey: "sidebar.newSocketIO" },
  { kind: "tcp", labelKey: "sidebar.newTcp" },
];

/** 连接类请求在列表里的类型徽标；HTTP 用方法名，见 entryRow。 */
const SOCKET_BADGES: Record<string, { className: string; text: string }> = {
  websocket: { className: "m-badge m-ws", text: "WS" },
  socketio: { className: "m-badge m-sio", text: "IO" },
  tcp: { className: "m-badge m-tcp", text: "TCP" },
};

function methodClass(method: string): string {
  const known = METHODS.find((name) => name === method);
  return known === undefined ? "" : METHOD_CLASSES[known];
}

function displayOf(entry: SavedRequest): string {
  return entry.name.trim() === "" ? t(UNTITLED_REQUEST) : entry.name;
}

export function mountWorkspaceSidebar(host: HTMLElement, handlers: WorkspaceHandlers): () => void {
  host.textContent = "";

  // 拖入 .band 由 mygo 原生拖放拿路径（main.ts 订阅 onFileDrop），这里只把
  // 虚线框高亮当作视觉反馈，不再消费 drop 事件。
  const highlight = (event: DragEvent): void => {
    event.preventDefault();
    host.classList.add("ws-drop");
  };
  host.addEventListener("dragenter", highlight);
  host.addEventListener("dragover", highlight);
  host.addEventListener("dragleave", (event) => {
    if (event.relatedTarget instanceof Node && host.contains(event.relatedTarget)) return;
    host.classList.remove("ws-drop");
  });
  host.addEventListener("drop", () => host.classList.remove("ws-drop"));

  let editing = false;
  let lastStructure = "";
  let lastCurrent: string | null = null;
  let menuRoot: HTMLElement | null = null;
  let varsCollapsed = false;
  let disposeVars: (() => void) | null = null;

  const rowsFromVars = (source: Record<string, string>): KVRow[] =>
    Object.entries(source).map(([key, value]) => ({
      key,
      value,
      description: "",
      enabled: true,
    }));

  const recordFromRows = (rows: readonly KVRow[]): Record<string, string> => {
    const next: Record<string, string> = {};
    for (const row of rows) {
      const key = row.key.trim();
      if (key === "") continue;
      next[key] = row.value;
    }
    return next;
  };

  const sameRecord = (left: Record<string, string>, right: Record<string, string>): boolean => {
    const keys = Object.keys(left);
    if (keys.length !== Object.keys(right).length) return false;
    return keys.every((key) => left[key] === right[key]);
  };

  // 变量表：kvTable 编辑的是行数组，提交时折叠成 Record 写进 vars store；
  // 脚本写回的变量（setVars）会让行数组换一份，表格随之重建。
  const mountVarsEditor = (host: HTMLElement): () => void => {
    let rows = rowsFromVars(getVars());

    const commit = (): void => setVars(recordFromRows(rows));

    const apply = (): void => {
      const incoming = getVars();
      // 本表自己写入的记录与 rows 一致：不重建，避免打断正在输入的单元格。
      if (sameRecord(incoming, recordFromRows(rows))) return;
      rows = rowsFromVars(incoming);
    };

    const unsubscribe = subscribeVars(apply);

    const disposeTable = mountKVTable(host, {
      rows: () => rows,
      append: () => {
        rows = [...rows, emptyRow()];
        commit();
      },
      remove: (index) => {
        rows = rows.filter((_, position) => position !== index);
        commit();
      },
      addLabel: () => t("sidebar.addVar"),
      withEnabled: false,
      withDescription: false,
      hint: () => t("sidebar.varsHint"),
      commit,
      subscribe: subscribeVars,
    });

    return () => {
      unsubscribe();
      disposeTable();
    };
  };

  const varsSection = (): HTMLElement => {
    const section = document.createElement("div");
    section.className = "ws-vars";

    const header = document.createElement("button");
    header.type = "button";
    header.className = "ws-section";
    header.setAttribute("aria-expanded", varsCollapsed ? "false" : "true");
    const label = document.createElement("span");
    label.textContent = t("sidebar.vars");
    const chevron = document.createElement("span");
    chevron.className = "chevron";
    chevron.textContent = "▾";
    header.append(label, chevron);

    const body = document.createElement("div");
    body.className = "ws-vars-body";
    body.hidden = varsCollapsed;

    header.addEventListener("click", () => {
      varsCollapsed = !varsCollapsed;
      body.hidden = varsCollapsed;
      header.setAttribute("aria-expanded", varsCollapsed ? "false" : "true");
    });

    disposeVars?.();
    disposeVars = mountVarsEditor(body);

    section.append(header, body);
    return section;
  };

  const closeMenu = (): void => {
    const menu = menuRoot?.querySelector<HTMLElement>(".ws-new-menu");
    if (menu !== null && menu !== undefined) menu.hidden = true;
    menuRoot?.querySelector<HTMLElement>(".ws-new")?.setAttribute("aria-expanded", "false");
  };

  const onDocumentClick = (event: MouseEvent): void => {
    if (!(event.target instanceof Node)) return;
    if (menuRoot !== null && menuRoot.contains(event.target)) return;
    closeMenu();
  };

  const onDocumentKey = (event: KeyboardEvent): void => {
    if (event.key === "Escape") closeMenu();
  };

  document.addEventListener("click", onDocumentClick);
  document.addEventListener("keydown", onDocumentKey);

  const structure = (): string => {
    const state = getWorkspace();
    return JSON.stringify([state.path, state.entries.map((entry) => [entry.id, entry.kind, entry.method])]);
  };

  const liveEntry = (id: string): SavedRequest | undefined =>
    getWorkspace().entries.find((entry) => entry.id === id);

  const beginRename = (anchor: HTMLElement, value: string, apply: (name: string) => void): void => {
    if (editing) return;
    editing = true;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "ws-edit";
    input.value = value;
    input.spellcheck = false;
    input.autocomplete = "off";
    anchor.replaceWith(input);
    input.focus();
    input.select();

    let done = false;
    const finish = (commit: boolean): void => {
      if (done) return;
      done = true;
      editing = false;
      const next = input.value.trim();
      const accepted = commit && next !== "";
      // Restore the label in place: redrawing here would take the button under
      // the pointer away before its click lands.
      anchor.textContent = accepted ? next : value;
      input.replaceWith(anchor);
      if (accepted && next !== value) apply(next);
      else sync();
    };

    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        finish(true);
      } else if (event.key === "Escape") {
        event.preventDefault();
        finish(false);
      }
    });
    input.addEventListener("blur", () => finish(true));
  };

  const entryRow = (entry: SavedRequest, currentId: string | null): HTMLElement => {
    const row = document.createElement("div");
    row.className = entry.id === currentId ? "ws-entry active" : "ws-entry";
    row.dataset.id = entry.id;
    row.title = entry.url;
    row.addEventListener("click", () => {
      if (getWorkspace().currentId === entry.id) return;
      handlers.load(entry.id);
    });

    const badge = document.createElement("span");
    // 旧 .band 里 HTTP 条目的 kind 是空串：查不到就走下面的方法名分支。
    const socketBadge = SOCKET_BADGES[entry.kind];
    if (socketBadge !== undefined) {
      badge.className = socketBadge.className;
      badge.textContent = socketBadge.text;
    } else {
      badge.className = `m-badge ${methodClass(entry.method)}`.trim();
      badge.textContent = entry.method || "GET";
    }

    const name = document.createElement("span");
    name.className = "ws-entry-name";
    name.textContent = displayOf(entry);
    name.addEventListener("dblclick", (event) => {
      event.stopPropagation();
      const live = liveEntry(entry.id);
      if (live === undefined) return;
      beginRename(name, live.name, (next) => handlers.rename(entry.id, next));
    });

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "ws-del";
    remove.textContent = "×";
    remove.title = t("sidebar.deleteRequest");
    remove.addEventListener("click", (event) => {
      event.stopPropagation();
      const live = liveEntry(entry.id);
      if (live !== undefined && window.confirm(t("sidebar.deleteConfirm", { name: displayOf(live) }))) {
        handlers.remove(entry.id);
      }
    });

    row.append(badge, name, remove);
    return row;
  };

  const draw = (): void => {
    const state = getWorkspace();
    lastStructure = structure();
    lastCurrent = state.currentId;
    host.textContent = "";

    const panel = document.createElement("div");
    panel.className = "ws-panel";

    const title = document.createElement("div");
    title.className = "ws-title";
    title.textContent = state.name.trim() === "" ? t("sidebar.untitledWorkspace") : state.name;
    title.title = state.path ?? t("sidebar.unsavedWorkspace");
    title.addEventListener("dblclick", () => beginRename(title, getWorkspace().name, handlers.renameWorkspace));

    const actions = document.createElement("div");
    actions.className = "ws-actions";
    // 四个按钮挤在 240px 的侧栏里：文案一律两字，全名放 title。
    for (const [labelKey, hintKey, run] of [
      ["sidebar.open", "sidebar.openTitle", handlers.open],
      ["sidebar.save", "sidebar.saveTitle", handlers.save],
      ["sidebar.saveAs", "sidebar.saveAsTitle", handlers.saveAs],
      ["sidebar.newWorkspace", "sidebar.newWorkspaceTitle", handlers.newWorkspace],
    ] as const) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "ws-btn";
      button.textContent = t(labelKey);
      button.title = t(hintKey);
      button.addEventListener("click", () => run());
      actions.append(button);
    }

    const create = document.createElement("div");
    create.className = "ws-create";
    const createButton = document.createElement("button");
    createButton.type = "button";
    createButton.className = "ws-new";
    createButton.textContent = t("sidebar.newRequest");
    createButton.setAttribute("aria-haspopup", "menu");
    createButton.setAttribute("aria-expanded", "false");

    const menu = document.createElement("div");
    menu.className = "ws-new-menu";
    menu.setAttribute("role", "menu");
    menu.hidden = true;
    for (const option of NEW_OPTIONS) {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "ws-new-item";
      item.setAttribute("role", "menuitem");
      item.dataset.kind = option.kind;
      item.textContent = t(option.labelKey);
      item.addEventListener("click", () => {
        closeMenu();
        handlers.newRequest(option.kind);
      });
      menu.append(item);
    }
    createButton.addEventListener("click", (event) => {
      event.stopPropagation();
      if (!menu.hidden) {
        closeMenu();
        return;
      }
      menu.hidden = false;
      createButton.setAttribute("aria-expanded", "true");
      menu.querySelector<HTMLButtonElement>(".ws-new-item")?.focus();
    });
    create.append(createButton, menu);
    menuRoot = create;

    panel.append(title, actions, create, varsSection());

    if (state.entries.length === 0) {
      const empty = document.createElement("p");
      empty.className = "ws-empty";
      empty.textContent = t("sidebar.empty");
      panel.append(empty);
    } else {
      const list = document.createElement("div");
      list.className = "ws-list";
      for (const entry of state.entries) list.append(entryRow(entry, state.currentId));
      panel.append(list);
    }

    host.append(panel);

    // 左下角设置入口：不参与工作区结构比较，每次重绘一并重建。
    const footer = document.createElement("div");
    footer.className = "ws-footer";
    const gear = document.createElement("button");
    gear.type = "button";
    gear.className = "ws-settings";
    gear.title = t("common.settings");
    gear.setAttribute("aria-label", t("common.settings"));
    gear.dataset.i18nAttr = "title:common.settings;aria-label:common.settings";
    gear.innerHTML =
      '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>';
    gear.addEventListener("click", () => handlers.openSettings());
    footer.append(gear);
    host.append(footer);
  };

  const sync = (): void => {
    if (editing) return;
    const state = getWorkspace();
    if (structure() !== lastStructure) {
      draw();
      return;
    }

    const title = host.querySelector<HTMLElement>(".ws-title");
    if (title !== null) {
      const name = state.name.trim() === "" ? t("sidebar.untitledWorkspace") : state.name;
      if (title.textContent !== name) title.textContent = name;
      const hint = state.path ?? t("sidebar.unsavedWorkspace");
      if (title.title !== hint) title.title = hint;
    }

    for (const row of host.querySelectorAll<HTMLElement>(".ws-entry")) {
      const entry = state.entries.find((item) => item.id === row.dataset.id);
      if (entry === undefined) continue;
      const name = row.querySelector<HTMLElement>(".ws-entry-name");
      const label = displayOf(entry);
      if (name !== null && name.textContent !== label) name.textContent = label;
      if (row.title !== entry.url) row.title = entry.url;
    }

    if (state.currentId !== lastCurrent) {
      lastCurrent = state.currentId;
      for (const row of host.querySelectorAll<HTMLElement>(".ws-entry")) {
        row.classList.toggle("active", row.dataset.id === state.currentId);
      }
    }
  };

  // beginRename's finish runs after mount, when sync is defined.
  const unsubscribe = subscribe(sync);
  // 语言切换：整块侧栏重画（工作区名、请求名是用户数据，保持原样）。
  const unsubscribeLang = onLangChange(() => {
    if (!editing) draw();
  });

  draw();

  return () => {
    unsubscribe();
    unsubscribeLang();
    document.removeEventListener("click", onDocumentClick);
    document.removeEventListener("keydown", onDocumentKey);
    disposeVars?.();
    disposeVars = null;
    host.textContent = "";
  };
}
