/**
 * 三键确认框：先保存 / 放弃改动继续 / 取消当前操作。
 *
 * 单例：应用里只有一个未保存守卫场景在前面，同一个对话框按每次打开的配置
 * 换标题、副文案与按钮文案，避免多个模态框叠加。
 */

export interface ConfirmDialogOptions {
  title: string;
  note: string;
  /** 主按钮（primary）文案：先跑保存流程，成功后再继续。 */
  save: string;
  /** 中间按钮文案：放弃未保存的改动，直接继续。 */
  discard: string;
  /** 末尾按钮文案：只关掉对话框，保持现状。 */
  cancel: string;
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

const SAVING_LABEL = "保存中…";

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

  const setBusy = (next: boolean): void => {
    busy = next;
    for (const button of [saveButton, discardButton, cancelButton]) button.disabled = next;
    saveButton.textContent = next ? SAVING_LABEL : (current?.save ?? "保存");
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

  return {
    open: (options) => {
      if (shown) return;
      current = options;
      title.textContent = options.title;
      note.textContent = options.note;
      saveButton.textContent = options.save;
      discardButton.textContent = options.discard;
      cancelButton.textContent = options.cancel;
      shown = true;
      overlay.hidden = false;
      saveButton.focus();
    },
  };
}
