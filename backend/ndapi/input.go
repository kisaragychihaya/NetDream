package ndapi

import (
	"context"
	"errors"
	"strings"

	"github.com/dop251/goja"
)

// InputBridge 把一次 nd.input 交给用户：name 是要写入的变量名，prompt 是输入框
// 里的提示。用户确认后返回输入的值；用户取消返回普通错误（脚本可以 try/catch）；
// 运行本身被取消（页面关闭、composer 终止）返回 *AbortError，按运行的中断路径
// 报告。ctx 是这次运行的生命周期，桥接实现据此在等待中响应取消。
type InputBridge func(ctx context.Context, name, prompt string) (string, error)

// WaitClock 是这次运行的时间预算：等用户输入时暂停，输入完继续走，用户的思考
// 时间不算进脚本的超时。
type WaitClock interface {
	Pause()
	Resume()
	// TimedOut 报告预算是否已经耗尽：时钟到点也会取消运行，AbortError 据此把
	// 它报成超时而不是取消。
	TimedOut() bool
}

// BindInput 装上 nd.input 的桥接实现；不装时 nd.input 抛「环境不支持」。
func (run *Run) BindInput(bridge InputBridge) {
	run.input = bridge
}

// BindClock 装上这次运行的时间预算，nd.input 等待用户时经它暂停计时。
func (run *Run) BindClock(clock WaitClock) {
	run.clock = clock
}

// InputGlobal 是 nd.input(name, prompt)：阻塞到用户在弹出的输入框里确认，把值
// 写进变量 name（与 nd.vars.set 同一份数据）并把它作为返回值，脚本随后继续。
// 等待期间这次运行的时间预算暂停；页面关闭/运行被取消按中断路径结束；用户点
// 「取消」抛普通 Error（脚本能 try/catch 后自己兜底）。
func (run *Run) InputGlobal() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		name := ""
		if arg := call.Argument(0); arg != nil && !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			name = strings.TrimSpace(arg.String())
		}
		if name == "" {
			panic(run.newError("nd.input(name, prompt)：变量名不能为空"))
		}
		if run.input == nil {
			panic(run.newError("nd.input：当前运行环境不支持交互输入"))
		}
		prompt := ""
		if arg := call.Argument(1); arg != nil && !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			prompt = arg.String()
		}

		if run.clock != nil {
			run.clock.Pause()
			defer run.clock.Resume()
		}
		value, err := run.input(run.ctx, name, prompt)
		if err != nil {
			var aborted *AbortError
			if errors.As(err, &aborted) {
				// 运行被取消：走 vm 中断的同一套报告路径（见 crypto 的 pbkdf2）。
				panic(aborted)
			}
			// 用户取消或环境失败：普通 JS Error，脚本可以 try/catch。
			panic(run.newError(err.Error()))
		}
		_ = run.vars.Set(name, value)
		return run.vm.ToValue(value)
	}
}
