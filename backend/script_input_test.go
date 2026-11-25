package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"netdream/backend/ndapi"
)

// 暂停期间预算不走：暂停中越过预算不算超时，Resume 后剩下的预算继续走。
func TestScriptClockPauseHoldsBudget(t *testing.T) {
	expired := make(chan struct{})
	clock := newScriptClock(60*time.Millisecond, nil, func() { close(expired) })
	defer clock.stop()

	clock.Pause()
	select {
	case <-expired:
		t.Fatal("时钟在暂停期间到点了")
	case <-time.After(150 * time.Millisecond):
	}
	if clock.TimedOut() {
		t.Fatal("暂停期间 timedOut 成了 true")
	}

	clock.Resume()
	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		t.Fatal("Resume 之后剩下的预算没有到点")
	}
	if !clock.TimedOut() {
		t.Fatal("到点后 timedOut 不是 true")
	}
}

// 嵌套暂停：Resume 次数与 Pause 对上之前时钟一直停着。
func TestScriptClockPauseNests(t *testing.T) {
	expired := make(chan struct{})
	clock := newScriptClock(60*time.Millisecond, nil, func() { close(expired) })
	defer clock.stop()

	clock.Pause()
	clock.Pause()
	clock.Resume() // 还剩一层暂停
	select {
	case <-expired:
		t.Fatal("还有一层暂停时时钟就走了")
	case <-time.After(120 * time.Millisecond):
	}
	clock.Resume()
	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		t.Fatal("配对的 Resume 之后没有到点")
	}
}

// 子时钟的暂停顺着链传到 parent：composer 调起的条目脚本等输入时，整个运行的
// 预算也停。
func TestScriptClockPauseReachesParent(t *testing.T) {
	parentExpired := make(chan struct{})
	parent := newScriptClock(60*time.Millisecond, nil, func() { close(parentExpired) })
	defer parent.stop()
	// 子预算给得很大，到不到点只看 parent。
	child := newScriptClock(time.Hour, parent, nil)
	defer child.stop()

	child.Pause()
	select {
	case <-parentExpired:
		t.Fatal("子时钟的暂停没有传到 parent")
	case <-time.After(150 * time.Millisecond):
	}
	child.Resume()
	select {
	case <-parentExpired:
	case <-time.After(2 * time.Second):
		t.Fatal("child Resume 后 parent 没有到点")
	}
}

// nd.input 等到回答后写变量并返回；等待期间脚本的时间预算暂停——200ms 的输入
// 不该触发 50ms 的超时。
func TestPreScriptInputWaitsAndPausesTimeout(t *testing.T) {
	run := newScriptRun(preScriptKind, &APIRequest{Method: "GET"}, nil, map[string]string{})
	run.api.BindInput(func(ctx context.Context, name, prompt string) (string, error) {
		if name != "token" || prompt != "输入 token" {
			return "", fmt.Errorf("bridge got (%q, %q)", name, prompt)
		}
		time.Sleep(200 * time.Millisecond)
		return "from-user", nil
	})

	err := run.executeWithin(context.Background(), `
		const v = nd.input("token", "输入 token");
		if (v !== "from-user") throw new Error("return " + v);
		if (nd.vars.get("token") !== "from-user") throw new Error("var " + nd.vars.get("token"));
	`, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("executeWithin = %v，等待期间的暂停没有生效", err)
	}
	if got := run.exportedVars()["token"]; got != "from-user" {
		t.Fatalf("vars[token] = %q, want %q", got, "from-user")
	}
}

// 用户点「取消」抛的是普通 JS Error：脚本能 try/catch 后自己兜底，变量不写。
func TestScriptInputUserCancelIsCatchable(t *testing.T) {
	run := newScriptRun(preScriptKind, &APIRequest{Method: "GET"}, nil, map[string]string{})
	run.api.BindInput(func(ctx context.Context, name, prompt string) (string, error) {
		return "", errScriptInputCanceled
	})

	err := run.executeWithin(context.Background(), `
		let caught = "";
		try {
			nd.input("token", "输入 token");
		} catch (e) {
			caught = String(e.message || e);
		}
		nd.vars.set("caught", caught);
	`, scriptTimeout)
	if err != nil {
		t.Fatalf("executeWithin = %v, want the script to catch the cancel", err)
	}
	vars := run.exportedVars()
	if !strings.Contains(vars["caught"], "用户取消了输入") {
		t.Fatalf("caught = %q, want the cancel message", vars["caught"])
	}
	if _, ok := vars["token"]; ok {
		t.Fatal("取消不该写变量")
	}
}

// 等输入时运行被取消（页面关闭/终止）：按中断路径报「已取消」，不是普通脚本错误。
func TestScriptInputRunCancelAbortsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	run := newScriptRun(preScriptKind, &APIRequest{Method: "GET"}, nil, map[string]string{})
	run.api.BindInput(func(ctx context.Context, name, prompt string) (string, error) {
		<-ctx.Done()
		return "", &ndapi.AbortError{Canceled: true}
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := run.executeWithin(ctx, `nd.input("token", "输入 token")`, scriptTimeout)
	if err == nil || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("executeWithin = %v, want the cancelled message", err)
	}
}

