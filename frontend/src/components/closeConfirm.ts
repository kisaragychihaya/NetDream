/**
 * 三键确认框：先保存 / 放弃改动继续 / 取消当前操作。
 *
 * 单例：应用里只有一个未保存守卫场景在前面，同一个对话框按每次打开的配置
 * 换标题、副文案与按钮文案，避免多个模态框叠加。
 *
 * 文案传词典键而不是成品文案：对话框开着时切语言，标题与按钮也跟着换。
 */

import { onLangChange, t } from "../i18n";

export interface ConfirmDialogOptions {
  /** 标题的词典键。 */
  titleKey: string;
  /** 副文案的词典键；缺省用通用的「保存 / 不保存 / 取消」说明。 */
  noteKey?: string;
  /** 主按钮（primary）文案的词典键：先跑保存流程，成功后再继续。 */
  saveKey: string;
  /** 中间按钮文案的词典键：放弃未保存的改动，直接继续。 */
  discardKey: string;
  /** 末尾按钮文案的词典键：只关掉对话框，保持现状。 */
  cancelKey: string;
  /** 继续执行的动作：保存成功后与选择放弃后都走它。 */
  proceed: () => void;
}

export interface ConfirmDialogHandlers {
  /** 执行保存流程，返回是否真的写进了文件（「另存为」被取消时为 false）。 */
  save: () => Promise<boolean>;
}

export interface ConfirmDialog {
  /** 按给定配置打开；对话框已经打开时忽略新的触发。 */
  open: (options: ConfirmDialogOptions) => void;
}

const DEFAULT_NOTE_KEY = "dialog.unsavedNote";

export function mountConfirmDialog(handlers: ConfirmDialogHandlers): ConfirmDialog {
  const overlay = document.createElement("div");
  overlay.className = "close-confirm";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "close-confirm-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "close-confirm-title";

  const note = document.createElement("p");
  note.className = "close-confirm-note";

  const actions = document.createElement("div");
  actions.className = "close-confirm-actions";

  const makeButton = (primary = false): HTMLButtonElement => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = primary ? "close-confirm-btn primary" : "close-confirm-btn";
    actions.append(button);
    return button;
  };

  const cancelButton = makeButton();
  const discardButton = makeButton();
  const saveButton = makeButton(true);

  card.append(title, note, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;
  let busy = false;
  // 当前这次打开的配置：按钮文案与「继续」动作都来自它。
  let current: ConfirmDialogOptions | null = null;

  // 按当前语言写标题、副文案与三个按钮。
  const applyLabels = (): void => {
    if (current === null) return;
    title.textContent = t(current.titleKey);
    note.textContent = t(current.noteKey ?? DEFAULT_NOTE_KEY);
    saveButton.textContent = busy ? t("dialog.saving") : t(current.saveKey);
    discardButton.textContent = t(current.discardKey);
    cancelButton.textContent = t(current.cancelKey);
  };

  const setBusy = (next: boolean): void => {
    busy = next;
    for (const button of [saveButton, discardButton, cancelButton]) button.disabled = next;
    saveButton.textContent = next ? t("dialog.saving") : t(current?.saveKey ?? "common.save");
  };

  const hide = (): void => {
    shown = false;
    overlay.hidden = true;
  };

  const dismiss = (): void => {
    if (busy) return;
    hide();
  };

  const proceed = (): void => {
    const action = current?.proceed;
    hide();
    action?.();
  };

  overlay.addEventListener("mousedown", (event) => {
    if (event.target === overlay) dismiss();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !shown) return;
    event.preventDefault();
    dismiss();
  });

  cancelButton.addEventListener("click", () => dismiss());

  discardButton.addEventListener("click", () => {
    if (busy) return;
    proceed();
  });

  saveButton.addEventListener("click", () => {
    if (busy) return;
    setBusy(true);
    void (async () => {
      try {
        const saved = await handlers.save();
        // 保存被取消（另存为对话框）或失败时留在应用里，对话框也不关。
        if (!saved) return;
        proceed();
      } catch {
        // 保存流程自己会报错；对话框留在原地等用户重试或改选。
      } finally {
        setBusy(false);
      }
    })();
  });

  // 对话框开着时切语言：标题与按钮文案跟着换。
  onLangChange(() => {
    if (shown) applyLabels();
  });

  return {
    open: (options) => {
      if (shown) return;
      current = options;
      applyLabels();
      shown = true;
      overlay.hidden = false;
      saveButton.focus();
    },
  };
}
