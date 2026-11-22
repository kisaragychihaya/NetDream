import { onLangChange, t } from "../i18n";
import type { SavedMessage } from "../mygo";
import {
  closeGrpcSend,
  currentGrpcMethod,
  currentStreamShape,
  getGrpcSession,
  sendGrpcMessage,
  subscribeGrpc,
  type GrpcStatus,
  type GrpcStreamShape,
} from "../grpc";
import { getRequest, subscribe, updateRequest } from "../state";

const STATUS_KEYS: Record<GrpcStatus, string> = {
  idle: "grpc.statusIdle",
  connecting: "grpc.statusConnecting",
  open: "grpc.statusOpen",
  closed: "grpc.statusClosed",
};

const SHAPE_KEYS: Record<GrpcStreamShape, string> = {
  unary: "grpc.streamUnary",
  server: "grpc.streamServer",
  client: "grpc.streamClient",
  bidi: "grpc.streamBidi",
};

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

let sequence = 0;

function newMessageId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  sequence += 1;
  return `template-${Date.now().toString(36)}-${sequence}`;
}

/**
 * gRPC 的「消息」页：左侧消息模板（SavedMessage 复用，只存 JSON 文本）+ 右侧编辑器。
 * 编辑器直接读写请求态的 raw（顶部「调用」发送的就是它），Binary 不提供。
 *
 * 按钮行为按方法的流式形态（从 grpcMethod + 已获取的方法表推断，未获取过方法表时
 * 按一元处理）：一元/服务端流式没有独立发送按钮（「调用」即发送）；客户端/双向流式
 * 用 [发送] 逐条发、[结束发送] 半关闭。
 */