// 没有页面的环境（纯后端、测试）：真桥接报错，脚本可以 try/catch。
func TestScriptInputWithoutPageIsCatchable(t *testing.T) {
	run := newScriptRun(preScriptKind, &APIRequest{Method: "GET"}, nil, map[string]string{})
	// 不换桥接：真桥接在没有页面的 ctx 上直接报错。
	err := run.executeWithin(context.Background(), `
		let caught = "";
		try { nd.input("token", "输入 token"); } catch (e) { caught = String(e.message || e); }
		nd.vars.set("caught", caught);
	`, scriptTimeout)
	if err != nil {
		t.Fatalf("executeWithin = %v, want the script to catch the missing page", err)
	}
	if got := run.exportedVars()["caught"]; !strings.Contains(got, "页面") {
		t.Fatalf("caught = %q, want the no-page message", got)
	}
}

// 变量名为空（或缺参）直接抛错。
func TestScriptInputRequiresName(t *testing.T) {
	run := newScriptRun(preScriptKind, &APIRequest{Method: "GET"}, nil, map[string]string{})
	err := run.executeWithin(context.Background(), `nd.input("  ")`, scriptTimeout)
	if err == nil || !strings.Contains(err.Error(), "变量名") {
		t.Fatalf("executeWithin = %v, want the empty-name error", err)
	}
}

// 回答按 id 递到等着的调用；同一个 id 再回答、或不存在的 id，都静默丢弃。
func TestResolveScriptInputDeliversAndDropsUnknown(t *testing.T) {
	ch := make(chan scriptInputAnswer, 1)
	scriptInputPending.Lock()
	scriptInputPending.byID["abc"] = ch
	scriptInputPending.Unlock()

	resolveScriptInput("abc", "v", true)
	select {
	case ans := <-ch:
		if ans.value != "v" || !ans.ok {
			t.Fatalf("answer = %#v, want v/ok", ans)
		}
	default:
		t.Fatal("回答没有递到")
	}

	// 迟到与未知的回答：不阻塞、不 panic。
	resolveScriptInput("abc", "late", true)
	resolveScriptInput("unknown", "x", false)

	scriptInputPending.Lock()
	_, found := scriptInputPending.byID["abc"]
	scriptInputPending.Unlock()
	if found {
		t.Fatal("回答之后登记表没清掉")
	}
}

// composer 脚本里 nd.input：等待期间整个运行的时间预算暂停——1s 的预算，输入
// 花了 1.5s 也不该超时。
func TestComposerInputPausesRunTimeout(t *testing.T) {
	bridge := scriptInputBridge
	scriptInputBridge = func(ctx context.Context, name, prompt string) (string, error) {
		time.Sleep(1500 * time.Millisecond)
		return "阿猫", nil
	}
	t.Cleanup(func() { scriptInputBridge = bridge })

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:      "input-" + t.Name(),
		Timeout: 1,
		Script: `
			var who = nd.input("who", "怎么称呼？");
			nd.test("拿到输入", function () {
				if (who !== "阿猫") throw new Error("who " + who);
				if (nd.vars.get("who") !== "阿猫") throw new Error("vars 没写上");
			});
		`,
	}
	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("reason = %q，等待没有暂停运行的预算（事件流 %s）",
			res.Reason, describeComposerEvents(events.snapshot()))
	}
	if res.Tests != 1 || res.Passed != 1 {
		t.Fatalf("result = %#v, want one passing test", res)
	}
	if done := composerDone(t, events.snapshot()); done.Vars["who"] != "阿猫" {
		t.Fatalf("done vars = %v, want who=阿猫", done.Vars)
	}
}

// 被调条目自己的脚本里 nd.input：等待顺着 parent 链暂停整个运行的预算。
func TestComposerNestedScriptInputPausesRunTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	bridge := scriptInputBridge
	scriptInputBridge = func(ctx context.Context, name, prompt string) (string, error) {
		time.Sleep(1500 * time.Millisecond)
		return "x", nil
	}
	t.Cleanup(func() { scriptInputBridge = bridge })

	events := &composerEvents{}
	params := ComposerRunParams{
		ID:      "nested-input-" + t.Name(),
		Timeout: 1,
		Requests: []SavedRequest{
			{ID: "a", Name: "甲", Method: "GET", URL: srv.URL + "/a",
				PreScript: `nd.vars.set("who", nd.input("who", "名字"));`},
		},
		Script: `
			var res = nd.composer.run("甲");
			nd.test("调用成功", function () {
				if (res.status !== 200) throw new Error("status " + res.status);
				if (nd.vars.get("who") !== "x") throw new Error("vars 没带上输入");
			});
		`,
	}
	res, err := runComposer(context.Background(), params, events.emit)
	if err != nil {
		t.Fatalf("runComposer: %v", err)
	}
	if res.Reason != "" {
		t.Fatalf("reason = %q，嵌套脚本的等待没有暂停运行的预算（事件流 %s）",
			res.Reason, describeComposerEvents(events.snapshot()))
	}
	if res.Tests != 1 || res.Passed != 1 {
		t.Fatalf("result = %#v, want one passing test", res)
	}
}
