import { getSocketSession, subscribeSocket } from "../socket";
import { getRequest, getResponse, METHODS, subscribe, updateRequest, type RequestKind } from "../state";

export interface UrlBarHandlers {
  send: () => void;
  connect: () => void;
  disconnect: () => void;
}

const URL_PLACEHOLDERS: Record<RequestKind, string> = {
  http: "https://api.example.com/v1/users",
  websocket: "请输入以 ws:// 或 wss:// 开头的地址",
  tcp: "请输入主机:端口，如 127.0.0.1:7000",
};

export function mountUrlBar(host: HTMLElement, handlers: UrlBarHandlers): () => void {
  host.textContent = "";

  const method = document.createElement("select");
  method.id = "method";
  method.setAttribute("aria-label", "请求方法");
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
  url.placeholder = URL_PLACEHOLDERS.http;

  const send = document.createElement("button");
  send.id = "send";
  send.type = "button";
  send.textContent = "发送";

  const h2c = document.createElement("label");
  h2c.className = "h2c-toggle";
  h2c.title = "对 http:// 明文强制 HTTP/2（h2c）；https 始终自动协商";
  const h2cBox = document.createElement("input");
  h2cBox.type = "checkbox";
  h2cBox.id = "http2";
  const h2cLabel = document.createElement("span");
  h2cLabel.textContent = "h2c";
  h2c.append(h2cBox, h2cLabel);

  host.append(method, url, h2c, send);

  const socketKind = (): boolean => getRequest().kind !== "http";

  const sync = (): void => {
    const state = getRequest();
    const socket = state.kind !== "http";
    if (method.hidden !== socket) method.hidden = socket;
    if (h2c.hidden !== socket) h2c.hidden = socket;
    if (url.placeholder !== URL_PLACEHOLDERS[state.kind]) url.placeholder = URL_PLACEHOLDERS[state.kind];
    if (url.value !== state.url) url.value = state.url;

    if (socket) {
      const { status } = getSocketSession();
      const live = status === "connecting" || status === "open";
      send.disabled = false;
      const label = live ? "断开" : "连接";
      if (send.textContent !== label) send.textContent = label;
      send.classList.toggle("disconnect", live);
      send.title = status === "connecting" ? "取消连接" : live ? "断开连接" : "建立连接";
      return;
    }

    if (method.value !== state.method) method.value = state.method;
    method.dataset.method = method.value;
    if (h2cBox.checked !== state.http2) h2cBox.checked = state.http2;
    const { sending, streaming } = getResponse();
    send.disabled = sending;
    const label = streaming ? "接收中…" : sending ? "发送中…" : "发送";
    if (send.textContent !== label) send.textContent = label;
    send.classList.remove("disconnect");
    send.title = "";
  };

  const primary = (): void => {
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

  sync();
  const unsubscribe = subscribe(sync);
  const unsubscribeSocket = subscribeSocket(sync);

  return () => {
    unsubscribe();
    unsubscribeSocket();
    host.textContent = "";
  };
}
