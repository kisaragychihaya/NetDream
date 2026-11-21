// 脚本编辑器用到的 Monaco contribution：每个模块都是副作用导入，注册命令与界面部件。
// 单独一个模块是因为它们都没有类型声明（`import()` 需要类型，副作用导入不需要），
// monacoSetup 只动态 import 这一个，整组随编辑器一起按需加载。
//
// 覆盖：括号匹配、剪贴板、快速修复、注释、查找替换、折叠、格式化、问题跳转、悬浮、
// 行操作、多光标、签名提示、选中扩展、代码片段、补全、出现处高亮、按词移动。
import "monaco-editor/editor/contrib/bracketMatching/browser/bracketMatching.js";
import "monaco-editor/editor/contrib/clipboard/browser/clipboard.js";
import "monaco-editor/editor/contrib/codeAction/browser/codeActionContributions.js";
import "monaco-editor/editor/contrib/comment/browser/comment.js";
import "monaco-editor/editor/contrib/find/browser/findController.js";
import "monaco-editor/editor/contrib/folding/browser/folding.js";
import "monaco-editor/editor/contrib/format/browser/formatActions.js";
import "monaco-editor/editor/contrib/gotoError/browser/gotoError.js";
import "monaco-editor/editor/contrib/hover/browser/hoverContribution.js";
import "monaco-editor/editor/contrib/linesOperations/browser/linesOperations.js";
import "monaco-editor/editor/contrib/multicursor/browser/multicursor.js";
import "monaco-editor/editor/contrib/parameterHints/browser/parameterHints.js";
import "monaco-editor/editor/contrib/smartSelect/browser/smartSelect.js";
import "monaco-editor/editor/contrib/snippet/browser/snippetController2.js";
import "monaco-editor/editor/contrib/suggest/browser/suggestController.js";
import "monaco-editor/editor/contrib/wordHighlighter/browser/wordHighlighter.js";
import "monaco-editor/editor/contrib/wordOperations/browser/wordOperations.js";

// JavaScript 语法高亮（tokenizer 本身在 register 里懒加载）。
import "monaco-editor/languages/definitions/javascript/register.js";

export {};
