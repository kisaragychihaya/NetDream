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
import { AppService, IdentityService, type IdentityInfo, type TrustedIdentity } from "../mygo";
import { getCachedIdentity, setCachedIdentity } from "../state";
import { THEME_MODES, getThemeMode, onThemeChange, setThemeMode, type ThemeMode } from "../theme";

const GITHUB_URL = "https://github.com/kisaragychihaya/NetDream";

/** 主题选项的文案键：跟着界面语言翻译。 */
const THEME_LABEL_KEYS: Record<ThemeMode, string> = {
  light: "settings.interface.themeLight",
  dark: "settings.interface.themeDark",
  auto: "settings.interface.themeAuto",
};

// 公钥与指纹的展示长度：指纹取前 16 位（与信任弹窗一致），完整值放 title 与
// 只读输入框里，方便贴给别人核对。
const FINGERPRINT_DISPLAY_LENGTH = 16;
const TRUSTED_FINGERPRINT_LENGTH = 8;

type Section = "interface" | "identity" | "about";

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// 复制公钥：navigator.clipboard 在 webview 里偶尔不可用（没有聚焦、权限被拒），
// 退回选中文本 + execCommand（与响应区复制同一套兜底）。
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

  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }

  area.remove();
  return copied;
}

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
    ["identity", "settings.identity.title"],
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

  /* ---- 身份管理：展示信息可改，密钥只读 ---- */

  const identityPanel = document.createElement("div");
  identityPanel.className = "identity-panel";

  // 展示信息（email / id）可以改：后端只动传了非空的那一项，指纹与密钥不变。
  const emailInput = document.createElement("input");
  emailInput.type = "email";
  emailInput.spellcheck = false;
  emailInput.autocomplete = "off";
  emailInput.id = "identity-email";

  const idInput = document.createElement("input");
  idInput.type = "text";
  idInput.spellcheck = false;
  idInput.autocomplete = "off";
  idInput.id = "identity-id";

  const makeSaveButton = (): HTMLButtonElement => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "identity-btn";
    button.textContent = t("settings.identity.save");
    button.dataset.i18n = "settings.identity.save";
    return button;
  };

  const emailSave = makeSaveButton();
  const idSave = makeSaveButton();

  const fieldRow = (labelKey: string, input: HTMLInputElement, save: HTMLButtonElement): HTMLElement => {
    const row = document.createElement("div");
    row.className = "identity-field";
    const label = document.createElement("span");
    label.textContent = t(labelKey);
    label.dataset.i18n = labelKey;
    row.append(label, input, save);
    return row;
  };

  const identityMeta = document.createElement("dl");
  identityMeta.className = "identity-meta";

  const addMetaRow = (labelKey: string): HTMLElement => {
    const term = document.createElement("dt");
    term.textContent = t(labelKey);
    term.dataset.i18n = labelKey;
    const value = document.createElement("dd");
    identityMeta.append(term, value);
    return value;
  };

  const fingerprintValue = addMetaRow("settings.identity.fingerprint");
  fingerprintValue.classList.add("mono");

  const copyButtons: HTMLButtonElement[] = [];

  // 公钥行：只读输入框（完整 base64）+「复制」。
  const addKeyRow = (labelKey: string): HTMLInputElement => {
    const value = addMetaRow(labelKey);
    const holder = document.createElement("div");
    holder.className = "identity-key";
    const input = document.createElement("input");
    input.type = "text";
    input.readOnly = true;
    input.className = "mono";
    input.spellcheck = false;
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "identity-btn";
    copy.textContent = t("common.copy");
    copy.title = t("common.copy");
    copy.addEventListener("click", () => {
      void (async () => {
        if (input.value === "") return;
        const copied = await copyText(input.value);
        copy.textContent = copied ? t("common.copied") : t("common.copyFailed");
        copy.title = copy.textContent;
        window.setTimeout(() => {
          copy.textContent = t("common.copy");
          copy.title = t("common.copy");
        }, 1500);
      })();
    });
    copyButtons.push(copy);
    holder.append(input, copy);
    value.append(holder);
    return input;
  };

  const ed25519Input = addKeyRow("settings.identity.ed25519");
  const ecdhInput = addKeyRow("settings.identity.ecdh");

  const identityError = document.createElement("p");
  identityError.className = "identity-error";
  identityError.hidden = true;

  const trustedTitle = document.createElement("div");
  trustedTitle.className = "identity-trusted-title";
  trustedTitle.textContent = t("settings.identity.trusted");
  trustedTitle.dataset.i18n = "settings.identity.trusted";

  const trustedList = document.createElement("div");
  trustedList.className = "identity-trusted-list";

  identityPanel.append(
    fieldRow("settings.identity.email", emailInput, emailSave),
    fieldRow("settings.identity.id", idInput, idSave),
    identityMeta,
    identityError,
    trustedTitle,
    trustedList,
  );

  let identity: IdentityInfo | null = getCachedIdentity();
  let trusted: TrustedIdentity[] = [];

  const showIdentityError = (message: string | null): void => {
    identityError.hidden = message === null;
    identityError.textContent = message ?? "";
  };

  const renderIdentity = (): void => {
    const ready = identity !== null;
    // 还没建立身份：输入框留空且不可编辑，需要的话从首次运行窗里建立。
    emailInput.value = identity?.email ?? "";
    idInput.value = identity?.id ?? "";
    ed25519Input.value = identity?.ed25519 ?? "";
    ecdhInput.value = identity?.ecdh ?? "";
    fingerprintValue.textContent = (identity?.fingerprint ?? "").slice(0, FINGERPRINT_DISPLAY_LENGTH);
    fingerprintValue.title = identity?.fingerprint ?? "";
    for (const button of [emailSave, idSave]) button.disabled = !ready;
    emailInput.disabled = !ready;
    idInput.disabled = !ready;
    for (const button of copyButtons) button.disabled = !ready;
  };

  const renderTrusted = (): void => {
    if (trusted.length === 0) {
      const empty = document.createElement("p");
      empty.className = "identity-trusted-empty";
      empty.textContent = t("settings.identity.trustedEmpty");
      trustedList.replaceChildren(empty);
      return;
    }
    trustedList.replaceChildren(
      ...trusted.map((row) => {
        const line = document.createElement("div");
        line.className = "identity-trusted-row";

        const text = document.createElement("span");
        text.className = "identity-trusted-text";
        const name = document.createElement("span");
        name.className = "identity-trusted-name";
        // 对方给的展示信息不可信但只是展示：一律 textContent。
        name.textContent = row.email !== "" ? row.email : row.id;
        const meta = document.createElement("span");
        meta.className = "identity-trusted-meta";
        const fingerprint = document.createElement("span");
        fingerprint.className = "mono";
        fingerprint.textContent = row.fingerprint.slice(0, TRUSTED_FINGERPRINT_LENGTH);
        fingerprint.title = row.fingerprint;
        meta.append(row.id !== "" && row.id !== row.email ? `${row.id} · ` : "", fingerprint);
        text.append(name, meta);

        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "identity-btn danger";
        remove.textContent = t("settings.identity.remove");
        remove.addEventListener("click", () => {
          remove.disabled = true;
          void (async () => {
            try {
              await IdentityService.untrust(row.fingerprint);
              trusted = trusted.filter((item) => item.fingerprint !== row.fingerprint);
              showIdentityError(null);
            } catch (cause) {
              showIdentityError(t("settings.identity.untrustFailed", { error: messageOf(cause) }));
            }
            renderTrusted();
          })();
        });

        line.append(text, remove);
        return line;
      }),
    );
  };

  // 拉一次身份与信任列表：打开设置页时、以及信任列表被改动后。
  const refreshIdentity = (): void => {
    void (async () => {
      try {
        const next = await IdentityService.get();
        identity = next.status === "ready" ? next : null;
        setCachedIdentity(identity);
        showIdentityError(null);
      } catch (cause) {
        showIdentityError(t("settings.identity.loadFailed", { error: messageOf(cause) }));
      }
      renderIdentity();
      try {
        trusted = await IdentityService.trusted();
      } catch (cause) {
        trusted = [];
        showIdentityError(t("settings.identity.loadFailed", { error: messageOf(cause) }));
      }
      renderTrusted();
    })();
  };

  const saveField = (field: "email" | "id", button: HTMLButtonElement): void => {
    if (identity === null) return;
    const email = field === "email" ? emailInput.value.trim() : "";
    const id = field === "id" ? idInput.value.trim() : "";
    // 空串后端按「这一项不动」处理：没改就别白跑一次。
    if ((field === "email" ? email : id) === "") return;
    button.disabled = true;
    void (async () => {
      try {
        const next = await IdentityService.update(email, id);
        identity = next.status === "ready" ? next : null;
        setCachedIdentity(identity);
        showIdentityError(null);
      } catch (cause) {
        showIdentityError(t("settings.identity.saveFailed", { error: messageOf(cause) }));
      }
      renderIdentity();
    })();
  };

  emailSave.addEventListener("click", () => saveField("email", emailSave));
  idSave.addEventListener("click", () => saveField("id", idSave));
  emailInput.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    saveField("email", emailSave);
  });
  idInput.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    saveField("id", idSave);
  });

  renderIdentity();
  renderTrusted();

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
    ["identity", [identityPanel]],
    ["about", [title, logo, tagline, meta]],
  ]);

  let section: Section = "interface";

  // 三页的节点都是现成的：切页只换容器里的孩子，不重建（重建会打断语言下拉本身）。
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
    // 身份区显示的是后端此刻的状态：信任列表可能被别处改过，进页就重取一次。
    if (next === "identity") refreshIdentity();
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

  // 切语言：静态文案由 applyStatic() 刷新，这里重建两个下拉的选项文案、
  // 身份区的信任列表（行内文案是现取的），并同步版本号兜底文案（它是运行时按
  // 语言取的，不在静态标注里）。
  onLangChange(() => {
    fillLangOptions();
    fillThemeOptions();
    renderTrusted();
    // 公钥的「复制」按钮文案不在 data-i18n 标注里（点一下会临时变成「已复制」），
    // 这里按当前语言复原一次。
    for (const button of copyButtons) {
      button.textContent = t("common.copy");
      button.title = t("common.copy");
    }
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
      // 打开设置就取一次身份：首次运行窗关掉后、或身份在别处改过，这里看到的是
      // 后端当前的值，而不是上次留在界面上的。
      refreshIdentity();
      items.get(section)?.focus();
    },
  };
}
