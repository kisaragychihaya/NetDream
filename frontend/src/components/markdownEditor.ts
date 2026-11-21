import { getLang, onLangChange, t, type Lang } from "../i18n";
import { getEffectiveTheme, onThemeChange } from "../theme";

type VditorModule = typeof import("vditor");
type VditorInstance = InstanceType<VditorModule["default"]>;

// Vditor 只在第一次切到 Markdown 条目时才需要：JS 与样式都走动态 import，
// 启动时主包不再带它（首次加载约几百 KB + 本地 lute 约 3.6M）。
let vditorModule: Promise<VditorModule> | null = null;

function loadVditor(): Promise<VditorModule> {
  vditorModule ??= Promise.all([import("vditor"), import("vditor/dist/index.css")])
    .then(([module]) => module)
    .catch((error: unknown) => {
      // 失败不缓存：下次切回文档条目还能重试。
      vditorModule = null;
      throw error;
    });
  return vditorModule;
}

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

  let instance: VditorInstance | null = null;
  let creating = false;
  let disposed = false;
  let applying = false;
  let errorBox: HTMLElement | null = null;
  // 每次创建换一个 token：超时、销毁、语言重建都让它作废。Vditor 的 after() 与
  // watchdog 都是异步的，迟到的那些只能凭 token + disposed 判断自己还该不该动
  // 界面——旧实例既不能复活，也不能把新实例盖掉。
  let token = 0;
  // 最近一次真正建过实例的 token。Vditor 的 destroy() 会把「构造它的那个元素」
  // 的 innerHTML 还原，盒子已经交给更新的实例时再去 destroy 旧实例会把它删掉。
  let builtToken = 0;

  const valid = (mine: number): boolean => !disposed && mine === token;

  const clearError = (): void => {
    errorBox?.remove();
    errorBox = null;
    box.hidden = false;
  };

  // 加载或初始化失败时把原因摆在宿主里，而不是吞掉：一条简洁消息 + 「重试」按钮，
  // 完整错误照旧打到控制台。
  const showError = (error: unknown): void => {
    const detail = error instanceof Error ? error.message : String(error);
    console.error("[markdownEditor] Vditor 加载失败", error);
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

  // 收拾一个迟到的实例：只在盒子还归这次创建时才 destroy（见 builtToken）。
  const discard = (vditor: VditorInstance): void => {
    try {
      vditor.destroy();
    } catch {
      // 盒子可能已经被清空过；收拾不掉也不影响新实例。
    }
  };

  // 让当前这次创建作废，放开 creating，好让需要的调用方重来一次。
  const invalidate = (): void => {
    token += 1;
    clearWatchdog();
    creating = false;
    box.textContent = "";
  };

  const build = (Vditor: VditorModule["default"], mine: number): void => {
    // 语言在构造时定死：取此刻的语言，after() 里再看它有没有变。
    const buildLang = getLang();
    // cdn 指向 public/vditor（scripts/prepare-vditor.mjs 同步，dev 与打包同一份），
    // 离线也能加载高亮/i18n/lute 等按需资源。数学公式资源未随包分发，关掉免得去拉。
    // 工具栏在默认的基础上删掉三个：upload（未配上传地址，点了只会报错/卡上传态）、
    // record（录音走 getUserMedia，桌面端没接权限处理会挂起）、export（导出 HTML 与
    // 工作区文档的定位不合，且一样触发下载流程）。
    const vditor = new Vditor(box, {
      mode: "ir",
      lang: VDITOR_LANGS[buildLang],
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
        // 这次创建已经作废（超时、销毁、语言重建）：绝不写进 instance、也绝不
        // 碰盒子里新实例的内容。
        if (!valid(mine)) {
          // 盒子已经有更新的实例时连 destroy 都不能做：Vditor 的 destroy 会把
          // 「构造它的那个元素」的 innerHTML 还原，等于删掉新实例的 DOM。
          if (builtToken === mine) discard(vditor);
          return;
        }
        if (buildLang !== getLang()) {
          // 初始化期间界面语言又换了：这个实例起来就是旧语言，丢掉按新语言重建。
          discard(vditor);
          invalidate();
          create();
          return;
        }
        clearWatchdog();
        instance = vditor;
        creating = false;
        clearError();
        applying = true;
        vditor.setValue(get());
        vditor.clearStack();
        applying = false;
      },
    });
    builtToken = mine;
    // 构造是同步的，初始化（加载 lute 等）异步，完成后进 ir 编辑态；
    // 加载期间盒子保持空，内容在 after 里一次性灌入。
  };

  // Vditor 只会在成功时回调 after()：lute 迟迟加载不出来时实例既没就绪也不会报错，
  // 所以留一个兜底——超时就清空盒子并放开 creating，下次切回文档条目可以重建。
  const CREATE_TIMEOUT_MS = 20_000;
  let watchdog: ReturnType<typeof setTimeout> | null = null;

  const clearWatchdog = (): void => {
    if (watchdog === null) return;
    clearTimeout(watchdog);
    watchdog = null;
  };

  const create = (): void => {
    if (instance !== null || creating || disposed) return;
    creating = true;
    token += 1;
    const mine = token;
    void loadVditor()
      .then((module) => {
        if (!valid(mine)) return;
        watchdog = setTimeout(() => {
          watchdog = null;
          if (!valid(mine)) return;
          // 超时：这次创建作废（迟到的 after 只收拾自己的实例），放开 creating
          // 好让下次切回重试；原因摆到界面上，别让用户对着空盒子等。
          invalidate();
          showError(new Error(t("editor.loadTimeout")));
        }, CREATE_TIMEOUT_MS);
        build(module.default, mine);
      })
      .catch((error: unknown) => {
        // 加载或初始化失败：放开 creating，把原因摆出来（点重试或切走再回来都行）。
        if (!valid(mine)) return;
        invalidate();
        showError(error);
      });
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
  // 正在创建时不用管：build 取的是构建那一刻的语言，after 里还会再核对一次。
  const unsubscribeLang = onLangChange(() => {
    if (instance === null) return;
    instance.destroy();
    instance = null;
    create();
  });

  return {
    ensure: create,
    dispose() {
      disposed = true;
      token += 1; // 迟到的 after/watchdog 一律作废
      clearWatchdog();
      creating = false;
      clearError();
      unsubscribeContent();
      unsubscribeTheme();
      unsubscribeLang();
      instance?.destroy();
      instance = null;
      host.textContent = "";
    },
  };
}
