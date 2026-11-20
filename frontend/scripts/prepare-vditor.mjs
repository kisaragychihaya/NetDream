// Vditor 运行时会按需从 cdn 加载资源（代码高亮、编辑引擎 lute、i18n、图标、
// emoji、内容主题、mermaid 等），桌面应用离线不能访问 unpkg，所以把用到的
// 资源从 node_modules 同步到 public/vditor/dist，初始化时把 cdn 指到本地。
// dev（Vite 伺服 public/）与打包（mygo 原样内嵌 dist）共用这一份。
import { cpSync, existsSync, mkdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = join(dirname(fileURLToPath(import.meta.url)), "..");
const source = join(frontend, "node_modules", "vditor", "dist");
const target = join(frontend, "public", "vditor", "dist");

if (!existsSync(source)) {
  console.error("[vditor] 未找到 node_modules/vditor/dist，先安装依赖");
  process.exit(1);
}

rmSync(target, { recursive: true, force: true });
mkdirSync(target, { recursive: true });

// 只拷用到的：数学公式（katex/mathjax）、echarts/graphviz 等冷门图表默认不开，
// 拷进来只白占打包体积；文档里真出现那些代码块时按普通代码块显示。
const paths = ["css", "images", "js/i18n", "js/icons", "js/highlight.js", "js/lute", "js/mermaid"];
for (const item of paths) {
  cpSync(join(source, item), join(target, item), { recursive: true });
}
console.log(`[vditor] 已同步 ${paths.length} 类资源到 public/vditor/dist`);
