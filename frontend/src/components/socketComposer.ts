import { onLangChange, t } from "../i18n";
import type { SavedMessage } from "../mygo";
import { getSocketSession, sendSocketMessage, subscribeSocket, type SocketStatus } from "../socket";
import { getRequest, subscribe, updateRequest, type MessageType } from "../state";

const MESSAGE_TYPES: { value: MessageType; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "json", label: "JSON" },
  { value: "xml", label: "XML" },
  { value: "html", label: "HTML" },
  { value: "binary", label: "Binary" },
];

const BINARY_PLACEHOLDER = "socket.binaryPlaceholder";
const TEXT_PLACEHOLDER = "socket.textPlaceholder";

const STATUS_NOTES: Record<SocketStatus, string> = {
  idle: "socket.idle",
  connecting: "socket.connecting",
  open: "",
  closed: "socket.closed",
};

interface Draft {
  name: string;
  msgType: MessageType;
  payload: string;
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

let sequence = 0;

function newMessageId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  sequence += 1;
  return `message-${Date.now().toString(36)}-${sequence}`;
}

function messageType(value: string): MessageType {
  for (const option of MESSAGE_TYPES) {
    if (option.value === value) return option.value;
  }
  return "text";
}

function emptyDraft(): Draft {
  return { name: "", msgType: "text", payload: "" };
}

function typeLabel(type: MessageType): string {
  for (const option of MESSAGE_TYPES) {
    if (option.value === type) return option.label;
  }
  return "Text";
}

function draftFrom(message: SavedMessage): Draft {
  return { name: message.name, msgType: messageType(message.msgType), payload: message.payload };
}

