/**
 * 保存选项弹窗：原生保存对话框挑好路径之后问「加密保存吗、写给谁」。加密与否
 * 是 Save 的参数（`SaveOptions`），所以要在这里落实；不加密就是一个勾选框都
 * 不勾直接确认。
 *
 * 收件人来自 `IdentityService.candidates()`（自己 + 持久信任 + 身份缓存），
 * 也可以从别的 .band 的 header 里导入一把公钥（`importFromBand()`，返回空指纹
 * = 用户取消了文件对话框）。
 *
 * 「一个都不勾」的含义取决于目标文件：那正是**当前这个已加密的文件**时等于
 * 沿用原信封（后端按路径记着它），否则后端会以「加密保存需要至少选择一个收件人」
 * 报错——所以这种情况在前端就地拦下，不白跑一次写盘。
 *
 * 单例：已经在显示时新的 ask 立刻以 null（取消）返回。Esc 与点遮罩都按取消。
 */

import { onLangChange, t } from "../i18n";
import { IdentityService, type IdentityCandidate, type SaveOptions } from "../mygo";

export interface SaveDialogRequest {
  /** 当前 .band 是否已加密：勾选框默认选中，并显示「沿用原收件人」的说明。 */
  encrypted: boolean;
  /** 目标路径是否就是当前这份文件：只有它已经持有信封时才谈得上「沿用」。 */
  canReuse: boolean;
}

export interface SaveDialog {
  /** 问一次保存选项；用户取消时返回 null。 */
  ask: (request: SaveDialogRequest) => Promise<SaveOptions | null>;
}

