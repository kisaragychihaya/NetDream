import { onLangChange, t } from "../i18n";
import type { SavedSocketEvent } from "../mygo";
import { getRequest, subscribe, updateRequest } from "../state";

let sequence = 0;

// 与 state.ts 同风格：webview 不一定是安全上下文，randomUUID 可能缺席。
function newEventId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  sequence += 1;
  return `event-${Date.now().toString(36)}-${sequence}`;
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

function makeInput(
  placeholder: string,
  value: string,
  field: string,
  onInput: (next: string) => void,
): HTMLInputElement {
  const input = document.createElement("input");
  input.type = "text";
  input.spellcheck = false;
  input.autocomplete = "off";
  input.dataset.field = field;
  input.placeholder = placeholder;
  input.value = value;
  input.addEventListener("input", () => onInput(input.value));
  return input;
}

/**
 * Socket.IO 的「事件」页：事件名 + 监听开关 + 描述。
 *
 * 监听开关只决定实时响应里出现哪些事件（过滤在 socket.ts 收事件时做），
 * 不参与握手，所以勾来勾去不用重连；自己发出的事件用发送区的事件名，不在这里选。
 */
export function mountSocketEventsEditor(host: HTMLElement): () => void {
  host.textContent = "";

  const root = el("div", "sock-events");
  const hint = el("p", "hint");
  // 复用键值表格的栅格与输入样式，列宽由 .sock-events-table 覆盖成四列。
  const table = el("div", "kv-table sock-events-table");
  root.append(hint, table);
  host.append(root);

  let rendered: readonly SavedSocketEvent[] | null = null;

  const render = (): void => {
    const events = getRequest().socketEvents;
    rendered = events;
    hint.textContent = t("socket.eventsHint");
    table.textContent = "";

    const head = el("div", "kv-head");
    for (const label of [
      t("socket.eventListen"),
      t("socket.eventName"),
      t("socket.eventDescription"),
      "",
    ]) {
      head.append(el("span", "", label));
    }
    table.append(head);

    events.forEach((event, index) => {
      const row = el("div", "kv-row");

      const listen = document.createElement("input");
      listen.type = "checkbox";
      listen.className = "kv-enabled";
      listen.checked = event.enabled;
      listen.setAttribute("aria-label", t("socket.eventListen"));
      // 行对象是请求态里的那一份：就地改再通知，表格不重画，勾选不跳焦点。
      listen.addEventListener("change", () => {
        event.enabled = listen.checked;
        updateRequest();
      });

      const name = makeInput(t("socket.eventName"), event.name, "name", (next) => {
        event.name = next;
        // 只改字段不换数组：表格不重建、光标不跳，但要走一次 updateRequest 存盘并通知。
        updateRequest();
      });
      const description = makeInput(t("socket.eventDescription"), event.description, "description", (next) => {
        event.description = next;
        updateRequest();
      });

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "kv-delete";
      remove.title = t("common.delete");
      remove.textContent = "×";
      remove.addEventListener("click", () => {
        // 删行换代数组：订阅里靠引用比较发现变化，表格才会重建。
        updateRequest((state) => {
          state.socketEvents = state.socketEvents.filter((_, position) => position !== index);
        });
      });

      row.append(listen, name, description, remove);
      table.append(row);
    });

    const add = document.createElement("button");
    add.type = "button";
    add.className = "add-row";
    add.textContent = t("socket.eventsAdd");
    add.addEventListener("click", () => {
      updateRequest((state) => {
        state.socketEvents = [
          ...state.socketEvents,
          { id: newEventId(), name: "", enabled: true, description: "" },
        ];
      });
      const rows = table.querySelectorAll<HTMLElement>(".kv-row");
      rows[rows.length - 1]?.querySelector<HTMLInputElement>('input[data-field="name"]')?.focus();
    });
    table.append(add);
  };

  render();

  const unsubscribe = subscribe(() => {
    if (getRequest().socketEvents !== rendered) render();
  });
  // 语言切换：整表重画（行数据在请求态里，输入值不会丢）。
  const unsubscribeLang = onLangChange(() => render());

  return () => {
    unsubscribe();
    unsubscribeLang();
    host.textContent = "";
  };
}
