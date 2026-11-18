import {
  subscribe,
  updateRequest,
  type FormRow,
  type KVRow,
  type RequestState,
} from "../state";

export interface KVTableOptions {
  rows: () => readonly KVRow[];
  append: (state: RequestState) => void;
  remove: (state: RequestState, index: number) => void;
  addLabel: string;
  withContentType?: boolean;
  canAdd?: boolean;
  canRemove?: boolean;
  hint?: string;
}

function makeInput(
  placeholder: string,
  value: string,
  field: string,
  onInput: (value: string) => void,
): HTMLInputElement {
  const input = document.createElement("input");
  input.type = "text";
  input.spellcheck = false;
  input.autocomplete = "off";
  input.dataset.field = field;
  input.placeholder = placeholder;
  input.value = value;
  input.addEventListener("input", () => {
    onInput(input.value);
    updateRequest();
  });
  return input;
}

export function mountKVTable(host: HTMLElement, options: KVTableOptions): () => void {
  host.textContent = "";
  const root = document.createElement("div");
  root.className = "kv-table";
  if (options.withContentType) root.classList.add("with-type");
  host.append(root);

  let rendered: readonly KVRow[] | null = null;

  const focusLastKey = (): void => {
    const rows = root.querySelectorAll<HTMLElement>(".kv-row");
    const last = rows[rows.length - 1];
    last?.querySelector<HTMLInputElement>('input[data-field="key"]')?.focus();
  };

  const render = (): void => {
    const rows = options.rows();
    rendered = rows;
    root.textContent = "";

    if (options.hint) {
      const hint = document.createElement("p");
      hint.className = "hint";
      hint.textContent = options.hint;
      root.append(hint);
    }

    const head = document.createElement("div");
    head.className = "kv-head";
    const labels = ["", "键", "值"];
    if (options.withContentType) labels.push("内容类型");
    labels.push("描述", "");
    for (const label of labels) {
      const cell = document.createElement("span");
      cell.textContent = label;
      head.append(cell);
    }
    root.append(head);

    rows.forEach((row, index) => {
      const line = document.createElement("div");
      line.className = "kv-row";

      const enabled = document.createElement("input");
      enabled.type = "checkbox";
      enabled.className = "kv-enabled";
      enabled.checked = row.enabled;
      enabled.title = "启用";
      enabled.addEventListener("change", () => {
        row.enabled = enabled.checked;
        updateRequest();
      });

      const key = makeInput("键", row.key, "key", (next) => {
        row.key = next;
      });
      const value = makeInput("值", row.value, "value", (next) => {
        row.value = next;
      });

      line.append(enabled, key, value);

      if (options.withContentType) {
        const formRow = row as FormRow;
        line.append(
          makeInput("默认", formRow.contentType, "contentType", (next) => {
            formRow.contentType = next;
          }),
        );
      }

      line.append(
        makeInput("描述", row.description, "description", (next) => {
          row.description = next;
        }),
      );

      if (options.canRemove === false) {
        line.append(document.createElement("span"));
      } else {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "kv-delete";
        remove.title = "删除";
        remove.textContent = "×";
        remove.addEventListener("click", () => {
          updateRequest((state) => options.remove(state, index));
        });
        line.append(remove);
      }

      root.append(line);
    });

    if (options.canAdd === false) return;

    const add = document.createElement("button");
    add.type = "button";
    add.className = "add-row";
    add.textContent = options.addLabel;
    add.addEventListener("click", () => {
      updateRequest((state) => options.append(state));
      focusLastKey();
    });
    root.append(add);
  };

  const unsubscribe = subscribe(() => {
    if (options.rows() !== rendered) render();
  });

  render();

  return () => {
    unsubscribe();
    host.textContent = "";
  };
}
