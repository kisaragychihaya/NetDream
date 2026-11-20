/**
 * 设置页：左侧导航 + 右侧内容的全屏弹层。
 *
 * 单例：导航分「界面设置」与「关于」两项。界面设置页放界面语言与主题两个
 * 下拉，关于页放应用标题、logo、副标题、版本号、许可证与 GitHub 主页链接。
 *
 * 文案统一标 data-i18n / data-i18n-attr 由 i18n 的 applyStatic() 刷新：弹层开着
 * 时切语言，只有下拉的选项文案与选中值需要就地同步。
 */

import {
  LANG_LABEL_KEYS,
  LANG_PREFS,
  applyStatic,
  getLangPref,
  onLangChange,
  setLang,
  t,
  type LangPref,
} from "../i18n";
import { AppService } from "../mygo";
import { THEME_MODES, getThemeMode, onThemeChange, setThemeMode, type ThemeMode } from "../theme";

const GITHUB_URL = "https://github.com/kisaragychihaya/NetDream";

/** 主题选项的文案键：跟着界面语言翻译。 */
const THEME_LABEL_KEYS: Record<ThemeMode, string> = {
  light: "settings.interface.themeLight",
  dark: "settings.interface.themeDark",
  auto: "settings.interface.themeAuto",
};

type Section = "interface" | "about";

export interface SettingsDialog {
  /** 打开设置页；已经打开时聚焦当前导航项。 */
  open: () => void;
}

