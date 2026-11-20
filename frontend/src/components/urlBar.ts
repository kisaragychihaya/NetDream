import { onLangChange, t } from "../i18n";
import { getSocketSession, subscribeSocket } from "../socket";
import {
  getRequest,
  getResponse,
  getWorkspace,
  METHODS,
  renameCurrentMarkdown,
  subscribe,
  updateRequest,
  type RequestKind,
} from "../state";

export interface UrlBarHandlers {
  send: () => void;
  connect: () => void;
  disconnect: () => void;
  /** Markdown 模式下「发送」按钮槽位变成保存工作区。 */
  save: () => void;
}

const URL_PLACEHOLDER_KEYS: Record<RequestKind, string> = {
  http: "urlbar.httpPlaceholder",
  websocket: "urlbar.websocketPlaceholder",
  tcp: "urlbar.tcpPlaceholder",
  socketio: "urlbar.socketioPlaceholder",
  // Markdown 条目隐藏整个顶栏，占位符不会出现，键只为类型完整。
  markdown: "urlbar.markdownPlaceholder",
};

export function mountUrlBar(host: HTMLElement, handlers: UrlBarHandlers): () => void {
  host.textContent = "";

  const method = document.createElement("select");
  method.id = "method";
  method.setAttribute("aria-label", t("urlbar.method"));
  method.dataset.i18nAttr = "aria-label:urlbar.method";
  for (const name of METHODS) {
    const option = document.createElement("option");
    option.value = name;
    option.textContent = name;
    method.append(option);
  }

  const url = document.createElement("input");
  url.id = "url";
  url.type = "text";
  url.spellcheck = false;
  url.autocomplete = "off";
  url.placeholder = t(URL_PLACEHOLDER_KEYS.http);

  const send = document.createElement("button");
  send.id = "send";
  send.type = "button";
  send.textContent = t("urlbar.send");

  // Markdown 条目的顶栏是文档名：URL/发送对文档没有意义，换成改名输入框。
  const docName = document.createElement("input");
  docName.id = "doc-name";
  docName.type = "text";
  docName.spellcheck = false;
  docName.autocomplete = "off";
  docName.placeholder = t("markdown.namePlaceholder");
  docName.hidden = true;

  const h2c = document.createElement("label");
  h2c.className = "h2c-toggle";
  h2c.title = t("urlbar.h2cTitle");
  h2c.dataset.i18nAttr = "title:urlbar.h2cTitle";
  const h2cBox = document.createElement("input");
  h2cBox.type = "checkbox";
  h2cBox.id = "http2";
  const h2cLabel = document.createElement("span");
  h2cLabel.textContent = "h2c";
  h2c.append(h2cBox, h2cLabel);

  host.append(method, url, h2c, send, docName);

  const socketKind = (): boolean => getRequest().kind !== "http";

  // 「保存」点完闪一下文案：文档收进了工作区，但还没写 .band 文件。
  let savedFlash = false;
  let flashTimer: number | undefined;

  const flashSaved = (): void => {
    savedFlash = true;
    if (flashTimer !== undefined) window.clearTimeout(flashTimer);
    flashTimer = window.setTimeout(() => {
      savedFlash = false;
      flashTimer = undefined;
      sync();
    }, 1500);
    sync();
  };

  // 当前条目（Markdown 文档名绑定到工作区条目，请求编辑态里没有名字字段）。
  const currentName = (): string => {
    const workspace = getWorkspace();
    const entry =
      workspace.currentId === null
        ? undefined
        : workspace.entries.find((item) => item.id === workspace.currentId);
    return entry?.name ?? "";
  };

  const commitDocName = (): void => {
    renameCurrentMarkdown(docName.value.trim());
  };

  const sync = (): void => {
    const state = getRequest();

    if (state.kind === "markdown") {
      // 文档模式：URL/方法收起，「发送」槽位换成保存工作区，旁边是文档名输入框。
      if (!method.hidden) method.hidden = true;
      if (!h2c.hidden) h2c.hidden = true;
      if (!url.hidden) url.hidden = true;
      if (send.hidden) send.hidden = false;
      send.disabled = false;
      send.classList.remove("disconnect");
      const saveLabel = savedFlash ? t("markdown.saved") : t("common.save");
      if (send.textContent !== saveLabel) send.textContent = saveLabel;
      if (send.title !== t("markdown.saveTitle")) send.title = t("markdown.saveTitle");
      if (docName.hidden) docName.hidden = false;
      if (docName.placeholder !== t("markdown.namePlaceholder")) {
        docName.placeholder = t("markdown.namePlaceholder");
      }
      const name = currentName();
      if (docName.value !== name) docName.value = name;
      return;
    }

    if (url.hidden) url.hidden = false;
    if (send.hidden) send.hidden = false;
    if (!docName.hidden) docName.hidden = true;

    const socket = state.kind !== "http";
    if (method.hidden !== socket) method.hidden = socket;
    if (h2c.hidden !== socket) h2c.hidden = socket;
    if (url.placeholder !== t(URL_PLACEHOLDER_KEYS[state.kind])) url.placeholder = t(URL_PLACEHOLDER_KEYS[state.kind]);
    if (url.value !== state.url) url.value = state.url;

    if (socket) {
      const { status } = getSocketSession();
      const live = status === "connecting" || status === "open";
      send.disabled = false;
      const label = live ? t("urlbar.disconnect") : t("urlbar.connect");
      if (send.textContent !== label) send.textContent = label;
      send.classList.toggle("disconnect", live);
      send.title =
        status === "connecting"
          ? t("urlbar.cancelConnect")
          : live
            ? t("urlbar.disconnectTitle")
            : t("urlbar.connectTitle");
      return;
    }

    if (method.value !== state.method) method.value = state.method;
    method.dataset.method = method.value;
    if (h2cBox.checked !== state.http2) h2cBox.checked = state.http2;
    const { sending, streaming } = getResponse();
    send.disabled = sending;
    const label = streaming ? t("urlbar.receiving") : sending ? t("urlbar.sending") : t("urlbar.send");
    if (send.textContent !== label) send.textContent = label;
    send.classList.remove("disconnect");
    send.title = "";
  };

  const primary = (): void => {
    if (getRequest().kind === "markdown") {
      handlers.save();
      flashSaved();
      return;
    }
    if (!socketKind()) {
      handlers.send();
      return;
    }
    const { status } = getSocketSession();
    if (status === "connecting" || status === "open") handlers.disconnect();
    else handlers.connect();
  };

  h2cBox.addEventListener("change", () => {
    updateRequest((state) => {
      state.http2 = h2cBox.checked;
    });
  });

  method.addEventListener("change", () => {
    const next = METHODS.find((name) => name === method.value);
    if (!next) return;
    updateRequest((state) => {
      state.method = next;
    });
  });

  url.addEventListener("input", () => {
    updateRequest((state) => {
      state.url = url.value;
    });
  });

  url.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    primary();
  });

  send.addEventListener("click", () => primary());

  // 文档名：输入即改名（条目名是唯一真相），回车/失焦时去掉首尾空格。
  // 文档尚未入册时（重启恢复的空工作区）改名会顺手把它收编进列表。
  docName.addEventListener("input", () => {
    renameCurrentMarkdown(docName.value);
  });
  docName.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    commitDocName();
    docName.blur();
  });
  docName.addEventListener("blur", () => commitDocName());

  sync();
  const unsubscribe = subscribe(sync);
  const unsubscribeSocket = subscribeSocket(sync);
  // 语言切换：按钮文案、占位符与提示都按当前语言重算。
  const unsubscribeLang = onLangChange(() => sync());

  return () => {
    if (flashTimer !== undefined) window.clearTimeout(flashTimer);
    unsubscribe();
    unsubscribeSocket();
    unsubscribeLang();
    host.textContent = "";
  };
}
