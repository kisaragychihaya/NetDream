package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 取消不能被 JS 吃掉：脚本用 try/catch 包住 pbkdf2，后面还有打日志与死循环，
// 取消后必须直接结束——catch、后面的日志、循环体一行都不该跑。
func TestPreScriptCancelIsNotCaughtByScript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	code := `
try {
  nd.crypto.pbkdf2("password", "salt", 10000000, 32, "sha256");
  nd.vars.set("returned", "yes");
} catch (error) {
  nd.vars.set("caught", "yes");
  console.log("caught", error && error.message);
} finally {
  nd.vars.set("finally", "yes");
  console.log("finally");
}
nd.vars.set("after", "yes");
console.log("after");
while (true) {}
`
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	out, err := runPreScript(ctx, code, &APIRequest{Method: "GET"}, map[string]string{})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("runPreScript returned no error after the run was cancelled")
	}
	if !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("runPreScript error = %v, want the cancelled message", err)
	}
	// 取消恰好发生在 pbkdf2 里：返回说明 Go 侧循环确实看了一眼 context。
	if elapsed > scriptTimeout {
		t.Fatalf("cancelling took %s: the Go-side loop did not watch its context", elapsed)
	}
	if out == nil {
		t.Fatal("runPreScript returned no outcome")
	}
	for _, name := range []string{"returned", "caught", "finally", "after"} {
		if _, ok := out.vars[name]; ok {
			t.Fatalf("variable %q was set: the script kept running after the abort (vars %v)", name, out.vars)
		}
	}
	// vars 出错时本来就是原始快照，光看它证明不了 finally 没跑；日志才是证据。
	for _, line := range out.logs {
		for _, banned := range []string{"caught", "finally", "after"} {
			if strings.Contains(line.Text, banned) {
				t.Fatalf("console line %q was kept: the script kept running after the abort", line.Text)
			}
		}
	}
}
