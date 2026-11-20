/**
 * 轻量 i18n：静态 JSON 词典 + 自写 t()，不依赖第三方库（对齐 dsh-vig 的
 * 界面多语言方案，改成同步加载：词典直接 import，模块加载即可用）。
 *
 * 用法：
 * - 静态文案在元素上标 data-i18n="section.key"（刷 textContent）或
 *   data-i18n-attr="placeholder:key;title:key"（刷属性），applyStatic() 统一刷新；
 * - 动态文案用 t('section.key', { var })，支持点路径查嵌套词典与 {{var}} 插值，
 *   缺键先回退 zh-CN 词典，再回退键本身；
 * - 组件挂载时 onLangChange(cb)，语言切换时刷新自己生成的 DOM；
 *   setLang 流程：换偏好 → applyStatic → 逐个调回调。
 *
 * 「偏好」与「生效语言」是两件事：偏好可以是 auto（跟随系统），生效语言永远
 * 是具体的一本词典。t() / getLang() 用生效语言，getLangPref() 用偏好。
 */

import { locale as mygoLocale, onLocaleChange } from "mygo-runtime";

import enUS from "./locales/en-US.json";
import ja from "./locales/ja.json";
import zhCN from "./locales/zh-CN.json";

export type Lang = "zh-CN" | "en-US" | "ja";

/** 界面语言偏好：具体语言，或跟随系统。 */
export type LangPref = "auto" | Lang;

/** 嵌套词典：叶子是文案，中间节点是子节。 */
export type Dict = { readonly [key: string]: string | Dict };

export const LANGS: readonly Lang[] = ["zh-CN", "en-US", "ja"];

/** 下拉里的全部选项，含跟随系统。 */
export const LANG_PREFS: readonly LangPref[] = ["auto", "zh-CN", "en-US", "ja"];

/** 每种偏好的显示名键：跟着界面语言翻译，所以走词典。 */
export const LANG_LABEL_KEYS: Record<LangPref, string> = {
  auto: "settings.interface.langAuto",
  "zh-CN": "settings.interface.langZh",
  "en-US": "settings.interface.langEn",
  ja: "settings.interface.langJa",
};

export const LANG_STORAGE_KEY = "netdream.lang";

const DEFAULT_LANG_PREF: LangPref = "auto";
const FALLBACK_LANG: Lang = "zh-CN";

const DICTS: Record<Lang, Dict> = { "zh-CN": zhCN, "en-US": enUS, ja };

type LangListener = (lang: Lang) => void;
const listeners = new Set<LangListener>();

let current: Lang = FALLBACK_LANG;
let pref: LangPref = DEFAULT_LANG_PREF;

function isLangPref(value: string | null): value is LangPref {
  return value !== null && (LANG_PREFS as readonly string[]).includes(value);
}

// zh 开头一律简体中文（本项目没有繁体词典），ja 开头日文，其余回落英文。
function fromLocale(locale: string): Lang {
  const lower = locale.toLowerCase();
  if (lower.startsWith("zh")) return "zh-CN";
  if (lower.startsWith("ja")) return "ja";
  return "en-US";
}

/**
 * 系统语言：优先 mygo-runtime 的 locale()——MyGo 把系统首选语言与
 * 后端 SetLanguages 声明的语言（backend/main.go，勿删，否则只剩英语）
 * 匹配后注入页面；WebView2 的 navigator.language 不跟系统语言走，不能
 * 用作跟随系统的依据。页面跑在 MyGo 窗口外（如纯浏览器调试）时取不到，
 * 退回 navigator.language，再缺退回中文。
 */
function systemLang(): Lang {
  try {
    const tag = mygoLocale();
    if (tag !== "") return fromLocale(tag);
  } catch {
    // 非 MyGo 环境，走下面的 navigator 兜底。
  }
  const nav = typeof navigator === "undefined" ? "" : navigator.language;
  return nav === "" || nav === undefined ? FALLBACK_LANG : fromLocale(nav);
}

/** 初始语言偏好：本地存储的偏好 → 跟随系统。 */
export function detectLangPref(): LangPref {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(LANG_STORAGE_KEY);
  } catch {
    // 存储被禁用时按系统语言走。
    stored = null;
  }
  return isLangPref(stored) ? stored : DEFAULT_LANG_PREF;
}

