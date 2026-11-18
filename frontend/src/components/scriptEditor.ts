import { monaco } from "../monacoSetup";

/**
 * 挂载一个 Monaco 脚本编辑器，与外部状态双向绑定：
 * - get 读外部状态（唯一真相）
 * - set 把编辑器内容写回外部状态
 * - subscribe 外部状态变化时通知，变化来自外部时同步进编辑器（避免回环）
 */
export function mountScriptEditor(
  host: HTMLElement,
  get: () => string,
  set: (value: string) => void,
  subscribe: (listener: () => void) => () => void,
): () => void {
  host.textContent = "";

  const box = document.createElement("div");
  box.className = "script-editor";
  host.append(box);

  const instance = monaco.editor.create(box, {
    value: get(),
    language: "javascript",
    theme: "vs",
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

  instance.getModel()?.updateOptions({ tabSize: 2, insertSpaces: true });

  let applying = false;

  const contentListener = instance.onDidChangeModelContent(() => {
    if (applying) return;
    const next = instance.getValue();
    if (next === get()) return;
    set(next);
  });

  const sync = (): void => {
    const next = get();
    if (applying || instance.getValue() === next) return;
    applying = true;
    const position = instance.getPosition();
    instance.setValue(next);
    if (position !== null) instance.setPosition(position);
    applying = false;
  };

  sync();
  const unsubscribe = subscribe(sync);

  return () => {
    unsubscribe();
    contentListener.dispose();
    instance.dispose();
    host.textContent = "";
  };
}
