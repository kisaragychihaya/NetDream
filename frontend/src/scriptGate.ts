/**
 * 脚本信任门：HTTP 发送与连接类（WS/TCP/Socket.IO）建立连接共用同一套流程。
 *
 * 打开的 .band 里的脚本能读变量、请求头与请求体，也能改写 URL / 连接地址，再由
 * 后端附上认证发出去——本次载入没确认过信任之前一次都不运行。不可信时只弹
 * components/scriptTrust.ts 的两键框（「信任并运行 / 不运行」），流程就此返回；
 * 用户确认后由 resume 从流程起点重来一遍（重新判断编辑器、工作区与脚本）。
 *
 * 弹框时记下当时的 `{generation, editor}`：确认时只有两者都还匹配才放行并
 * `trustWorkspaceScripts()`，否则什么都不做——弹框期间拖入/打开了另一份工作区、
 * 或切到别的条目，这次确认都不会被算作对新界面的放行。
 */
import { mountScriptTrustDialog } from "./components/scriptTrust";
import {
  getRequest,
  getWorkspaceGeneration,
  scriptsTrustedInWorkspace,
  trustWorkspaceScripts,
  type RequestState,
} from "./state";

interface PendingTrust {
  generation: number;
  editor: RequestState;
  /** 信任确认后重走的那条流程（HTTP send / socket connect）。 */
  resume: () => void;
}

let pending: PendingTrust | null = null;

const dialog = mountScriptTrustDialog({
  trust: () => {
    const waiting = pending;
    pending = null;
    if (waiting === null) return;
    if (getWorkspaceGeneration() !== waiting.generation || getRequest() !== waiting.editor) return;
    trustWorkspaceScripts();
    waiting.resume();
  },
});

/**
 * 带脚本的流程执行前的信任检查。
 * 可信（或本次载入已确认过）返回 true，调用方直接继续；不可信就弹框并返回 false，
 * 用户确认后 resume 会从流程起点重来。
 */
export function requireScriptTrust(editor: RequestState, resume: () => void): boolean {
  if (scriptsTrustedInWorkspace()) return true;
  pending = { generation: getWorkspaceGeneration(), editor, resume };
  dialog.open();
  return false;
}