export function mountSocketComposer(host: HTMLElement): () => void {
  host.textContent = "";

  let draft = emptyDraft();
  let selectedId: string | null = null;
  let editing = false;

  const root = el("div", "sock-composer");

  const side = el("aside", "sock-tpl");
  const sideHead = el("div", "sock-tpl-head");
  const sideTitle = el("span", "sock-tpl-title", t("socket.templates"));
  sideHead.append(sideTitle);  const addButton = el("button", "sock-tpl-add", t("socket.addTemplate"));
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
  const typeSelect = document.createElement("select");
  typeSelect.className = "sock-type";
  typeSelect.setAttribute("aria-label", t("socket.msgType"));
  for (const option of MESSAGE_TYPES) {
    const node = document.createElement("option");
    node.value = option.value;
    node.textContent = option.label;
    typeSelect.append(node);
  }
  head.append(nameInput, typeSelect);

  const payload = document.createElement("textarea");
  payload.className = "sock-message";
  payload.spellcheck = false;
  payload.autocomplete = "off";

  const actions = el("div", "sock-actions");
  const sendButton = el("button", "sock-send", t("common.send"));
  sendButton.type = "button";
  const storeButton = el("button", "sock-store", t("socket.store"));
  storeButton.type = "button";
  storeButton.title = t("socket.storeTitle");
  const note = el("span", "sock-note");
  actions.append(sendButton, storeButton, note);

  editor.append(head, payload, actions);
  root.append(side, editor);
  host.append(root);

  const applyDraft = (): void => {
    if (nameInput.value !== draft.name) nameInput.value = draft.name;
    if (typeSelect.value !== draft.msgType) typeSelect.value = draft.msgType;
    if (payload.value !== draft.payload) payload.value = draft.payload;
    const placeholder = draft.msgType === "binary" ? t(BINARY_PLACEHOLDER) : t(TEXT_PLACEHOLDER);
    if (payload.placeholder !== placeholder) payload.placeholder = placeholder;
  };

  const syncSend = (): void => {
    const status = getSocketSession().status;
    const live = status === "open";
    sendButton.disabled = !live;
    sendButton.title = live ? t("socket.sendTitle") : t("socket.sendDisabled");
    const noteKey = STATUS_NOTES[status];
    note.textContent = noteKey === "" ? "" : t(noteKey);
    note.dataset.state = status;
  };

  const drawList = (): void => {
    const messages = getRequest().messages;
    listHost.textContent = "";
    if (messages.length === 0) {
      listHost.append(el("p", "sock-tpl-empty", t("socket.noTemplates")));
      return;
    }
    for (const message of messages) {
      const row = el("div", message.id === selectedId ? "sock-tpl-row active" : "sock-tpl-row");
      row.dataset.id = message.id;
      row.title = t("socket.loadTitle");
      const name = el("span", "sock-tpl-name", message.name.trim() === "" ? t("socket.untitled") : message.name);
      const tag = el("span", "sock-tpl-type", typeLabel(messageType(message.msgType)));
      const remove = el("button", "sock-tpl-del", "×");
      remove.type = "button";
      remove.title = t("socket.deleteTitle");

      row.addEventListener("click", () => {
        // 双击改名时两次 click 会先经过这里：只切高亮，不重建列表，
        // 否则输入框会被换掉。
        if (editing) return;
        const live = getRequest().messages.find((item) => item.id === message.id);
        if (live === undefined) return;
        selectedId = live.id;
        draft = draftFrom(live);
        applyDraft();
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
          draft = emptyDraft();
          applyDraft();
        }
        updateRequest((state) => {
          state.messages = state.messages.filter((item) => item.id !== message.id);
        });
        drawList();
      });

      row.append(name, tag, remove);
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
      if (selectedId === id) {
        draft.name = next;
        if (nameInput.value !== next) nameInput.value = next;
      }
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

  const storeDraft = (forceNew: boolean): void => {
    const target = forceNew ? null : selectedId;
    const id = target ?? newMessageId();
    const name = draft.name.trim() === "" ? t("socket.defaultName", { type: typeLabel(draft.msgType) }) : draft.name.trim();
    draft.name = name;
    const message: SavedMessage = { id, name, msgType: draft.msgType, payload: draft.payload };
    selectedId = id;
    updateRequest((state) => {
      const index = state.messages.findIndex((item) => item.id === id);
      state.messages =
        index < 0
          ? [...state.messages, message]
          : state.messages.map((item, position) => (position === index ? message : item));
    });
    applyDraft();
    drawList();
  };

  nameInput.addEventListener("input", () => {
    draft.name = nameInput.value;
  });

  typeSelect.addEventListener("change", () => {
    draft.msgType = messageType(typeSelect.value);
    applyDraft();
  });

  payload.addEventListener("input", () => {
    draft.payload = payload.value;
  });

  sendButton.addEventListener("click", () => {
    sendSocketMessage({ payload: draft.payload, isBinary: draft.msgType === "binary" });
  });

  storeButton.addEventListener("click", () => {
    storeDraft(false);
  });

  addButton.addEventListener("click", () => {
    storeDraft(true);
  });

  let requestRef = getRequest();
  let lastListKey = "";
  let lastStatus = getSocketSession().status;

  const sync = (): void => {
    const request = getRequest();
    if (request !== requestRef) {
      // 换了请求（新建/载入条目）：编辑器回到空白草稿。
      requestRef = request;
      editing = false;
      selectedId = null;
      draft = emptyDraft();
      applyDraft();
      drawList();
    }
    const key = JSON.stringify(request.messages.map((item) => [item.id, item.name]));
    if (key !== lastListKey) {
      lastListKey = key;
      // 列表重建会带走行内改名输入框，别把 editing 永久卡住。
      editing = false;
      drawList();
    }
    if (getSocketSession().status !== lastStatus) syncSend();
    lastStatus = getSocketSession().status;
  };

  applyDraft();
  drawList();
  syncSend();

  const unsubscribeState = subscribe(sync);
  const unsubscribeSocket = subscribeSocket(sync);
  // 语言切换：静态标签、按钮与占位符重取；行内改名时不重画列表，免受输入框被换掉。
  const unsubscribeLang = onLangChange(() => {
    sideTitle.textContent = t("socket.templates");
    addButton.textContent = t("socket.addTemplate");
    addButton.title = t("socket.addTemplateTitle");
    nameInput.placeholder = t("socket.namePlaceholder");
    typeSelect.setAttribute("aria-label", t("socket.msgType"));
    sendButton.textContent = t("common.send");
    storeButton.textContent = t("socket.store");
    storeButton.title = t("socket.storeTitle");
    applyDraft();
    syncSend();
    if (!editing) drawList();
  });

  return () => {
    unsubscribeState();
    unsubscribeSocket();
    unsubscribeLang();
    host.textContent = "";
  };
}

/** 设置页：WS 子协议（Sec-WebSocket-Protocol），TCP 不使用。 */
export function mountSocketSettings(host: HTMLElement): () => void {
  host.textContent = "";

  const root = el("div", "sock-settings");
  const field = el("label", "field");
  const fieldLabel = el("span", "", t("socket.protocolLabel"));
  field.append(fieldLabel);
  const input = document.createElement("input");
  input.type = "text";
  input.spellcheck = false;
  input.autocomplete = "off";
  input.placeholder = t("socket.protocolPlaceholder");
  input.value = getRequest().socketProtocol;
  field.append(input);
  const hint = el("p", "hint", t("socket.protocolHint"));
  root.append(field, hint);
  host.append(root);

  input.addEventListener("input", () => {
    updateRequest((state) => {
      state.socketProtocol = input.value;
    });
  });

  let requestRef = getRequest();
  const unsubscribe = subscribe(() => {
    const request = getRequest();
    if (request === requestRef) return;
    requestRef = request;
    if (input.value !== request.socketProtocol) input.value = request.socketProtocol;
  });
  // 语言切换：标签、占位符与说明重取（输入值是用户数据，保持原样）。
  const unsubscribeLang = onLangChange(() => {
    fieldLabel.textContent = t("socket.protocolLabel");
    input.placeholder = t("socket.protocolPlaceholder");
    hint.textContent = t("socket.protocolHint");
  });

  return () => {
    unsubscribe();
    unsubscribeLang();
    host.textContent = "";
  };
}
