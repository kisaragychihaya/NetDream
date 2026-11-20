import { onLangChange, t } from "../i18n";
import {
  FORM_FIELD_KINDS,
  subscribe,
  updateRequest,
  type FormFieldKind,
  type FormRow,
  type KVRow,
} from "../state";

export interface KVTableOptions {
  rows: () => readonly KVRow[];
  /** 新增一行；由调用方写回数据源并触发一次变化通知。 */
  append: () => void;
  /** 删除第 index 行；同样由调用方写回数据源。 */
  remove: (index: number) => void;
  /** 新增按钮的文案；用 getter 是因为语言切换后要重取。 */
  addLabel: () => string;
  withContentType?: boolean;
  /** 是否显示启用列（默认显示）。 */
  withEnabled?: boolean;
  /** 是否显示描述列（默认显示）。 */
  withDescription?: boolean;
  /**
   * 是否显示字段类型列（默认不显示）。开启后每行键前多一个「文本/文件」切换，
   * 文件行的值单元格换成文件控件（选路径 + 接收拖放高亮），行数据按 FormRow 读写。
   */
  withFileKind?: boolean;
  /** 文件行点「选择」时的回调；调用方负责打开对话框并写回 filePath。 */
  pickFile?: (index: number) => void;
  canAdd?: boolean;
  canRemove?: boolean;
  hint?: () => string;
  /** 单元格编辑后的提交动作；默认写回请求态（updateRequest）。 */
  commit?: () => void;
  /** 数据源变化通知；默认订阅请求态。 */
  subscribe?: (listener: () => void) => () => void;
}

const KIND_LABELS: Record<FormFieldKind, string> = { text: "common.text", file: "common.file" };

const KIND_TITLES: Record<FormFieldKind, string> = {
  text: "common.textFieldTitle",
  file: "common.fileFieldTitle",
};

/** 路径的末段；Windows 与 POSIX 分隔符都认。 */
function baseName(path: string): string {
  const cut = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  return path.slice(cut + 1);
}

function makeKindSwitch(kind: FormFieldKind, onChange: (kind: FormFieldKind) => void): HTMLElement {
  const group = document.createElement("span");
  group.className = "kv-kind";
  group.setAttribute("role", "radiogroup");
  group.setAttribute("aria-label", t("common.fieldType"));
  for (const option of FORM_FIELD_KINDS) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "kv-kind-option";
    button.textContent = t(KIND_LABELS[option]);
    button.title = t(KIND_TITLES[option]);
    button.setAttribute("aria-pressed", String(option === kind));
    if (option === kind) button.classList.add("active");
    button.addEventListener("click", () => onChange(option));
    group.append(button);
  }
  return group;
}

function makeFileCell(index: number, filePath: string, onPick: () => void): HTMLElement {
  const cell = document.createElement("div");
  cell.className = "kv-file";
  // 拖入路由靠这个属性找落点行（见 main.ts 的 onFileDrop）；按当前 index 写。
  cell.setAttribute("data-file-drop-index", String(index));

  const label = baseName(filePath);
  const name = document.createElement("span");
  name.className = "kv-file-name";
  name.textContent = label === "" ? t("common.noFile") : label;
  name.classList.toggle("empty", label === "");
  name.title = filePath;

  const pick = document.createElement("button");
  pick.type = "button";
  pick.className = "kv-file-pick";
  pick.textContent = t("common.choose");
  pick.title = t("common.chooseFile");
  pick.addEventListener("click", onPick);

  cell.append(name, pick);

  // 与侧栏拖入 .band 同一模式：这里只做视觉反馈，真实路径来自 Go 侧的
  // 文件拖放事件，页面不消费 DOM drop 数据。
  cell.addEventListener("dragover", (event) => {
    event.preventDefault();
    cell.classList.add("drop-target");
  });
  cell.addEventListener("dragleave", (event) => {
    if (event.relatedTarget instanceof Node && cell.contains(event.relatedTarget)) return;
    cell.classList.remove("drop-target");
  });
  cell.addEventListener("drop", () => cell.classList.remove("drop-target"));

  return cell;
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
  if (options.withFileKind === true) root.classList.add("with-file-kind");
  host.append(root);

  const commit = options.commit ?? ((): void => updateRequest());
  const withEnabled = options.withEnabled !== false;
  const withDescription = options.withDescription !== false;
  const withFileKind = options.withFileKind === true;

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
      hint.textContent = options.hint();
      root.append(hint);
    }

    const head = document.createElement("div");
    head.className = "kv-head";
    const labels: string[] = [];
    if (withEnabled) labels.push("");
    if (withFileKind) labels.push(t("common.type"));
    labels.push(t("common.key"), t("common.value"));
    if (options.withContentType) labels.push(t("common.contentType"));
    if (withDescription) labels.push(t("common.description"));
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
        enabled.title = t("common.enabled");
        enabled.addEventListener("change", () => {
          row.enabled = enabled.checked;
          commit();
        });
        line.append(enabled);
      }

      // 文件行按 FormRow 读写类型与路径；其余表格只有 KVRow 的字段。
      const formRow = withFileKind ? (row as FormRow) : null;

      if (formRow !== null) {
        line.append(
          makeKindSwitch(formRow.kind, (next) => {
            if (formRow.kind === next) return;
            formRow.kind = next;
            commit();
            // 类型切换会把值单元格换成文件控件（或换回来），整表跟着重画。
            render();
          }),
        );
      }

      line.append(
        makeInput(
          t("common.key"),
          row.key,
          "key",
          (next) => {
            row.key = next;
          },
          commit,
        ),
      );

      if (formRow !== null && formRow.kind === "file") {
        line.append(makeFileCell(index, formRow.filePath, () => options.pickFile?.(index)));
      } else {
        line.append(
          makeInput(
            t("common.value"),
            row.value,
            "value",
            (next) => {
              row.value = next;
            },
            commit,
          ),
        );
      }

      if (options.withContentType) {
        const typedRow = row as FormRow;
        line.append(
          makeInput(
            t("common.default"),
            typedRow.contentType,
            "contentType",
            (next) => {
              typedRow.contentType = next;
            },
            commit,
          ),
        );
      }

      if (withDescription) {
        line.append(
          makeInput(
            t("common.description"),
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
        remove.title = t("common.delete");
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
    add.textContent = options.addLabel();
    add.addEventListener("click", () => {
      options.append();
      focusLastKey();
    });
    root.append(add);
  };

  const unsubscribe = (options.subscribe ?? subscribe)(() => {
    if (options.rows() !== rendered) render();
  });
  // 语言切换只换文案：整表重画，数据仍在 options.rows() 里。
  const unsubscribeLang = onLangChange(() => render());

  render();

  return () => {
    unsubscribe();
    unsubscribeLang();
    host.textContent = "";
  };
}
