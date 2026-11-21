/**
 * 页面是否跑在 MyGo 窗口里。纯浏览器（`vite dev` / `vite preview`、单测桩）没有
 * `globalThis.mygo`，任何经 mygo-runtime 的调用都会抛「runtime not found」。
 *
 * 这条判断只用来给「只有桌面端才有意义」的订阅加守卫：模块求值期抛出的异常会让
 * 入口模块进入 errored 状态，之后任何 import 它的 chunk（Monaco 的 editor.api 就是）
 * 都会连带失败。
 */
export function isMyGo(): boolean {
  return typeof (globalThis as { mygo?: unknown }).mygo !== "undefined";
}
