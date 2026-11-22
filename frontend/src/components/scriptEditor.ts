import { t } from "../i18n";
import { loadMonaco, monacoTheme, type MonacoApi } from "../monacoSetup";
import { appendExample } from "../scriptExamples";

/**
 * 挂载一个 Monaco 脚本编辑器，与外部状态双向绑定：
 * - get 读外部状态（唯一真相）
 * - set 把编辑器内容写回外部状态
 * - subscribe 外部状态变化时通知，变化来自外部时同步进编辑器（避免回环）
 *
 * Monaco 很重（编辑器本体 + ts 语言服务），所以实例惰性创建：ensure 在「预执行/
 * 后执行」页签第一次显示时调用，之后实例常驻（视图只是 hidden，不销毁重建）。
 * 加载或初始化失败会放开 creating，下次切回来重试，不会永久卡在创建中。
 *
 * 返回值里的 insertText 供「插入范例」用：空编辑器整体填入，非空追加，
 * 实例未创建时直接写外部状态（不为此提前加载 Monaco）。
 */
export function mountScriptEditor(
  host: HTMLElement,
  get: () => string,
  set: (value: string) => void,
  subscribe: (listener: () => void) => () => void,
): { ensure: () => void; insertText: (text: string, name: string) => void; dispose: () => void } {
  host.textContent = "";

  const box = document.createElement("div");
  box.className = "script-editor";
  host.append(box);

  let instance: ReturnType<MonacoApi["editor"]["create"]> | null = null;
  let creating = false;
  let disposed = false;
  let applying = false;
  let errorBox: HTMLElement | null = null;

  const clearError = (): void => {
    errorBox?.remove();
    errorBox = null;
    box.hidden = false;
  };

  // 加载或创建失败时把原因摆在宿主里，而不是吞掉：一条简洁消息 + 「重试」按钮。
  // 控制台仍有完整错误（异常对象原样打出去），定位问题不用猜。
  const showError = (error: unknown): void => {
    const detail = error instanceof Error ? error.message : String(error);
    console.error("[scriptEditor] Monaco 加载失败", error);
    clearError();
    box.hidden = true;
    const panel = document.createElement("div");
    panel.className = "editor-error";
    const text = document.createElement("p");
    text.className = "editor-error-text";
    text.textContent = t("editor.loadFailed", { error: detail });
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "editor-error-retry";
    retry.textContent = t("editor.retry");
    retry.addEventListener("click", () => {
      clearError();
      create();
    });
    panel.append(text, retry);
    host.append(panel);
    errorBox = panel;
  };

  const sync = (): void => {
    if (instance === null) return;
    const next = get();
    if (applying || instance.getValue() === next) return;
    applying = true;
    const position = instance.getPosition();
    instance.setValue(next);
    if (position !== null) instance.setPosition(position);
    applying = false;
  };

  const build = (monaco: MonacoApi): void => {
    const created = monaco.editor.create(box, {
      value: get(),
      language: "javascript",
      theme: monacoTheme(),
      minimap: { enabled: false },
      fontSize: 13,
      wordWrap: "on",
      automaticLayout: true,
      scrollBeyondLastLine: false,
      fixedOverflowWidgets: true,
      fontFamily: 'ui-monospace, "Cascadia Code", Consolas, monospace',
      padding: { top: 8, bottom: 8 },
      scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
      suggest: { showWords: false },
      tabCompletion: "on",
    });

    created.getModel()?.updateOptions({ tabSize: 2, insertSpaces: true });
    created.onDidChangeModelContent(() => {
      if (applying) return;
      const next = created.getValue();
      if (next === get()) return;
      set(next);
    });
    instance = created;
    // 创建期间内容可能已经被换掉（切条目/载入工作区），对齐一次。
    sync();
  };

  const create = (): void => {
    if (instance !== null || creating || disposed) return;
    creating = true;
    void loadMonaco()
      .then((monaco) => {
        if (disposed || instance !== null) return;
        build(monaco);
        clearError();
      })
      .catch((error: unknown) => {
        // 失败不缓存（loadMonaco 会清掉自己），这里把原因摆出来，点重试或再切回页签都行。
        if (disposed) return;
        showError(error);
      })
      .finally(() => {
        creating = false;
      });
  };

  const unsubscribe = subscribe(sync);

  /**
   * 插入一段代码（「插入范例」用）：空编辑器整体填入，非空在末尾追加，
   * 中间隔一行注释分隔线（带范例名，注释语言跟随界面）。
   *
   * 绝不覆盖用户已有内容：这是编辑器里唯一一处「不经过用户输入就改内容」的
   * 入口，静默覆盖会把写了一半的脚本吃掉。实例还没创建时（用户没切到过该
   * 页签）直接写外部状态，ensure 时自然带出——Monaco 很重，不为此提前加载。
   */
  const insertText = (text: string, name: string): void => {
    const current = get();
    const next = appendExample(current, text, name);
    if (next === current) return;
    if (instance === null) {
      set(next);
      return;
    }
    // setValue 会触发 onDidChangeModelContent，由那里写回外部状态。
    instance.setValue(next);
    const model = instance.getModel();
    if (model !== null) {
      const end = model.getFullModelRange().getEndPosition();
      instance.setPosition(end);
      instance.revealPosition(end);
    }
    instance.focus();
  };

  return {
    ensure: create,
    insertText,
    dispose() {
      disposed = true;
      unsubscribe();
      instance?.dispose();
      instance = null;
      clearError();
      host.textContent = "";
    },
  };
}
