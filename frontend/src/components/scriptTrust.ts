/**
 * 脚本信任确认框：打开的 .band 带预执行/后执行脚本时，第一次要执行带脚本的
 * 请求之前明确问一次「信任并运行」。
 *
 * 两键、单例：主按钮是「信任并运行」，另一个是「不运行」（默认焦点，避免手快
 * 就放行）。文案走词典键，开着时切语言跟着换；文件名按当前工作区现取。
 *
 * 样式复用 close-confirm 的三键框（同一套模态外观），按钮只有两个。
 */

import { t, onLangChange } from "../i18n";
import { getWorkspace } from "../state";

export interface ScriptTrustDialogHandlers {
  /** 用户明确同意运行本次载入的工作区里的脚本。 */
  trust: () => void;
}

export interface ScriptTrustDialog {
  /** 打开确认框；已经打开时忽略新的触发。 */
  open: () => void;
}

export function mountScriptTrustDialog(handlers: ScriptTrustDialogHandlers): ScriptTrustDialog {
  const overlay = document.createElement("div");
  overlay.className = "close-confirm";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "script-trust-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "script-trust-title";

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
  const trustButton = makeButton(true);

  card.append(title, note, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;

  // 副文案里的「哪份文件」：打开的 .band 总有真实路径，取文件名；没有路径
  // （理论上不会发生在打开流程里）就退回工作区名。
  const workspaceLabel = (): string => {
    const workspace = getWorkspace();
    const base = (workspace.path ?? "").split(/[\\/]/).pop() ?? "";
    if (base !== "") return base;
    const name = workspace.name.trim();
    return name !== "" ? name : t("sidebar.untitledWorkspace");
  };

  const applyLabels = (): void => {
    title.textContent = t("dialog.scriptTrustTitle");
    note.textContent = t("dialog.scriptTrustNote", { file: workspaceLabel() });
    trustButton.textContent = t("dialog.scriptTrustRun");
    cancelButton.textContent = t("dialog.scriptTrustCancel");
  };

  const hide = (): void => {
    shown = false;
    overlay.hidden = true;
  };

  overlay.addEventListener("mousedown", (event) => {
    if (event.target === overlay) hide();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !shown) return;
    event.preventDefault();
    hide();
  });

  cancelButton.addEventListener("click", () => hide());
  trustButton.addEventListener("click", () => {
    hide();
    handlers.trust();
  });

  // 对话框开着时切语言：标题、副文案与按钮文案跟着换。
  onLangChange(() => {
    if (shown) applyLabels();
  });

  return {
    open: () => {
      if (shown) return;
      applyLabels();
      shown = true;
      overlay.hidden = false;
      // 默认焦点落在「不运行」：放行脚本必须是用户主动点的那一下。
      cancelButton.focus();
    },
  };
}