export function mountGrpcComposer(host: HTMLElement): () => void {
  host.textContent = "";

  let draftName = "";
  let selectedId: string | null = null;
  let editing = false;

  const root = el("div", "sock-composer");

  const side = el("aside", "sock-tpl");
  const sideHead = el("div", "sock-tpl-head");
  const sideTitle = el("span", "sock-tpl-title", t("socket.templates"));
  sideHead.append(sideTitle);
  const addButton = el("button", "sock-tpl-add", t("socket.addTemplate"));
  addButton.type = "button";
  addButton.title = t("socket.addTemplateTitle");
  sideHead.append(addButton);
  const listHost = el("div", "sock-tpl-list");
  side.append(sideHead, listHost);

  const editor = el("div", "sock-edit");
  const head = el("div", "sock-edit-head");
  const nameInput = document.createElement("input");
  nameInput.type = "text";
  nameInput.className = "sock-name";
  nameInput.spellcheck = false;
  nameInput.autocomplete = "off";
  nameInput.placeholder = t("socket.namePlaceholder");
  const shapeBadge = el("span", "grpc-stream");
  head.append(nameInput, shapeBadge);

  const payload = document.createElement("textarea");
  payload.className = "sock-message";
  payload.spellcheck = false;
  payload.autocomplete = "off";
  payload.placeholder = t("grpc.messagePlaceholder");

  const actions = el("div", "sock-actions");
  const sendButton = el("button", "sock-send", t("common.send"));
  sendButton.type = "button";
  const closeButton = el("button", "sock-store", t("grpc.closeSend"));
  closeButton.type = "button";
  closeButton.title = t("grpc.closeSendTitle");
  const storeButton = el("button", "sock-store", t("socket.store"));
  storeButton.type = "button";
  storeButton.title = t("socket.storeTitle");
  const note = el("span", "sock-note");
  actions.append(sendButton, closeButton, storeButton, note);

  editor.append(head, payload, actions);
  root.append(side, editor);
  host.append(root);

  /** 客户端/双向流式才有独立发送：其余形态由顶部的「调用」一次发出。 */
  const streaming = (): boolean => {
    const shape = currentStreamShape();
    return shape === "client" || shape === "bidi";
  };

  const drawList = (): void => {
    const messages = getRequest().messages;
    listHost.textContent = "";
    if (messages.length === 0) {
      listHost.append(el("p", "sock-tpl-empty", t("grpc.noTemplates")));
      return;
    }
    for (const message of messages) {
      const row = el("div", message.id === selectedId ? "sock-tpl-row active" : "sock-tpl-row");
      row.dataset.id = message.id;
      row.title = t("socket.loadTitle");
      const name = el(
        "span",
        "sock-tpl-name",
        message.name.trim() === "" ? t("socket.untitled") : message.name,
      );
      const remove = el("button", "sock-tpl-del", "×");
      remove.type = "button";
      remove.title = t("socket.deleteTitle");

      row.addEventListener("click", () => {
        // 双击改名时两次 click 会先经过这里：只切高亮，不重建列表。
        if (editing) return;
        const live = getRequest().messages.find((item) => item.id === message.id);
        if (live === undefined) return;
        selectedId = live.id;
        draftName = live.name;
        applyDraft();
        if (getRequest().raw !== live.payload) {
          updateRequest((state) => {
            state.raw = live.payload;
          });
        }
        syncActive();
      });

      name.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        beginRename(name, message.id, message.name);
      });

      remove.addEventListener("click", (event) => {
        event.stopPropagation();
        const live = getRequest().messages.find((item) => item.id === message.id);
        if (live === undefined) return;
        if (
          !window.confirm(
            t("socket.deleteConfirm", {
              name: live.name.trim() === "" ? t("socket.untitled") : live.name,
            }),
          )
        ) {
          return;
        }
        if (selectedId === message.id) {
          selectedId = null;
          draftName = "";
          applyDraft();
        }
        updateRequest((state) => {
          state.messages = state.messages.filter((item) => item.id !== message.id);
        });
        drawList();
      });

      row.append(name, remove);
      listHost.append(row);
    }
  };

  const syncActive = (): void => {
    for (const row of listHost.querySelectorAll<HTMLElement>(".sock-tpl-row")) {
      row.classList.toggle("active", row.dataset.id === selectedId);
    }
  };

  const beginRename = (anchor: HTMLElement, id: string, value: string): void => {
    if (editing) return;
    editing = true;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "sock-tpl-edit";
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
      anchor.textContent = accepted ? next : value.trim() === "" ? t("socket.untitled") : value;
      input.replaceWith(anchor);
      if (!accepted || next === value) return;
      if (selectedId === id) draftName = next;
      updateRequest((state) => {
        const message = state.messages.find((item) => item.id === id);
        if (message !== undefined) message.name = next;
      });
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

  // 把编辑器里的内容存成模板：选中模板时覆盖它，「+ 新增」总是新建。
  const storeDraft = (forceNew: boolean): void => {
    const id = forceNew ? null : selectedId;
    const target = id ?? newMessageId();
    const name = draftName.trim() === "" ? t("grpc.defaultTemplateName") : draftName.trim();
    draftName = name;
    const message: SavedMessage = {
      id: target,
      name,
      msgType: "json",
      payload: getRequest().raw,
      event: "",
    };
    selectedId = target;
    updateRequest((state) => {
      const index = state.messages.findIndex((item) => item.id === target);
      state.messages =
        index < 0
          ? [...state.messages, message]
          : state.messages.map((item, position) => (position === index ? message : item));
    });
    applyDraft();
    drawList();
  };

  const applyDraft = (): void => {
    if (nameInput.value !== draftName) nameInput.value = draftName;
    const raw = getRequest().raw;
    if (payload.value !== raw) payload.value = raw;
  };

  const syncActions = (): void => {
    const session = getGrpcSession();
    const live = streaming();
    const shape = currentStreamShape();
    const info = currentGrpcMethod();
    shapeBadge.textContent = t(SHAPE_KEYS[shape]);
    shapeBadge.className = `grpc-stream grpc-stream-${shape}`;
    shapeBadge.title =
      info === null ? "" : t("grpc.types", { input: info.inputType, output: info.outputType });

    // 一元/服务端流式没有独立发送按钮：顶部的「调用」就是发送。
    sendButton.hidden = !live;
    closeButton.hidden = !live;
    // 「连接中」也可发送：客户端流式的响应头往往随最终应答才到，而后端从
    // 注册调用起就在排队接收消息（send/closeSend 的判定同 grpc.ts）。
    const canStream = (session.status === "open" || session.status === "connecting") && !session.halfClosed;
    sendButton.disabled = !canStream;
    closeButton.disabled = !canStream;
    sendButton.title = canStream ? t("grpc.sendTitle") : t("grpc.sendDisabled");
    closeButton.title = session.halfClosed ? t("grpc.sendClosedTitle") : t("grpc.closeSendTitle");
    note.dataset.state = live ? session.status : "hint";
    if (live) note.textContent = t(STATUS_KEYS[session.status]);
    else note.textContent = t(shape === "server" ? "grpc.modeServer" : "grpc.modeUnary");
  };

  nameInput.addEventListener("input", () => {
    draftName = nameInput.value;
  });

  // 消息体就是请求态的 raw：输入即时写回，顶部「调用」发出去的才是当前内容。
  payload.addEventListener("input", () => {
    const next = payload.value;
    if (getRequest().raw === next) return;
    updateRequest((state) => {
      state.raw = next;
    });
  });

  sendButton.addEventListener("click", () => sendGrpcMessage(payload.value));
  closeButton.addEventListener("click", () => closeGrpcSend());
  storeButton.addEventListener("click", () => storeDraft(false));
  addButton.addEventListener("click", () => storeDraft(true));

  let requestRef = getRequest();
  let lastListKey = "";
  let lastSessionKey = "";

  const sessionKey = (): string => {
    const session = getGrpcSession();
    return `${session.status}|${session.halfClosed}|${currentStreamShape()}|${getRequest().grpcMethod}`;
  };

  const sync = (): void => {
    const request = getRequest();
    if (request !== requestRef) {
      // 换了请求（新建/载入条目）：编辑器回到空白草稿，模板列表跟着新条目走。
      requestRef = request;
      editing = false;
      selectedId = null;
      draftName = "";
    }
    applyDraft();
    const key = JSON.stringify(request.messages.map((item) => [item.id, item.name]));
    if (key !== lastListKey) {
      lastListKey = key;
      editing = false;
      drawList();
    }
    const nextSessionKey = sessionKey();
    if (nextSessionKey !== lastSessionKey) {
      lastSessionKey = nextSessionKey;
      syncActions();
    }
  };

  applyDraft();
  drawList();
  lastListKey = JSON.stringify(getRequest().messages.map((item) => [item.id, item.name]));
  syncActions();
  lastSessionKey = sessionKey();

  const unsubscribeState = subscribe(sync);
  const unsubscribeGrpc = subscribeGrpc(sync);
  // 语言切换：静态标签、按钮与占位符重取；行内改名时不重画列表。
  const unsubscribeLang = onLangChange(() => {
    sideTitle.textContent = t("socket.templates");
    addButton.textContent = t("socket.addTemplate");
    addButton.title = t("socket.addTemplateTitle");
    nameInput.placeholder = t("socket.namePlaceholder");
    payload.placeholder = t("grpc.messagePlaceholder");
    sendButton.textContent = t("common.send");
    closeButton.textContent = t("grpc.closeSend");
    storeButton.textContent = t("socket.store");
    storeButton.title = t("socket.storeTitle");
    syncActions();
    if (!editing) drawList();
  });

  return () => {
    unsubscribeState();
    unsubscribeGrpc();
    unsubscribeLang();
    host.textContent = "";
  };
}
