/**
 * 工作区信任弹窗：打开的 v2 .band 带签名，而签发者既不是本机身份也不在信任
 * 列表里。此时后端只解到 header（还没解密、没解 zip），把签发者的 ID / 邮箱 /
 * 指纹交给页面，由用户决定这次怎么办。
 *
 * 三键：**信任一次**（只对这次打开生效，主按钮）/ **信任公钥**（写进
 * trusted.json，以后同一把钥匙直接开）/ **取消**（丢弃挂起的读盘）。Esc 与点
 * 遮罩都按「取消」（与 scriptTrust 一致：不给用户无声放行的路径）。
 *
 * ID / 邮箱来自一个还没被信任的文件，只能用 textContent 写进 DOM，绝不拼 HTML。
 *
 * 单例：挂起期间又触发一次打开时，新触发被忽略（main.ts 另外用 isOpen() 拦在
 * 「调后端」之前，避免后端挂起槽被第二次读盘覆盖）。
 */

import { onLangChange, t } from "../i18n";
import type { TrustPrompt } from "../mygo";

export interface BandTrustDialogHandlers {
  /** 用户在弹窗里做了决定：remember=false 是「信任一次」。 */
  resolve: (fingerprint: string, remember: boolean) => void;
  /** 取消：丢弃挂起的读盘，什么都不加载。 */
  dismiss: () => void;
}

export interface BandTrustDialog {
  /** 打开弹窗并按 prompt 填内容；已经打开时忽略新的触发。 */
  open: (prompt: TrustPrompt) => void;
  /** 弹窗是否正在显示（main.ts 据此忽略挂起期间的新打开触发）。 */
  isOpen: () => boolean;
}

// 指纹很长，界面上只取前 16 位（与文档一致）；完整值仍由 resolve 回给后端。
const FINGERPRINT_DISPLAY_LENGTH = 16;

export function mountBandTrustDialog(handlers: BandTrustDialogHandlers): BandTrustDialog {
  const overlay = document.createElement("div");
  overlay.className = "close-confirm";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card wide";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "band-trust-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "band-trust-title";

  const note = document.createElement("p");
  note.className = "close-confirm-note";

  // 签发者信息表：ID / 邮箱 / 指纹。值一律 textContent 写入。
  const meta = document.createElement("dl");
  meta.className = "band-trust-meta";

  const addRow = (labelKey: string): HTMLElement => {
    const term = document.createElement("dt");
    term.textContent = t(labelKey);
    term.dataset.i18n = labelKey;
    const value = document.createElement("dd");
    meta.append(term, value);
    return value;
  };

  const idValue = addRow("dialog.bandTrustId");
  const emailValue = addRow("dialog.bandTrustEmail");
  const fingerprintValue = addRow("dialog.bandTrustFingerprint");
  fingerprintValue.classList.add("mono");

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
  const alwaysButton = makeButton();
  const onceButton = makeButton(true);

  card.append(title, note, meta, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;
  let prompt: TrustPrompt | null = null;

  const fileName = (path: string): string => {
    const base = path.split(/[\\/]/).pop() ?? "";
    return base !== "" ? base : t("sidebar.untitledWorkspace");
  };

  const applyLabels = (): void => {
    title.textContent = t("dialog.bandTrustTitle");
    note.textContent = t("dialog.bandTrustNote", { file: fileName(prompt?.path ?? "") });
    onceButton.textContent = t("dialog.bandTrustOnce");
    alwaysButton.textContent = t("dialog.bandTrustAlways");
    cancelButton.textContent = t("dialog.cancel");
  };

  const fillPrompt = (next: TrustPrompt): void => {
    idValue.textContent = next.id;
    emailValue.textContent = next.email;
    // 指纹前 16 位；等宽字体便于逐字核对（样式在 dd 上的 .mono）。
    fingerprintValue.textContent = next.fingerprint.slice(0, FINGERPRINT_DISPLAY_LENGTH);
    // 完整指纹放 title 里：需要贴给别人核对时不用去翻文件。
    fingerprintValue.title = next.fingerprint;
  };

  const hide = (): void => {
    shown = false;
    overlay.hidden = true;
  };

  const dismiss = (): void => {
    if (!shown) return;
    const hadPrompt = prompt !== null;
    prompt = null;
    hide();
    if (hadPrompt) handlers.dismiss();
  };

  const resolve = (remember: boolean): void => {
    if (!shown) return;
    const current = prompt;
    prompt = null;
    hide();
    if (current === null) return;
    handlers.resolve(current.fingerprint, remember);
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
  alwaysButton.addEventListener("click", () => resolve(true));
  onceButton.addEventListener("click", () => resolve(false));

  // 弹窗开着时切语言：标题、说明与按钮跟着换；签发者信息是数据，不动。
  onLangChange(() => {
    if (shown) applyLabels();
  });

  return {
    open: (next) => {
      if (shown) return;
      prompt = next;
      applyLabels();
      fillPrompt(next);
      shown = true;
      overlay.hidden = false;
      // 默认焦点落在「取消」：放行别人的签名必须是用户主动点的那一下。
      cancelButton.focus();
    },
    isOpen: () => shown,
  };
}
