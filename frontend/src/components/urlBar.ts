import { onLangChange, t } from "../i18n";
import { getSocketSession, subscribeSocket } from "../socket";
import { getRequest, getResponse, METHODS, subscribe, updateRequest, type RequestKind } from "../state";

export interface UrlBarHandlers {
  send: () => void;
  connect: () => void;
  disconnect: () => void;
}

const URL_PLACEHOLDER_KEYS: Record<RequestKind, string> = {
  http: "urlbar.httpPlaceholder",
  websocket: "urlbar.websocketPlaceholder",
  tcp: "urlbar.tcpPlaceholder",
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

  host.append(method, url, h2c, send);

  const socketKind = (): boolean => getRequest().kind !== "http";

  const sync = (): void => {
    const state = getRequest();
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
  // 语言切换：按钮文案、占位符与提示都按当前语言重算。
  const unsubscribeLang = onLangChange(() => sync());

  return () => {
    unsubscribe();
    unsubscribeSocket();
    unsubscribeLang();
    host.textContent = "";
  };
}
