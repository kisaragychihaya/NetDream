package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"netdream/backend/ndapi"
)

// 脚本在循环里写日志时，后端只保留前 scriptMaxLogs 条，并留一条说明。
func TestScriptLogRetentionLimit(t *testing.T) {
	run := newScriptRun(preScriptKind, &APIRequest{}, nil, nil)
	for i := 0; i < scriptMaxLogs+25; i++ {
		run.Log("log", "line", int64(i))
	}

	out := run.outcome(map[string]string{})
	if len(out.logs) != scriptMaxLogs+1 {
		t.Fatalf("kept %d console lines, want %d plus one note", len(out.logs), scriptMaxLogs)
	}
	note := out.logs[len(out.logs)-1]
	if note.Level != "warn" || !strings.Contains(note.Text, "25") {
		t.Fatalf("last console line = %#v, want a warning about the 25 dropped lines", note)
	}
	// 保留下来的就是前面那些，顺序没变。
	if out.logs[0].At != 0 || out.logs[scriptMaxLogs-1].At != int64(scriptMaxLogs-1) {
		t.Fatalf("kept console lines changed order: first %d, last %d",
			out.logs[0].At, out.logs[scriptMaxLogs-1].At)
	}
}

// nd.test 同样有保留上限，并且在同一条提示里说明。
func TestScriptTestRetentionLimit(t *testing.T) {
	run := newScriptRun(postScriptKind, &APIRequest{}, nil, nil)
	for i := 0; i < scriptMaxTests+7; i++ {
		run.Test("t", true, "")
	}

	out := run.outcome(map[string]string{})
	if len(out.tests) != scriptMaxTests {
		t.Fatalf("kept %d test results, want %d", len(out.tests), scriptMaxTests)
	}
	note := out.logs[len(out.logs)-1]
	if note.Level != "warn" || !strings.Contains(note.Text, "nd.test") || !strings.Contains(note.Text, "7") {
		t.Fatalf("note = %#v, want a warning about the 7 dropped test results", note)
	}
}

// 单条文本超长时按字节上限截断，且不切坏 UTF-8。
func TestScriptTextCutsAtRuneBoundary(t *testing.T) {
	// 三字节一个汉字，正好让上限落在一个字符中间。
	text := strings.Repeat("汉", scriptMaxTextBytes)
	got := scriptText(text)
	if len(got) > scriptMaxTextBytes {
		t.Fatalf("scriptText kept %d bytes, want at most %d", len(got), scriptMaxTextBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("scriptText cut a character in half")
	}
	if len(got) < scriptMaxTextBytes-2 {
		t.Fatalf("scriptText kept only %d bytes, want as much as fits", len(got))
	}
	// 没超限的文本原样返回。
	if short := scriptText("短"); short != "短" {
		t.Fatalf("scriptText(%q) = %q, want it unchanged", "短", short)
	}
}

// 一次长时间花在 Go 里的 pbkdf2 也要能被取消：vm.Interrupt 到不了那里，
// 靠 run 的 context 停下，并按既有的「已取消」消息报告。
func TestPreScriptCancelStopsPBKDF2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 10M 次迭代要跑好几秒：取消落在中间，才说明是它让这次运行停下来的。
	code := `nd.crypto.pbkdf2("password", "salt", 10000000, 32, "sha256")`
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	out, err := runPreScript(ctx, code, &APIRequest{Method: "GET"}, map[string]string{})
	if err == nil {
		t.Fatal("runPreScript returned no error after the run was cancelled")
	}
	if !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("runPreScript error = %v, want the cancelled message", err)
	}
	if elapsed := time.Since(started); elapsed > scriptTimeout {
		t.Fatalf("cancelling took %s: the Go-side loop did not watch its context", elapsed)
	}
	if out == nil {
		t.Fatal("runPreScript returned no outcome")
	}
}

// 超时与取消共用既有的两条消息，别让 Go 侧的提前退出露出第三种说法。
func TestScriptFailureMapsAbortErrors(t *testing.T) {
	if got := scriptFailure(preScriptKind, &ndapi.AbortError{Canceled: true}).Error(); got != "预执行脚本已取消" {
		t.Fatalf("cancelled failure = %q, want the cancelled message", got)
	}
	got := scriptFailure(postScriptKind, &ndapi.AbortError{}).Error()
	if !strings.Contains(got, "后执行脚本超时") || !strings.Contains(got, scriptTimeout.String()) {
		t.Fatalf("timed-out failure = %q, want the timeout message", got)
	}
	// 包一层也要认得出来：ctx.Err() 之类的错误会被再包一次。
	wrapped := errors.Join(&ndapi.AbortError{Canceled: true})
	if got := scriptFailure(preScriptKind, wrapped).Error(); got != "预执行脚本已取消" {
		t.Fatalf("wrapped cancelled failure = %q, want the cancelled message", got)
	}
}