// 收件人列表里指纹只取前 8 位够辨认（完整值在设置页可查）。
const FINGERPRINT_DISPLAY_LENGTH = 8;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function mountSaveDialog(): SaveDialog {
  const overlay = document.createElement("div");
  // stacked：保存选项会叠在未保存守卫（同一个 200 层级）上面。
  overlay.className = "close-confirm stacked";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card wide";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "save-options-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "save-options-title";

  const encryptRow = document.createElement("label");
  encryptRow.className = "save-options-check";
  const encryptInput = document.createElement("input");
  encryptInput.type = "checkbox";
  const encryptLabel = document.createElement("span");
  encryptRow.append(encryptInput, encryptLabel);

  // 「未勾选任何收件人 = 沿用原收件人」：只有原文件已加密时才有这层含义。
  const reuseNote = document.createElement("p");
  reuseNote.className = "close-confirm-hint";

  const recipients = document.createElement("div");
  recipients.className = "save-recipients";
  recipients.hidden = true;

  const recipientsTitle = document.createElement("div");
  recipientsTitle.className = "save-recipients-title";

  const list = document.createElement("div");
  list.className = "save-recipient-list";

  const importButton = document.createElement("button");
  importButton.type = "button";
  importButton.className = "save-import";

  recipients.append(recipientsTitle, list, importButton);

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

  const cancelButton = makeButton();
  const confirmButton = makeButton(true);

  card.append(title, encryptRow, reuseNote, recipients, error, actions);
  overlay.append(card);
  document.body.append(overlay);

  let shown = false;
  let request: SaveDialogRequest | null = null;
  // 本次打开的答复出口：取消、确认各调用一次。
  let settle: ((options: SaveOptions | null) => void) | null = null;
  let candidates: IdentityCandidate[] = [];
  // 勾选状态按指纹记（刷新列表、切语言重建行都不丢选择）。
  const checked = new Set<string>();
  let loadingCandidates = false;

  const applyLabels = (): void => {
    title.textContent = t("dialog.saveOptionsTitle");
    encryptLabel.textContent = t("dialog.saveEncrypt");
    reuseNote.textContent = t("dialog.saveReuseNote");
    recipientsTitle.textContent = t("dialog.saveRecipients");
    importButton.textContent = t("dialog.saveImport");
    importButton.title = t("dialog.saveImportTitle");
    importButton.disabled = loadingCandidates;
    confirmButton.textContent = t("dialog.saveConfirm");
    cancelButton.textContent = t("dialog.cancel");
  };

  const showError = (message: string): void => {
    error.textContent = message;
    error.hidden = false;
  };

  const clearError = (): void => {
    error.hidden = true;
    error.textContent = "";
  };

  // 收件人行：勾选框 + 邮箱（没有就显示 ID）+ ID（与邮箱不同才有）+ 指纹前 8 位。
  const renderRecipients = (): void => {
    const rows: HTMLElement[] = [];
    if (candidates.length === 0) {
      const empty = document.createElement("p");
      empty.className = "save-recipient-empty";
      empty.textContent = t("dialog.saveCandidatesEmpty");
      rows.push(empty);
    }
    for (const candidate of candidates) {
      const row = document.createElement("label");
      row.className = "save-recipient";

      const input = document.createElement("input");
      input.type = "checkbox";
      input.checked = checked.has(candidate.fingerprint);
      input.addEventListener("change", () => {
        if (input.checked) checked.add(candidate.fingerprint);
        else checked.delete(candidate.fingerprint);
      });

      const primary = document.createElement("span");
      primary.className = "save-recipient-id";
      primary.textContent = candidate.email !== "" ? candidate.email : candidate.id;

      const secondary = document.createElement("span");
      secondary.className = "save-recipient-meta";
      const parts: string[] = [];
      if (candidate.id !== "" && candidate.id !== candidate.email) parts.push(candidate.id);
      const fingerprint = document.createElement("span");
      fingerprint.className = "mono";
      fingerprint.textContent = candidate.fingerprint.slice(0, FINGERPRINT_DISPLAY_LENGTH);
      secondary.append(parts.length > 0 ? `${parts.join(" · ")} · ` : "", fingerprint);

      row.append(input, primary, secondary);
      rows.push(row);
    }
    list.replaceChildren(...rows);
  };

  const loadCandidates = (): void => {
    loadingCandidates = true;
    applyLabels();
    void (async () => {
      try {
        candidates = await IdentityService.candidates();
      } catch (cause) {
        candidates = [];
        showError(t("dialog.saveCandidatesFailed", { error: messageOf(cause) }));
      } finally {
        loadingCandidates = false;
        // 弹窗已经被关掉（用户在这期间取消了）就不必再画。
        if (shown) {
          applyLabels();
          renderRecipients();
        }
      }
    })();
  };

  const syncEncrypt = (): void => {
    const encrypt = encryptInput.checked;
    recipients.hidden = !encrypt;
    // 「未勾选任何收件人 = 沿用原收件人」只对写回原文件成立：另存到新路径时
    // 一个都不勾就是没人能打开，这个说明不该出现。
    reuseNote.hidden = !encrypt || request?.encrypted !== true || request?.canReuse !== true;
    // 收件人列表按需拉：不加密时不必问后端要一份用不上的列表。
    if (encrypt && candidates.length === 0 && !loadingCandidates) loadCandidates();
  };

  const finish = (options: SaveOptions | null): void => {
    const done = settle;
    settle = null;
    shown = false;
    request = null;
    overlay.hidden = true;
    done?.(options);
  };

  const cancel = (): void => {
    if (!shown) return;
    finish(null);
  };

  const confirm = (): void => {
    if (!shown || request === null) return;
    clearError();
    if (!encryptInput.checked) {
      finish({ encrypt: false, recipients: [] });
      return;
    }
    const recipientsPicked = candidates.filter((candidate) => checked.has(candidate.fingerprint));
    if (recipientsPicked.length === 0 && !request.canReuse) {
      // 没有原信封可沿用：一个收件人都没有就没人能打开这份文件。
      showError(t("dialog.saveNoRecipients"));
      return;
    }
    finish({ encrypt: true, recipients: recipientsPicked.map((candidate) => candidate.ecdh) });
  };

  overlay.addEventListener("mousedown", (event) => {
    if (event.target === overlay) cancel();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !shown) return;
    event.preventDefault();
    cancel();
  });

  encryptInput.addEventListener("change", syncEncrypt);
  cancelButton.addEventListener("click", () => cancel());
  confirmButton.addEventListener("click", () => confirm());

  // 导入公钥：文件对话框由后端弹；返回空指纹 = 用户取消，什么都不改。
  importButton.addEventListener("click", () => {
    if (!shown || loadingCandidates) return;
    clearError();
    loadingCandidates = true;
    applyLabels();
    void (async () => {
      try {
        const imported = await IdentityService.importFromBand();
        if (imported.fingerprint === "") return;
        candidates = await IdentityService.candidates();
        checked.add(imported.fingerprint);
        renderRecipients();
      } catch (cause) {
        showError(t("dialog.saveImportFailed", { error: messageOf(cause) }));
      } finally {
        loadingCandidates = false;
        if (shown) applyLabels();
      }
    })();
  });

  // 弹窗开着时切语言：标签、按钮与收件人行跟着换（勾选状态保留）。
  onLangChange(() => {
    if (!shown) return;
    applyLabels();
    renderRecipients();
  });

  return {
    ask: (next) => {
      // 单例：已经问着一次了，新的触发当作取消（不叠加两个框，也不动前一次的选择）。
      if (shown) return Promise.resolve(null);
      request = next;
      checked.clear();
      candidates = [];
      loadingCandidates = false;
      clearError();
      encryptInput.checked = next.encrypted;
      applyLabels();
      renderRecipients();
      shown = true;
      overlay.hidden = false;
      syncEncrypt();
      confirmButton.focus();
      return new Promise<SaveOptions | null>((resolve) => {
        settle = resolve;
      });
    },
  };
}
