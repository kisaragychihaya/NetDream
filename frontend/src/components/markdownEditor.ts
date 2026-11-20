import Vditor from "vditor";
import "vditor/dist/index.css";
import { getLang, onLangChange, t, type Lang } from "../i18n";
import { getEffectiveTheme, onThemeChange } from "../theme";

/**
 * Markdown 文档编辑器（Vditor，即时渲染模式），与外部状态双向绑定：
 * - get 读外部状态（唯一真相）
 * - set 把文档内容写回外部状态
 * - subscribe 外部状态变化时通知，变化来自外部时同步进编辑器（避免回环）
 *
 * Vditor 初始化要加载本地 lute（约 3.6M），所以惰性创建：ensure 在界面第一次
 * 切到 Markdown 条目时调用，之后实例常驻（视图只是 hidden，不销毁重建）。
 * 语言切换时重建实例（Vditor 的界面语言只在初始化时确定）。
 */
export function mountMarkdownEditor(
  host: HTMLElement,
  get: () => string,
  set: (value: string) => void,
  subscribe: (listener: () => void) => () => void,
): { ensure: () => void; dispose: () => void } {
  host.textContent = "";

  const box = document.createElement("div");
  box.className = "md-editor";
  host.append(box);

  // 界面语言 → Vditor 语言包（Vditor 只认下划线命名）。
  const VDITOR_LANGS: Record<Lang, "zh_CN" | "en_US" | "ja_JP"> = {
    "zh-CN": "zh_CN",
    "en-US": "en_US",
    ja: "ja_JP",
  };

  const editorTheme = (): "dark" | "classic" => (getEffectiveTheme() === "dark" ? "dark" : "classic");
  const contentTheme = (): "dark" | "light" => (getEffectiveTheme() === "dark" ? "dark" : "light");

  let instance: Vditor | null = null;
  let creating = false;
  let applying = false;

  const create = (): void => {
    if (instance !== null || creating) return;
    creating = true;
    // cdn 指向 public/vditor（scripts/prepare-vditor.mjs 同步，dev 与打包同一份），
    // 离线也能加载高亮/i18n/lute 等按需资源。数学公式资源未随包分发，关掉免得去拉。
    // 工具栏在默认的基础上删掉三个：upload（未配上传地址，点了只会报错/卡上传态）、
    // record（录音走 getUserMedia，桌面端没接权限处理会挂起）、export（导出 HTML 与
    // 工作区文档的定位不合，且一样触发下载流程）。
    const vditor = new Vditor(box, {
      mode: "ir",
      lang: VDITOR_LANGS[getLang()],
      cdn: "vditor",
      theme: editorTheme(),
      height: "100%",
      placeholder: t("markdown.placeholder"),
      cache: { enable: false }, // 内容的持久化归 state.ts 管，Vditor 的 localStorage 缓存只会捣乱
      toolbar: [
        "emoji",
        "headings",
        "bold",
        "italic",
        "strike",
        "link",
        "|",
        "list",
        "ordered-list",
        "check",
        "outdent",
        "indent",
        "|",
        "quote",
        "line",
        "code",
        "inline-code",
        "insert-before",
        "insert-after",
        "|",
        "table",
        "|",
        "undo",
        "redo",
        "|",
        "fullscreen",
        "edit-mode",
        {
          name: "more",
          toolbar: ["both", "code-theme", "content-theme", "outline", "preview", "devtools", "info", "help"],
        },
      ],
      preview: {
        theme: { current: contentTheme() },
        markdown: { inlineMath: false, mathBlockPreview: false },
      },
      input(value) {
        if (applying) return;
        if (value === get()) return;
        set(value);
      },
      after() {
        instance = vditor;
        creating = false;
        applying = true;
        vditor.setValue(get());
        vditor.clearStack();
        applying = false;
      },
    });
    // 构造是同步的，初始化（加载 lute 等）异步，完成后进 ir 编辑态；
    // 加载期间盒子保持空，内容在 after 里一次性灌入。
  };

  const sync = (): void => {
    if (instance === null) return;
    const next = get();
    if (applying || instance.getValue() === next) return;
    // 载入别的条目/工作区：整篇替换，顺带清掉上一篇的撤销栈。
    applying = true;
    instance.setValue(next);
    instance.clearStack();
    applying = false;
  };

  const unsubscribeContent = subscribe(sync);

  const unsubscribeTheme = onThemeChange(() => {
    instance?.setTheme(editorTheme(), contentTheme());
  });

  // Vditor 的语言在初始化时定死，切换界面语言只能重建（lute 等脚本已加载，重建很快）。
  const unsubscribeLang = onLangChange(() => {
    if (instance === null) return;
    instance.destroy();
    instance = null;
    create();
  });

  return {
    ensure: create,
    dispose() {
      unsubscribeContent();
      unsubscribeTheme();
      unsubscribeLang();
      instance?.destroy();
      instance = null;
      host.textContent = "";
    },
  };
}
