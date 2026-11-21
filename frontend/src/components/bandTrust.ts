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

// 指纹很长，界面上只取前 16 位（与文档一致）；完整值仍由 resolve 回给后端，
// 也能经「复制」整串贴给签发者核对。
const FINGERPRINT_DISPLAY_LENGTH = 16;

// 复制指纹：navigator.clipboard 在 webview 里偶尔不可用（没有聚焦、权限被拒），
// 退回选中文本 + execCommand（与设置页复制公钥同一套兜底）。
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return copyWithSelection(text);
  }
}

function copyWithSelection(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "-1000px";
  area.style.left = "-1000px";
  document.body.append(area);
  area.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}

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
  fingerprintValue.classList.add("band-trust-fingerprint");
  const fingerprintText = document.createElement("span");
  fingerprintText.classList.add("mono");
  const copyButton = document.createElement("button");
  copyButton.type = "button";
  copyButton.className = "identity-btn";
  fingerprintValue.append(fingerprintText, copyButton);

  // 防中间人的核心提示：id / 邮箱是自称的，唯一可信的是指纹，而指纹要经别的
  // 渠道核对过才谈得上「信任公钥」。
  const verifyHint = document.createElement("p");
  verifyHint.className = "close-confirm-hint";

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

  card.append(title, note, meta, verifyHint, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;
  let prompt: TrustPrompt | null = null;
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  const fileName = (path: string): string => {
    const base = path.split(/[\\/]/).pop() ?? "";
    return base !== "" ? base : t("sidebar.untitledWorkspace");
  };

  const applyLabels = (): void => {
    title.textContent = t("dialog.bandTrustTitle");
    note.textContent = t("dialog.bandTrustNote", { file: fileName(prompt?.path ?? "") });
    verifyHint.textContent = t("dialog.bandTrustVerifyHint");
    onceButton.textContent = t("dialog.bandTrustOnce");
    alwaysButton.textContent = t("dialog.bandTrustAlways");
    cancelButton.textContent = t("dialog.cancel");
    // 复制反馈中途切语言会覆盖临时文案，回到常态文案即可。
    if (copyTimer === null) {
      copyButton.textContent = t("common.copy");
      copyButton.title = t("common.copy");
    }
  };

  const fillPrompt = (next: TrustPrompt): void => {
    idValue.textContent = next.id;
    emailValue.textContent = next.email;
    // 指纹前 16 位；等宽字体便于逐字核对。完整指纹在悬停提示与「复制」里。
    fingerprintText.textContent = next.fingerprint.slice(0, FINGERPRINT_DISPLAY_LENGTH);
    fingerprintText.title = next.fingerprint;
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

  // 复制完整指纹：贴给签发者走别的渠道核对。
  copyButton.addEventListener("click", () => {
    const fingerprint = prompt?.fingerprint ?? "";
    if (fingerprint === "") return;
    void (async () => {
      const ok = await copyText(fingerprint);
      if (copyTimer !== null) clearTimeout(copyTimer);
      copyButton.textContent = ok ? t("common.copied") : t("common.copyFailed");
      copyTimer = setTimeout(() => {
        copyButton.textContent = t("common.copy");
        copyTimer = null;
      }, 1600);
    })();
  });

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
