import { subscribe, updateRequest, type FormRow, type KVRow } from "../state";

export interface KVTableOptions {
  rows: () => readonly KVRow[];
  /** 新增一行；由调用方写回数据源并触发一次变化通知。 */
  append: () => void;
  /** 删除第 index 行；同样由调用方写回数据源。 */
  remove: (index: number) => void;
  addLabel: string;
  withContentType?: boolean;
  /** 是否显示启用列（默认显示）。 */
  withEnabled?: boolean;
  /** 是否显示描述列（默认显示）。 */
  withDescription?: boolean;
  canAdd?: boolean;
  canRemove?: boolean;
  hint?: string;
  /** 单元格编辑后的提交动作；默认写回请求态（updateRequest）。 */
  commit?: () => void;
  /** 数据源变化通知；默认订阅请求态。 */
  subscribe?: (listener: () => void) => () => void;
}

function makeInput(
  placeholder: string,
  value: string,
  field: string,
  onInput: (value: string) => void,
  commit: () => void,
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
    commit();
  });
  return input;
}

export function mountKVTable(host: HTMLElement, options: KVTableOptions): () => void {
  host.textContent = "";
  const root = document.createElement("div");
  root.className = "kv-table";
  if (options.withContentType) root.classList.add("with-type");
  if (options.withEnabled === false) root.classList.add("no-enabled");
  if (options.withDescription === false) root.classList.add("no-description");
  host.append(root);

  const commit = options.commit ?? ((): void => updateRequest());
  const withEnabled = options.withEnabled !== false;
  const withDescription = options.withDescription !== false;

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
    const labels: string[] = [];
    if (withEnabled) labels.push("");
    labels.push("键", "值");
    if (options.withContentType) labels.push("内容类型");
    if (withDescription) labels.push("描述");
    labels.push("");
    for (const label of labels) {
      const cell = document.createElement("span");
      cell.textContent = label;
      head.append(cell);
    }
    root.append(head);

    rows.forEach((row, index) => {
      const line = document.createElement("div");
      line.className = "kv-row";

      if (withEnabled) {
        const enabled = document.createElement("input");
        enabled.type = "checkbox";
        enabled.className = "kv-enabled";
        enabled.checked = row.enabled;
        enabled.title = "启用";
        enabled.addEventListener("change", () => {
          row.enabled = enabled.checked;
          commit();
        });
        line.append(enabled);
      }

      const key = makeInput(
        "键",
        row.key,
        "key",
        (next) => {
          row.key = next;
        },
        commit,
      );
      const value = makeInput(
        "值",
        row.value,
        "value",
        (next) => {
          row.value = next;
        },
        commit,
      );

      line.append(key, value);

      if (options.withContentType) {
        const formRow = row as FormRow;
        line.append(
          makeInput(
            "默认",
            formRow.contentType,
            "contentType",
            (next) => {
              formRow.contentType = next;
            },
            commit,
          ),
        );
      }

      if (withDescription) {
        line.append(
          makeInput(
            "描述",
            row.description,
            "description",
            (next) => {
              row.description = next;
            },
            commit,
          ),
        );
      }

      if (options.canRemove === false) {
        line.append(document.createElement("span"));
      } else {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "kv-delete";
        remove.title = "删除";
        remove.textContent = "×";
        remove.addEventListener("click", () => options.remove(index));
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
      options.append();
      focusLastKey();
    });
    root.append(add);
  };

  const unsubscribe = (options.subscribe ?? subscribe)(() => {
    if (options.rows() !== rendered) render();
  });

  render();

  return () => {
    unsubscribe();
    host.textContent = "";
  };
}
