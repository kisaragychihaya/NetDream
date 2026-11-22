import { onLangChange, t } from "../i18n";
import { getComposerSession, subscribeComposer } from "../composer";
import { getGrpcSession, subscribeGrpc } from "../grpc";
import { getSocketSession, subscribeSocket } from "../socket";
import {
  getRequest,
  getResponse,
  getWorkspace,
  METHODS,
  renameCurrentDocument,
  subscribe,
  updateRequest,
  type RequestKind,
} from "../state";

export interface UrlBarHandlers {
  send: () => void;
  connect: () => void;
  disconnect: () => void;
  /** gRPC 的「调用」：按当前方法与消息发起一次调用。 */
  grpcCall: () => void;
  /** gRPC 的「停止」：断开这次调用。 */
  grpcStop: () => void;
  /** Composer 的「运行」：跑当前条目的编排脚本。 */
  composerRun: () => void;
  /** Composer 的「终止」：停掉正在跑的那次编排。 */
  composerStop: () => void;
  /** Markdown 模式下「发送」按钮槽位变成保存工作区。 */
  save: () => void;
}

const URL_PLACEHOLDER_KEYS: Record<RequestKind, string> = {
  http: "urlbar.httpPlaceholder",
  websocket: "urlbar.websocketPlaceholder",
  tcp: "urlbar.tcpPlaceholder",
  socketio: "urlbar.socketioPlaceholder",
  grpc: "urlbar.grpcPlaceholder",
  // Markdown / Composer 条目没有 URL 输入框，占位符不会出现，键只为类型完整。
  composer: "urlbar.composerPlaceholder",
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

  // Markdown 文档 / Composer 编排的顶栏是名称输入框：URL/发送对它们没有意义。
  const docName = document.createElement("input");
  docName.id = "doc-name";
  docName.type = "text";
  docName.spellcheck = false;
  docName.autocomplete = "off";
  docName.placeholder = t("markdown.namePlaceholder");
  docName.hidden = true;

  // Composer 的单次请求超时（秒）：复用请求的 timeout 字段，0 = 后端默认 60s。
  const timeoutField = document.createElement("label");
  timeoutField.className = "composer-timeout";
  timeoutField.hidden = true;
  const timeoutLabel = document.createElement("span");
  timeoutLabel.textContent = t("urlbar.composerTimeoutLabel");
  timeoutLabel.dataset.i18n = "urlbar.composerTimeoutLabel";
  const timeoutInput = document.createElement("input");
  timeoutInput.id = "composer-timeout";
  timeoutInput.type = "number";
  timeoutInput.min = "0";
  timeoutInput.step = "1";
  timeoutInput.inputMode = "numeric";
  timeoutInput.placeholder = t("urlbar.composerTimeoutPlaceholder");
  timeoutInput.title = t("urlbar.composerTimeoutTitle");
  timeoutInput.dataset.i18nAttr =
    "title:urlbar.composerTimeoutTitle;placeholder:urlbar.composerTimeoutPlaceholder";
  timeoutField.append(timeoutLabel, timeoutInput);

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

  host.append(method, url, h2c, send, docName, timeoutField);

  const socketKind = (): boolean => {
    const kind = getRequest().kind;
    return kind !== "http" && kind !== "markdown" && kind !== "composer";
  };

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
    renameCurrentDocument(docName.value.trim());
  };

  // 超时输入：只收非负整数，其余按 0（后端默认 60s）处理。
  const commitTimeout = (): void => {
    const next = Number(timeoutInput.value);
    const value = Number.isFinite(next) && next > 0 ? Math.floor(next) : 0;
    updateRequest((state) => {
      state.timeout = value;
    });
  };

  const sync = (): void => {
    const state = getRequest();

    if (state.kind === "markdown" || state.kind === "composer") {
      // 文档模式：URL/方法/h2c 收起，右侧是名称输入框 +（编排才有）超时输入。
      if (!method.hidden) method.hidden = true;
      if (!h2c.hidden) h2c.hidden = true;
      if (!url.hidden) url.hidden = true;
      if (send.hidden) send.hidden = false;
      if (docName.hidden) docName.hidden = false;
      // 名称输入即改名（条目名是唯一真相），提示文案说明空名的显示约定。
      const namePlaceholder = t(
        state.kind === "markdown" ? "markdown.namePlaceholder" : "composer.namePlaceholder",
      );
      if (docName.placeholder !== namePlaceholder) docName.placeholder = namePlaceholder;
      const name = currentName();
      if (docName.value !== name) docName.value = name;

      if (state.kind === "composer") {
        if (timeoutField.hidden) timeoutField.hidden = false;
        // 正在输入的单元格不被回写打断（超时是数字框，中途也会被 sync 覆盖）。
        if (timeoutInput !== document.activeElement && timeoutInput.value !== String(state.timeout)) {
          timeoutInput.value = String(state.timeout);
        }
        const { status } = getComposerSession();
        const running = status === "running";
        send.disabled = false;
        const label = running ? t("common.stop") : t("urlbar.composerRun");
        if (send.textContent !== label) send.textContent = label;
        send.classList.toggle("disconnect", running);
        send.title = running ? t("urlbar.composerStopTitle") : t("urlbar.composerRunTitle");
        return;
      }

      // Markdown 文档：超时输入与文档无关，收起；「发送」槽位是存入工作区。
      if (!timeoutField.hidden) timeoutField.hidden = true;
      send.disabled = false;
      send.classList.remove("disconnect");
      const saveLabel = savedFlash ? t("markdown.saved") : t("common.save");
      if (send.textContent !== saveLabel) send.textContent = saveLabel;
      if (send.title !== t("markdown.saveTitle")) send.title = t("markdown.saveTitle");
      return;
    }

    if (timeoutField.hidden === false) timeoutField.hidden = true;
    if (url.hidden) url.hidden = false;
    if (send.hidden) send.hidden = false;
    if (!docName.hidden) docName.hidden = true;

    const socket = state.kind !== "http";
    if (method.hidden !== socket) method.hidden = socket;
    if (h2c.hidden !== socket) h2c.hidden = socket;
    if (url.placeholder !== t(URL_PLACEHOLDER_KEYS[state.kind])) url.placeholder = t(URL_PLACEHOLDER_KEYS[state.kind]);
    if (url.value !== state.url) url.value = state.url;

    if (socket) {
      if (state.kind === "grpc") {
        // gRPC：连接还在建立或已经建立时按钮是「停止」，其余时候是「调用」。
        const { status } = getGrpcSession();
        const live = status === "connecting" || status === "open";
        send.disabled = false;
        const label = live ? t("common.stop") : t("urlbar.grpcCall");
        if (send.textContent !== label) send.textContent = label;
        send.classList.toggle("disconnect", live);
        send.title = live ? t("urlbar.grpcStopTitle") : t("urlbar.grpcCallTitle");
        return;
      }
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
    const kind = getRequest().kind;
    if (kind === "markdown") {
      handlers.save();
      flashSaved();
      return;
    }
    if (kind === "composer") {
      // 编排：运行中按钮是「终止」，其余时候是「运行」。
      if (getComposerSession().status === "running") handlers.composerStop();
      else handlers.composerRun();
      return;
    }
    if (!socketKind()) {
      handlers.send();
      return;
    }
    if (kind === "grpc") {
      const { status } = getGrpcSession();
      if (status === "connecting" || status === "open") handlers.grpcStop();
      else handlers.grpcCall();
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
    renameCurrentDocument(docName.value);
  });
  docName.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    commitDocName();
    docName.blur();
  });
  docName.addEventListener("blur", () => commitDocName());

  // 超时（秒）：输入即写回；回车/失焦时把「非数字 / 负数」归一成 0。
  timeoutInput.addEventListener("input", () => commitTimeout());
  timeoutInput.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    commitTimeout();
    timeoutInput.blur();
  });
  timeoutInput.addEventListener("blur", () => {
    commitTimeout();
    sync();
  });

  sync();
  const unsubscribe = subscribe(sync);
  const unsubscribeSocket = subscribeSocket(sync);
  const unsubscribeGrpc = subscribeGrpc(sync);
  const unsubscribeComposer = subscribeComposer(sync);
  // 语言切换：按钮文案、占位符与提示都按当前语言重算。
  const unsubscribeLang = onLangChange(() => sync());

  return () => {
    if (flashTimer !== undefined) window.clearTimeout(flashTimer);
    unsubscribe();
    unsubscribeSocket();
    unsubscribeGrpc();
    unsubscribeComposer();
    unsubscribeLang();
    host.textContent = "";
  };
}