/** 把偏好算成生效语言：auto 看系统语言，其余就是偏好本身。 */
export function resolveLang(value: LangPref): Lang {
  return value === "auto" ? systemLang() : value;
}

/** 初始语言（生效值）。 */
export function detectLang(): Lang {
  return resolveLang(detectLangPref());
}

// 点路径查嵌套词典；找不到或值不是字符串时返回 undefined
function lookup(dict: Dict, key: string): string | undefined {
  let node: string | Dict | undefined = dict;
  for (const part of key.split(".")) {
    if (typeof node !== "object" || node === null) return undefined;
    node = node[part];
  }
  return typeof node === "string" ? node : undefined;
}

// {{var}} 插值；未提供的变量保留原样（便于发现漏传）
function interpolate(text: string, vars?: Record<string, string | number>): string {
  if (vars === undefined) return text;
  return text.replace(/\{\{\s*(\w+)\s*\}\}/g, (match, name: string) => {
    const value = vars[name];
    return value !== undefined ? String(value) : match;
  });
}

/** 查当前语言文案；缺键回退 zh-CN，再缺回退键本身。 */
export function t(key: string, vars?: Record<string, string | number>): string {
  let text = lookup(DICTS[current], key);
  if (text === undefined) text = lookup(DICTS[FALLBACK_LANG], key);
  if (text === undefined) return key;
  return interpolate(text, vars);
}

/** 当前生效语言。 */
export function getLang(): Lang {
  return current;
}

/** 当前语言偏好（可能是 "auto"）。 */
export function getLangPref(): LangPref {
  return pref;
}

// 刷新所有静态标注（含运行时补标 data-i18n 的元素），并同步 <html lang>
export function applyStatic(): void {
  document.documentElement.lang = current;
  for (const element of document.querySelectorAll<HTMLElement>("[data-i18n]")) {
    const key = element.dataset.i18n;
    if (key === undefined) continue;
    element.textContent = t(key);
  }
  for (const element of document.querySelectorAll<HTMLElement>("[data-i18n-attr]")) {
    const spec = element.dataset.i18nAttr;
    if (spec === undefined) continue;
    // 格式：placeholder:key;title:key
    for (const pair of spec.split(";")) {
      const index = pair.indexOf(":");
      if (index < 0) continue;
      const attribute = pair.slice(0, index).trim();
      const key = pair.slice(index + 1).trim();
      if (attribute !== "" && key !== "") element.setAttribute(attribute, t(key));
    }
  }
}

/**
 * 切换语言偏好：落盘偏好、刷静态文案、通知各组件重刷。
 * 生效语言没变时（例如"跟随系统"与具体语言恰好一致）不重刷——
 * 界面上没有任何文案需要改，重刷只是白费功夫。
 */
export function setLang(next: LangPref): void {
  pref = next;
  try {
    localStorage.setItem(LANG_STORAGE_KEY, next);
  } catch {
    // 存储不可用只影响下次启动的默认语言。
  }
  const effective = resolveLang(next);
  if (effective === current) return;
  current = effective;
  applyStatic();
  for (const listener of [...listeners]) {
    try {
      listener(current);
    } catch (error) {
      console.error("[i18n] language listener failed:", error);
    }
  }
}

/** 注册语言切换回调，返回取消注册函数。 */
export function onLangChange(listener: LangListener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

pref = detectLangPref();
current = resolveLang(pref);
// 首屏：index.html 上的静态标注（data-i18n-attr）按初始语言刷一遍。
// 模块脚本在 HTML 解析完成后执行，这里的节点都已就位。
applyStatic();

// 系统语言变化（mygo 广播）：跟随系统时重算生效语言，变了才重刷。
try {
  onLocaleChange(() => {
    if (pref !== "auto") return;
    const effective = systemLang();
    if (effective === current) return;
    current = effective;
    applyStatic();
    for (const listener of [...listeners]) {
      try {
        listener(current);
      } catch (error) {
        console.error("[i18n] language listener failed:", error);
      }
    }
  });
} catch {
  // 非 MyGo 环境没有广播可订，忽略。
}
