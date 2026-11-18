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

const BINARY_PLACEHOLDER = "粘贴 base64 编码的二进制内容，例如 SGVsbG8=";
const TEXT_PLACEHOLDER = "输入要发送的消息内容";

const STATUS_NOTES: Record<SocketStatus, string> = {
  idle: "未连接",
  connecting: "连接中…",
  open: "",
  closed: "已断开",
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
  sideHead.append(el("span", "sock-tpl-title", "消息模板"));
  const addButton = el("button", "sock-tpl-add", "+ 新增");
  addButton.type = "button";
  addButton.title = "把编辑器里的内容存成消息模板";
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
  nameInput.placeholder = "消息名称（可留空自动命名）";
  const typeSelect = document.createElement("select");
  typeSelect.className = "sock-type";
  typeSelect.setAttribute("aria-label", "消息类型");
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
  const sendButton = el("button", "sock-send", "发送");
  sendButton.type = "button";
  const storeButton = el("button", "sock-store", "存为消息");
  storeButton.type = "button";
  storeButton.title = "保存编辑器内容：选中模板时覆盖它，否则新建模板";
  const note = el("span", "sock-note");
  actions.append(sendButton, storeButton, note);

  editor.append(head, payload, actions);
  root.append(side, editor);
  host.append(root);

  const applyDraft = (): void => {
    if (nameInput.value !== draft.name) nameInput.value = draft.name;
    if (typeSelect.value !== draft.msgType) typeSelect.value = draft.msgType;
    if (payload.value !== draft.payload) payload.value = draft.payload;
    const placeholder = draft.msgType === "binary" ? BINARY_PLACEHOLDER : TEXT_PLACEHOLDER;
    if (payload.placeholder !== placeholder) payload.placeholder = placeholder;
  };

  const syncSend = (): void => {
    const status = getSocketSession().status;
    const live = status === "open";
    sendButton.disabled = !live;
    sendButton.title = live ? "发送当前编辑器内容" : "连接建立后才能发送";
    note.textContent = STATUS_NOTES[status];
    note.dataset.state = status;
  };

  const drawList = (): void => {
    const messages = getRequest().messages;
    listHost.textContent = "";
    if (messages.length === 0) {
      listHost.append(el("p", "sock-tpl-empty", "还没有消息模板：填好内容点「+ 新增」保存"));
      return;
    }
    for (const message of messages) {
      const row = el("div", message.id === selectedId ? "sock-tpl-row active" : "sock-tpl-row");
      row.dataset.id = message.id;
      row.title = "点击载入编辑器";
      const name = el("span", "sock-tpl-name", message.name.trim() === "" ? "未命名消息" : message.name);
      const tag = el("span", "sock-tpl-type", typeLabel(messageType(message.msgType)));
      const remove = el("button", "sock-tpl-del", "×");
      remove.type = "button";
      remove.title = "删除消息模板";

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
        if (!window.confirm(`删除消息模板“${live.name.trim() === "" ? "未命名消息" : live.name}”？`)) return;
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
      anchor.textContent = accepted ? next : value.trim() === "" ? "未命名消息" : value;
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
    const name = draft.name.trim() === "" ? `${typeLabel(draft.msgType)} 消息` : draft.name.trim();
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

  return () => {
    unsubscribeState();
    unsubscribeSocket();
    host.textContent = "";
  };
}

/** 设置页：WS 子协议（Sec-WebSocket-Protocol），TCP 不使用。 */
export function mountSocketSettings(host: HTMLElement): () => void {
  host.textContent = "";

  const root = el("div", "sock-settings");
  const field = el("label", "field");
  field.append(el("span", "", "子协议（Sec-WebSocket-Protocol）"));
  const input = document.createElement("input");
  input.type = "text";
  input.spellcheck = false;
  input.autocomplete = "off";
  input.placeholder = "如 graphql-ws, json";
  input.value = getRequest().socketProtocol;
  field.append(input);
  const hint = el("p", "hint", "逗号分隔多个子协议，握手时按顺序协商；TCP 连接不使用。");
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

  return () => {
    unsubscribe();
    host.textContent = "";
  };
}