export function mountSettings(): SettingsDialog {
  const overlay = document.createElement("div");
  overlay.className = "settings-overlay";
  overlay.hidden = true;

  const window_ = document.createElement("div");
  window_.className = "settings-window";
  window_.setAttribute("role", "dialog");
  window_.setAttribute("aria-modal", "true");
  window_.setAttribute("aria-label", t("settings.title"));
  window_.dataset.i18nAttr = "aria-label:settings.title";

  const nav = document.createElement("nav");
  nav.className = "settings-nav";
  nav.setAttribute("aria-label", t("settings.nav"));
  nav.dataset.i18nAttr = "aria-label:settings.nav";
  const navTitle = document.createElement("div");
  navTitle.className = "settings-nav-title";
  navTitle.textContent = t("settings.title");
  navTitle.dataset.i18n = "settings.title";

  const items = new Map<Section, HTMLButtonElement>();
  for (const [section, key] of [
    ["interface", "settings.interface.title"],
    ["about", "settings.about"],
  ] as const) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "settings-nav-item";
    item.dataset.section = section;
    item.textContent = t(key);
    item.dataset.i18n = key;
    items.set(section, item);
    nav.append(item);
  }
  nav.prepend(navTitle);

  const content = document.createElement("div");
  content.className = "settings-content";

  /* ---- 界面设置：语言与主题 ---- */

  const fields = document.createElement("div");
  fields.className = "settings-fields";

  const langField = document.createElement("label");
  langField.className = "field";
  const langLabel = document.createElement("span");
  langLabel.textContent = t("settings.interface.language");
  langLabel.dataset.i18n = "settings.interface.language";
  const langSelect = document.createElement("select");
  langSelect.id = "settings-lang";
  langField.append(langLabel, langSelect);

  const themeField = document.createElement("label");
  themeField.className = "field";
  const themeLabel = document.createElement("span");
  themeLabel.textContent = t("settings.interface.theme");
  themeLabel.dataset.i18n = "settings.interface.theme";
  const themeSelect = document.createElement("select");
  themeSelect.id = "settings-theme";
  themeField.append(themeLabel, themeSelect);

  // 选项文案随界面语言变，所以每次切语言都重建一遍，并把选中值放回当前值。
  const fillOptions = <T extends string>(
    select: HTMLSelectElement,
    values: readonly T[],
    labelOf: (value: T) => string,
    selected: T,
  ): void => {
    select.replaceChildren(
      ...values.map((value) => {
        const option = document.createElement("option");
        option.value = value;
        option.textContent = labelOf(value);
        return option;
      }),
    );
    select.value = selected;
  };

  const fillLangOptions = (): void => {
    fillOptions(langSelect, LANG_PREFS, (value: LangPref) => t(LANG_LABEL_KEYS[value]), getLangPref());
  };

  const fillThemeOptions = (): void => {
    fillOptions(themeSelect, THEME_MODES, (value: ThemeMode) => t(THEME_LABEL_KEYS[value]), getThemeMode());
  };

  fillLangOptions();
  fillThemeOptions();

  fields.append(langField, themeField);

  /* ---- 关于 ---- */

  const title = document.createElement("h1");
  title.className = "settings-title";
  title.textContent = "NetDream";

  const logo = document.createElement("img");
  logo.className = "settings-logo";
  logo.src = "./icon.png";
  logo.alt = "NetDream logo";

  const tagline = document.createElement("p");
  tagline.className = "settings-tagline";
  tagline.textContent = "NetDream! It's MyGO!!!!!";

  const meta = document.createElement("dl");
  meta.className = "settings-meta";

  const addRow = (labelKey: string): HTMLElement => {
    const term = document.createElement("dt");
    term.textContent = t(labelKey);
    term.dataset.i18n = labelKey;
    const value = document.createElement("dd");
    meta.append(term, value);
    return value;
  };

  const version = addRow("settings.version");
  version.textContent = "…";
  const license = addRow("settings.license");
  license.textContent = "MIT";
  const github = addRow("settings.github");
  github.textContent = "GitHub";
  const githubLink = document.createElement("button");
  githubLink.type = "button";
  githubLink.className = "settings-link";
  githubLink.textContent = GITHUB_URL;
  githubLink.title = t("settings.openInBrowser");
  githubLink.dataset.i18nAttr = "title:settings.openInBrowser";
  github.append(githubLink);

  const panels = new Map<Section, HTMLElement[]>([
    ["interface", [fields]],
    ["about", [title, logo, tagline, meta]],
  ]);

  let section: Section = "interface";

  // 两页的节点都是现成的：切页只换容器里的孩子，不重建（重建会打断语言下拉本身）。
  const showSection = (next: Section): void => {
    section = next;
    for (const [id, item] of items) {
      item.classList.toggle("active", id === next);
    }
    const nodes = panels.get(next);
    if (nodes !== undefined) content.replaceChildren(...nodes);
    // 换页时节点刚从脱离状态挂回来：data-i18n 标注按当前语言补刷一次
    //（脱离期间切语言，applyStatic 找不到它们）。
    applyStatic();
  };

  for (const [id, item] of items) {
    item.addEventListener("click", () => showSection(id));
  }

  window_.append(nav, content);
  overlay.append(window_);
  document.body.append(overlay);

  showSection(section);

  // 版本号来自打包信息（mygo.config.ts 的 version），每次打开现取；
  // dev 模式下没有打包信息，拿到空串就退回显示 dev。
  let versionMissing = false;
  const loadVersion = (): void => {
    AppService.version()
      .then((value) => {
        versionMissing = false;
        version.textContent = value.trim() === "" ? "dev" : value;
      })
      .catch(() => {
        versionMissing = true;
        version.textContent = t("settings.versionUnknown");
      });
  };

  githubLink.addEventListener("click", () => {
    void AppService.openURL(GITHUB_URL).catch(() => {
      // 打不开外部浏览器时没有别的补救，链接留在原地。
    });
  });

  langSelect.addEventListener("change", () => {
    const next = LANG_PREFS.find((pref) => pref === langSelect.value);
    if (next !== undefined) setLang(next);
  });

  themeSelect.addEventListener("change", () => {
    const next = THEME_MODES.find((mode) => mode === themeSelect.value);
    if (next !== undefined) setThemeMode(next);
  });

  // 切语言：静态文案由 applyStatic() 刷新，这里重建两个下拉的选项文案，
  // 并同步版本号兜底文案（它是运行时按语言取的，不在静态标注里）。
  onLangChange(() => {
    fillLangOptions();
    fillThemeOptions();
    if (versionMissing) version.textContent = t("settings.versionUnknown");
  });

  // 主题变化只需要同步选中值：跟随系统时系统配色变了，选中值仍是「跟随系统」。
  onThemeChange(() => {
    themeSelect.value = getThemeMode();
  });

  overlay.addEventListener("mousedown", (event) => {
    if (event.target === overlay) hide();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || overlay.hidden) return;
    event.preventDefault();
    hide();
  });

  const hide = (): void => {
    overlay.hidden = true;
  };

  return {
    open: () => {
      overlay.hidden = false;
      loadVersion();
      items.get(section)?.focus();
    },
  };
}
