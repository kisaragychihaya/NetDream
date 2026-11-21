/**
 * 首次运行的身份对话框：本机还没建立身份时（`IdentityService.get()` 的
 * status 为 "unset"）在启动后弹出，说明用途并要求二选一——填邮箱建立，或留空
 * 让后端生成随机身份。
 *
 * 单例且**不可绕过**：Esc 与点遮罩都不关闭（信任弹窗那种「按一下就跳过」的
 * 默认行为在这里等于把用户推给一个随机身份，没有提示也没有记录）。要么填邮箱
 * 点「确定」，要么点「跳过」，两条路都会真的调用 Setup 建立身份。
 *
 * 失败留在框里显示原因（错误条在遮罩后面看不见），关掉框之前不继续。文案走
 * 词典键，开着时切语言跟着换。
 */

import { onLangChange, t } from "../i18n";
import { IdentityService, type IdentityInfo } from "../mygo";

export interface FirstRunDialogHandlers {
  /** 身份已就绪（刚建立或本来就有），参数是后端返回的身份。 */
  ready: (identity: IdentityInfo) => void;
}

export interface FirstRunDialog {
  /** 打开对话框；已经打开时忽略新的触发。 */
  open: () => void;
}

export function mountFirstRunDialog(handlers: FirstRunDialogHandlers): FirstRunDialog {
  const overlay = document.createElement("div");
  overlay.className = "close-confirm";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "first-run-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "first-run-title";

  const note = document.createElement("p");
  note.className = "close-confirm-note";

  const field = document.createElement("label");
  field.className = "close-confirm-field";
  const fieldLabel = document.createElement("span");
  const emailInput = document.createElement("input");
  emailInput.type = "email";
  emailInput.autocomplete = "off";
  emailInput.spellcheck = false;
  field.append(fieldLabel, emailInput);

  const error = document.createElement("p");
  error.className = "close-confirm-error";
  error.hidden = true;

  const actions = document.createElement("div");
  actions.className = "close-confirm-actions";

  const makeButton = (primary = false): HTMLButtonElement => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = primary ? "close-confirm-btn primary" : "close-confirm-btn";
    actions.append(button);
    return button;
  };

  const skipButton = makeButton();
  const confirmButton = makeButton(true);

  card.append(title, note, field, error, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;
  let busy = false;

  const applyLabels = (): void => {
    title.textContent = t("dialog.firstRunTitle");
    note.textContent = t("dialog.firstRunNote");
    fieldLabel.textContent = t("dialog.firstRunEmailLabel");
    emailInput.placeholder = t("dialog.firstRunEmailPlaceholder");
    skipButton.textContent = t("dialog.firstRunSkip");
    confirmButton.textContent = t("dialog.firstRunConfirm");
  };

  const setBusy = (next: boolean): void => {
    busy = next;
    emailInput.disabled = next;
    skipButton.disabled = next;
    confirmButton.disabled = next;
    confirmButton.textContent = next ? t("dialog.firstRunWorking") : t("dialog.firstRunConfirm");
  };

  const showError = (message: string): void => {
    error.textContent = message;
    error.hidden = false;
  };

  const clearError = (): void => {
    error.hidden = true;
    error.textContent = "";
  };

  // 点遮罩不关闭：首次运行必须明确二选一（见文件头）。Esc 也不注册处理器。
  const hide = (): void => {
    shown = false;
    overlay.hidden = true;
  };

  const setup = (email: string): void => {
    if (busy) return;
    clearError();
    setBusy(true);
    void (async () => {
      try {
        const identity = await IdentityService.setup(email);
        setBusy(false);
        hide();
        handlers.ready(identity);
      } catch (cause) {
        setBusy(false);
        showError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
  };

  skipButton.addEventListener("click", () => setup(""));
  confirmButton.addEventListener("click", () => setup(emailInput.value.trim()));
  emailInput.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    setup(emailInput.value.trim());
  });

  // 对话框开着时切语言：标题、说明、占位与按钮文案跟着换（已填的邮箱保留）。
  onLangChange(() => {
    if (!shown) return;
    applyLabels();
    if (busy) confirmButton.textContent = t("dialog.firstRunWorking");
  });

  return {
    open: () => {
      if (shown) return;
      clearError();
      setBusy(false);
      applyLabels();
      shown = true;
      overlay.hidden = false;
      emailInput.focus();
    },
  };
}
