/**
 * 设置页：左侧导航 + 右侧内容的全屏弹层。
 *
 * 单例：导航目前只有「关于」一项，内容固定为应用标题、logo、副标题、
 * 版本号、许可证与 GitHub 主页链接。
 */

import { AppService } from "../mygo";

const GITHUB_URL = "https://github.com/kisaragychihaya/NetDream";

export interface SettingsDialog {
  /** 打开设置页；已经打开时聚焦「关于」导航。 */
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
  window_.setAttribute("aria-label", "设置");

  const nav = document.createElement("nav");
  nav.className = "settings-nav";
  nav.setAttribute("aria-label", "设置导航");
  const navTitle = document.createElement("div");
  navTitle.className = "settings-nav-title";
  navTitle.textContent = "设置";
  const aboutItem = document.createElement("button");
  aboutItem.type = "button";
  aboutItem.className = "settings-nav-item active";
  aboutItem.textContent = "关于";
  nav.append(navTitle, aboutItem);

  const content = document.createElement("div");
  content.className = "settings-content";

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

  const addRow = (label: string): HTMLElement => {
    const term = document.createElement("dt");
    term.textContent = label;
    const value = document.createElement("dd");
    meta.append(term, value);
    return value;
  };

  const version = addRow("版本号");
  version.textContent = "…";
  const license = addRow("许可证");
  license.textContent = "MIT";
  const github = addRow("GitHub");
  const githubLink = document.createElement("button");
  githubLink.type = "button";
  githubLink.className = "settings-link";
  githubLink.textContent = GITHUB_URL;
  githubLink.title = "在浏览器中打开";
  github.append(githubLink);

  content.append(title, logo, tagline, meta);
  window_.append(nav, content);
  overlay.append(window_);
  document.body.append(overlay);

  // 版本号来自打包信息（mygo.config.ts 的 version），每次打开现取；
  // dev 模式下没有打包信息，拿到空串就退回显示 dev。
  const loadVersion = (): void => {
    AppService.version()
      .then((value) => {
        version.textContent = value.trim() === "" ? "dev" : value;
      })
      .catch(() => {
        version.textContent = "未知";
      });
  };

  githubLink.addEventListener("click", () => {
    void AppService.openURL(GITHUB_URL).catch(() => {
      // 打不开外部浏览器时没有别的补救，链接留在原地。
    });
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
      aboutItem.focus();
    },
  };
}
