package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/egoist/mygo"

	"netdream/backend/ndapi"
)

// scriptInputRequested 请页面为一次 nd.input 弹出输入框：脚本阻塞在 Go 侧等回答，
// 页面弹模态框，用户确认或取消后经 ScriptService.ResolveInput 回来。
var scriptInputRequested = mygo.NewEvent[ScriptInputRequest]("script:inputRequested")

// scriptInputRetracted 收回一个还开着的输入框：等回答的运行已经结束（页面关闭、
// composer 终止），负载是那次输入请求的 id。回答与撤回撞车时页面两个都认得：
// 已回答的框不在了就忽略撤回，没回答的框被撤回后回答也递不到（幂等丢弃）。
var scriptInputRetracted = mygo.NewEvent[string]("script:inputRetracted")

// errScriptInputCanceled 是用户点「取消」时桥接返回的错误：ndapi 把它抛成普通的
// JS Error，脚本能 try/catch 后自己兜底。
var errScriptInputCanceled = errors.New("用户取消了输入")

// scriptInputAnswer 是页面对一次 nd.input 的回答：ok=false = 用户取消。
type scriptInputAnswer struct {
	value string
	ok    bool
}

// scriptInputPending 是等待回答的 nd.input 调用，按 id 登记。多个脚本（HTTP 后
// 执行脚本与连接接收脚本等）可以同时等输入，页面逐个弹框回答。
var scriptInputPending = struct {
	sync.Mutex
	byID map[string]chan scriptInputAnswer
}{byID: map[string]chan scriptInputAnswer{}}

// ScriptService 暴露脚本运行与页面之间的交互。Every exported method here is
// bound to the frontend, so helpers stay unexported.
type ScriptService struct{}

// ResolveInput 回答一次 nd.input：value 写进脚本要的变量，ok=false 表示用户取消。
// 不存在或已回答的 id 静默忽略（与 Stop/Disconnect 的幂等语义一致）。
func (ScriptService) ResolveInput(id string, value string, ok bool) {
	resolveScriptInput(id, value, ok)
}

// resolveScriptInput 把回答递给等它的那次 nd.input；迟到的回答（脚本已被取消）
// 丢弃。
func resolveScriptInput(id string, value string, ok bool) {
	scriptInputPending.Lock()
	ch, found := scriptInputPending.byID[id]
	if found {
		delete(scriptInputPending.byID, id)
	}
	scriptInputPending.Unlock()
	if found {
		ch <- scriptInputAnswer{value: value, ok: ok}
	}
}

// scriptInputBridge 是装配进每个脚本运行时的桥接实现；测试换成假桥接来模拟
// 用户回答。
var scriptInputBridge ndapi.InputBridge = awaitScriptInput

// awaitScriptInput 是 ndapi 的 InputBridge：把输入请求发到发起这次运行的页面，
// 阻塞等用户回答；运行被取消（页面关闭、composer 终止）时按中断路径返回。
func awaitScriptInput(ctx context.Context, name, prompt string) (string, error) {
	page := mygo.CallerPage(ctx)
	if page == nil {
		return "", errors.New("当前没有可交互的页面")
	}

	id, err := newScriptInputID()
	if err != nil {
		return "", err
	}
	ch := make(chan scriptInputAnswer, 1)
	scriptInputPending.Lock()
	scriptInputPending.byID[id] = ch
	scriptInputPending.Unlock()
	defer func() {
		scriptInputPending.Lock()
		delete(scriptInputPending.byID, id)
		scriptInputPending.Unlock()
	}()

	if err := scriptInputRequested.EmitPage(page, ScriptInputRequest{ID: id, Name: name, Prompt: prompt}); err != nil {
		return "", fmt.Errorf("发送输入请求失败: %w", err)
	}

	select {
	case answer := <-ch:
		if !answer.ok {
			return "", errScriptInputCanceled
		}
		return answer.value, nil
	case <-ctx.Done():
		// 等待期间运行的时间预算已暂停，走到这里只会是页面关闭或运行被终止：
		// 把还开着的输入框收回去（用户还没来得及回答的话），回答已到的竞态由
		// 页面按 id 对不上静默忽略。
		_ = scriptInputRetracted.EmitPage(page, id)
		return "", &ndapi.AbortError{Canceled: errors.Is(ctx.Err(), context.Canceled)}
	}
}

// newScriptInputID 给一次输入请求一个随机 id：回答凭它找到等着的脚本。
func newScriptInputID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成输入请求 id 失败: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
