/**
 * 主题：浅色 / 深色 / 跟随系统。
 *
 * 存的是「偏好」，生效主题由偏好与系统配色算出来：
 * - 偏好落 localStorage["netdream.theme"]（light / dark / auto，缺省 auto）；
 * - 偏好同步到 <html data-theme>，具体配色全在 style.css 里：
 *   data-theme="dark" / "light" 直接定死，data-theme="auto" 交给
 *   @media (prefers-color-scheme: dark) 自动跟随；
 * - 首屏防闪烁由 index.html 的内联脚本负责（模块脚本执行太晚，会先闪一下
 *   亮色），这里只维护运行期的一致性与通知。
 *
 * 跟随系统时系统配色变了，CSS 自己会跟着变，但画布类组件（Monaco）得靠
 * onThemeChange 手动换主题。
 */

export type ThemeMode = "light" | "dark" | "auto";

/** 实际生效的配色，只有两种。 */
export type EffectiveTheme = "light" | "dark";

export const THEME_MODES: readonly ThemeMode[] = ["light", "dark", "auto"];

export const THEME_STORAGE_KEY = "netdream.theme";

const DEFAULT_MODE: ThemeMode = "auto";

type ThemeListener = (effective: EffectiveTheme, mode: ThemeMode) => void;
const listeners = new Set<ThemeListener>();

// 系统配色媒体查询；没有 matchMedia 的环境（如测试桩）按浅色处理。
const darkQuery: MediaQueryList | null =
  typeof window === "undefined" || typeof window.matchMedia !== "function"
    ? null
    : window.matchMedia("(prefers-color-scheme: dark)");

let mode: ThemeMode = DEFAULT_MODE;

function isThemeMode(value: string | null): value is ThemeMode {
  return value !== null && (THEME_MODES as readonly string[]).includes(value);
}

function prefersDark(): boolean {
  return darkQuery !== null && darkQuery.matches;
}

function applyAttribute(): void {
  document.documentElement.dataset.theme = mode;
}

function readStoredMode(): ThemeMode {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    return isThemeMode(stored) ? stored : DEFAULT_MODE;
  } catch {
    // 存储被禁用时按跟随系统走。
    return DEFAULT_MODE;
  }
}

/** 当前主题偏好（可能是 "auto"）。 */
export function getThemeMode(): ThemeMode {
  return mode;
}

/** 当前真正生效的配色：偏好是 light / dark 就是它，auto 看系统。 */
export function getEffectiveTheme(): EffectiveTheme {
  if (mode !== "auto") return mode;
  return prefersDark() ? "dark" : "light";
}

function notify(): void {
  const effective = getEffectiveTheme();
  for (const listener of [...listeners]) {
    try {
      listener(effective, mode);
    } catch (error) {
      console.error("[theme] theme listener failed:", error);
    }
  }
}

/** 切换主题偏好：落盘、同步 <html data-theme>、通知跟着变的组件。 */
export function setThemeMode(next: ThemeMode): void {
  mode = next;
  try {
    localStorage.setItem(THEME_STORAGE_KEY, next);
  } catch {
    // 存储不可用只影响下次启动的默认主题。
  }
  applyAttribute();
  notify();
}

/** 注册主题变化回调，返回取消注册函数。 */
export function onThemeChange(listener: ThemeListener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// 系统配色变化：只在跟随系统时需要通知（dataset 保持 "auto"，颜色由 CSS 接管）。
darkQuery?.addEventListener("change", () => {
  if (mode === "auto") notify();
});

mode = readStoredMode();
applyAttribute();
