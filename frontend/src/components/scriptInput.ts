/**
 * nd.input 的输入框：脚本在 Go 侧阻塞等回答，这里弹模态框收一个值。
 *
 * 单例 + 队列：多个脚本（HTTP 后执行脚本与连接接收脚本等）可能同时在等输入，
 * 一次只弹一个，回答完再弹下一个。确认把值写回脚本要的变量并继续执行；取消
 * 让脚本吃到一个可 try/catch 的错误。Esc / 取消按钮 = 取消；点遮罩不取消——
 * 已经输入的内容不该被一次误点丢掉。文案走词典键，开着时切语言跟着换。
 */

import { onLangChange, t } from "../i18n";
import type { ScriptInputRequest } from "../mygo";

export interface ScriptInputDialogHandlers {
  /** 把回答递回后端：ok=false 表示用户取消；投递失败（脚本已被取消）由调用方吞掉。 */
  resolve: (id: string, value: string, ok: boolean) => void;
}

export interface ScriptInputDialog {
  /** 排入一次输入请求；当前没有展示中的请求时立即弹出。 */
  enqueue: (request: ScriptInputRequest) => void;
  /**
   * 撤回一次输入请求：脚本那次运行已经结束（取消/终止），后端收不到回答也没有
   * 必要再展示。排队中的直接摘掉；正在展示的直接关掉换下一个（不递回答）。
   */
  retract: (id: string) => void;
}

export function mountScriptInputDialog(handlers: ScriptInputDialogHandlers): ScriptInputDialog {
  const overlay = document.createElement("div");
  overlay.className = "close-confirm";
  overlay.hidden = true;

  const card = document.createElement("div");
  card.className = "close-confirm-card";
  card.setAttribute("role", "dialog");
  card.setAttribute("aria-modal", "true");
  card.setAttribute("aria-labelledby", "script-input-title");

  const title = document.createElement("h2");
  title.className = "close-confirm-title";
  title.id = "script-input-title";

  // 脚本给的提示语：用户数据，textContent 写入。
  const prompt = document.createElement("p");
  prompt.className = "script-input-prompt";

  const note = document.createElement("p");
  note.className = "close-confirm-note";

  const input = document.createElement("input");
  input.type = "text";
  input.className = "script-input-field";
  input.spellcheck = false;

  const actions = document.createElement("div");
  actions.className = "close-confirm-actions";

  const cancelButton = document.createElement("button");
  cancelButton.type = "button";
  cancelButton.className = "close-confirm-btn";

  const confirmButton = document.createElement("button");
  confirmButton.type = "button";
  confirmButton.className = "close-confirm-btn primary";

  actions.append(cancelButton, confirmButton);
  card.append(title, prompt, note, input, actions);
  overlay.append(card);
  document.body.append(overlay);

  // 展示中的请求与等待队列：一次只弹一个，回答完再弹下一个。
  let current: ScriptInputRequest | null = null;
  const queue: ScriptInputRequest[] = [];

  const applyLabels = (): void => {
    if (current === null) return;
    title.textContent = t("dialog.scriptInputTitle");
    prompt.textContent = current.prompt;
    prompt.hidden = current.prompt.trim() === "";
    note.textContent = t("dialog.scriptInputNote", { name: current.name });
    input.placeholder = t("dialog.scriptInputPlaceholder");
    cancelButton.textContent = t("dialog.cancel");
    confirmButton.textContent = t("dialog.scriptInputConfirm");
  };

  const showNext = (): void => {
    current = queue.shift() ?? null;
    if (current === null) {
      overlay.hidden = true;
      return;
    }
    input.value = "";
    applyLabels();
    overlay.hidden = false;
    input.focus();
  };

  const answer = (ok: boolean): void => {
    const request = current;
    if (request === null) return;
    handlers.resolve(request.id, ok ? input.value : "", ok);
    showNext();
  };

  confirmButton.addEventListener("click", () => answer(true));
  cancelButton.addEventListener("click", () => answer(false));
  input.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    answer(true);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || current === null) return;
    event.preventDefault();
    answer(false);
  });

  // 对话框开着时切语言：标题、说明与按钮文案跟着换（已输入的内容保留）。
  onLangChange(() => {
    if (current !== null) applyLabels();
  });

  return {
    enqueue: (request) => {
      queue.push(request);
      if (current === null) showNext();
    },
    retract: (id) => {
      const at = queue.findIndex((request) => request.id === id);
      if (at >= 0) {
        queue.splice(at, 1);
        return;
      }
      // 展示中的被撤回：回答已无处可去，直接关掉换下一个。
      if (current?.id === id) showNext();
    },
  };
}
