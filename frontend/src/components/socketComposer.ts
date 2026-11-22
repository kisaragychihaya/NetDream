import { onLangChange, t } from "../i18n";
import type { SavedMessage } from "../mygo";
import { mountGrpcSettings } from "./grpcSettings";
import { getSocketSession, sendSocketMessage, subscribeSocket, type SocketStatus } from "../socket";
import { getRequest, subscribe, updateRequest, type MessageType, type RequestKind } from "../state";

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
  /** Socket.IO 模板的事件名；空 = "message"（ws/tcp 忽略）。 */
  event: string;
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
  return { name: "", msgType: "text", payload: "", event: "" };
}

function typeLabel(type: MessageType): string {
  for (const option of MESSAGE_TYPES) {
    if (option.value === type) return option.label;
  }
  return "Text";
}

function draftFrom(message: SavedMessage): Draft {
  return {
    name: message.name,
    msgType: messageType(message.msgType),
    payload: message.payload,
    event: message.event,
  };
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
  const typeSelect = document.createElement("select");
  typeSelect.className = "sock-type";
  typeSelect.setAttribute("aria-label", t("socket.msgType"));
  head.append(nameInput, typeSelect);

  // Socket.IO 专属的发送选项：事件名（空 = message）与 Ack 勾选。
  // ws/tcp 用不上，整行按 kind 显示/隐藏。
  const ioRow = el("div", "sock-io");
  const eventInput = document.createElement("input");
  eventInput.type = "text";
  eventInput.className = "sock-event-name";
  eventInput.spellcheck = false;
  eventInput.autocomplete = "off";
  eventInput.placeholder = t("socket.eventPlaceholder");
  const ackLabel = el("label", "sock-ack");
  const ackBox = document.createElement("input");
  ackBox.type = "checkbox";
  const ackText = el("span", "", t("socket.ack"));
  ackLabel.append(ackBox, ackText);
  ioRow.append(eventInput, ackLabel);

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

  editor.append(head, ioRow, payload, actions);
  root.append(side, editor);
  host.append(root);

  const isSocketIO = (): boolean => getRequest().kind === "socketio";

  // 类型下拉按 kind 重建：Socket.IO 的二进制附件没实现，选项里不出现 Binary。
  const drawTypes = (): void => {
    const socketio = isSocketIO();
    typeSelect.textContent = "";
    for (const option of MESSAGE_TYPES) {
      if (socketio && option.value === "binary") continue;
      const node = document.createElement("option");
      node.value = option.value;
      node.textContent = option.label;
      typeSelect.append(node);
    }
  };

  const applyDraft = (): void => {
    const socketio = isSocketIO();
    // 存成 Binary 的旧模板在 Socket.IO 下调不出去：回落到文本，别让它看起来能发。
    if (socketio && draft.msgType === "binary") draft.msgType = "text";
    if (nameInput.value !== draft.name) nameInput.value = draft.name;
    if (typeSelect.value !== draft.msgType) typeSelect.value = draft.msgType;
    if (eventInput.value !== draft.event) eventInput.value = draft.event;
    if (payload.value !== draft.payload) payload.value = draft.payload;
    ioRow.hidden = !socketio;
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
    const message: SavedMessage = { id, name, msgType: draft.msgType, payload: draft.payload, event: draft.event };
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

  eventInput.addEventListener("input", () => {
    draft.event = eventInput.value;
  });

  sendButton.addEventListener("click", () => {
    const socketio = isSocketIO();
    sendSocketMessage({
      payload: draft.payload,
      isBinary: draft.msgType === "binary",
      event: socketio ? draft.event.trim() : "",
      ack: socketio && ackBox.checked,
    });
  });

  storeButton.addEventListener("click", () => {
    storeDraft(false);
  });

  addButton.addEventListener("click", () => {
    storeDraft(true);
  });

  let requestRef = getRequest();
  let lastKind = getRequest().kind;
  let lastListKey = "";
  let lastStatus = getSocketSession().status;

  const sync = (): void => {
    const request = getRequest();
    if (request !== requestRef) {
      // 换了请求（新建/载入条目，或换了 kind）：编辑器回到空白草稿。
      requestRef = request;
      editing = false;
      selectedId = null;
      draft = emptyDraft();
      lastKind = request.kind;
      // 类型下拉的选项集合跟着 kind 变（Socket.IO 不提供 Binary）。
      drawTypes();
      applyDraft();
      drawList();
    } else if (request.kind !== lastKind) {
      // kind 就地变了（事件名/Ack 行的可见性靠这里跟）：草稿留着，只重排控件。
      lastKind = request.kind;
      drawTypes();
      applyDraft();
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

  drawTypes();
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
    eventInput.placeholder = t("socket.eventPlaceholder");
    ackText.textContent = t("socket.ack");
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

/** Socket.IO 客户端版本：值直接映射 SocketConnectParams.version。 */
const SOCKETIO_VERSIONS: { value: string; labelKey: string }[] = [
  { value: "4", labelKey: "socket.sioVersion4" },
  { value: "3", labelKey: "socket.sioVersion3" },
];

/** 设置页的文案槽：语言切换时按 key 重取，控件不重建（值都在请求态里）。 */
interface TextSlot {
  node: HTMLElement;
  key: string;
  /** 缺省刷 textContent；给了属性名就刷那个属性。 */
  attribute?: "placeholder";
}

/**
 * 设置页（kind 感知）：WS 是子协议（Sec-WebSocket-Protocol）；Socket.IO 是客户端版本
 * 与握手路径；gRPC 交给 grpcSettings.ts（传输 + 描述来源）；TCP 没有设置项。
 * kind 一变整块重画——控件值都存在请求态里，重画不丢。
 */
export function mountSocketSettings(host: HTMLElement): () => void {
  host.textContent = "";

  const root = el("div", "sock-settings");
  host.append(root);

  let slots: TextSlot[] = [];

  const track = (node: HTMLElement, key: string, attribute?: "placeholder"): void => {
    slots.push(attribute === undefined ? { node, key } : { node, key, attribute });
    if (attribute === undefined) node.textContent = t(key);
    else node.setAttribute(attribute, t(key));
  };

  const fieldOf = (labelKey: string, control: HTMLElement): HTMLElement => {
    const field = el("label", "field");
    const label = el("span");
    track(label, labelKey);
    field.append(label, control);
    return field;
  };

  const textInput = (
    className: string,
    placeholderKey: string,
    value: string,
    onInput: (next: string) => void,
  ): HTMLInputElement => {
    const input = document.createElement("input");
    input.type = "text";
    input.className = className;
    input.spellcheck = false;
    input.autocomplete = "off";
    track(input, placeholderKey, "placeholder");
    input.value = value;
    input.addEventListener("input", () => onInput(input.value));
    return input;
  };

  const renderProtocol = (): void => {
    const input = textInput("sock-protocol", "socket.protocolPlaceholder", getRequest().socketProtocol, (next) => {
      updateRequest((state) => {
        state.socketProtocol = next;
      });
    });
    const hint = el("p", "hint");
    track(hint, "socket.protocolHint");
    root.append(fieldOf("socket.protocolLabel", input), hint);
  };

  const renderSocketIO = (): void => {
    const request = getRequest();

    // 版本只影响握手：EIO4（默认）对上 Socket.IO v3/v4/v5，EIO3 对上 v2。
    const version = document.createElement("select");
    version.className = "sock-version";
    for (const option of SOCKETIO_VERSIONS) {
      const node = document.createElement("option");
      node.value = option.value;
      track(node, option.labelKey);
      version.append(node);
    }
    // 空串是「没设过」的默认值，按 EIO4 显示。
    version.value = request.socketIOVersion === "3" ? "3" : "4";
    version.addEventListener("change", () => {
      updateRequest((state) => {
        state.socketIOVersion = version.value;
      });
    });

    // 握手路径留空 = /socket.io/（首尾斜杠由后端补齐）。
    const path = textInput("sock-path", "socket.sioPathPlaceholder", request.socketIOPath, (next) => {
      updateRequest((state) => {
        state.socketIOPath = next;
      });
    });
    const hint = el("p", "hint");
    track(hint, "socket.sioHint");

    root.append(fieldOf("socket.sioVersionLabel", version), fieldOf("socket.sioPathLabel", path), hint);
  };

  const render = (): void => {
    slots = [];
    root.textContent = "";
    const kind = getRequest().kind;
    renderedKind = kind;
    if (kind === "grpc") mountGrpcSettings(root, track);
    else if (kind === "socketio") renderSocketIO();
    else if (kind === "websocket") renderProtocol();
  };

  let requestRef = getRequest();
  let renderedKind: RequestKind = requestRef.kind;
  const unsubscribe = subscribe(() => {
    const request = getRequest();
    // 请求换了（载入条目）或 kind 变了才重画：同一条请求里的输入不该被换掉。
    if (request === requestRef && request.kind === renderedKind) return;
    requestRef = request;
    render();
  });
  // 语言切换：标签、占位符与说明重取（输入值是用户数据，留在请求态里照旧显示）。
  const unsubscribeLang = onLangChange(() => {
    for (const slot of slots) {
      if (slot.attribute === undefined) slot.node.textContent = t(slot.key);
      else slot.node.setAttribute(slot.attribute, t(slot.key));
    }
  });

  render();

  return () => {
    unsubscribe();
    unsubscribeLang();
    host.textContent = "";
  };
}
