package ndapi

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// responseRuntime builds a runtime with nd.response injected, body being the
// response body a post script would see.
func responseRuntime(t *testing.T, body string) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	run := NewRun(vm, nil, nil)
	nd := vm.NewObject()
	_ = nd.Set("response", run.ResponseGlobal(&Response{Status: 200, StatusText: "OK", Body: body}))
	if err := vm.Set("nd", nd); err != nil {
		t.Fatalf("set nd: %v", err)
	}
	return vm
}

// json() 只在 JS 里解析一次，给出来的是真正的 JS 值：数组带方法、对象可直接读字段。
func TestResponseJSONReturnsJSValues(t *testing.T) {
	vm := responseRuntime(t, `{"list":[1,2,3],"nested":{"ok":true},"text":"中文"}`)
	out, err := vm.RunString(`[
		Array.isArray(nd.response.json().list),
		nd.response.json().list.map(function (n) { return n * 2 }).join(","),
		nd.response.json().nested.ok,
		nd.response.json().text
	].join("|")`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got, want := out.String(), "true|2,4,6|true|中文"; got != want {
		t.Fatalf("json() result = %q, want %q", got, want)
	}
}

// 解析失败的消息仍带 nd.response.json(): 前缀，脚本也能像以前那样 catch。
func TestResponseJSONReportsParseError(t *testing.T) {
	vm := responseRuntime(t, `{oops}`)
	out, err := vm.RunString(`
		var message = "";
		try {
			nd.response.json();
		} catch (error) {
			message = String(error);
		}
		message
	`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if !strings.Contains(out.String(), "nd.response.json(): ") {
		t.Fatalf("error %q does not carry the nd.response.json() prefix", out.String())
	}
}

// 标量与非对象的 JSON 也照常返回（解析交给 JS，Go 侧不再先解一遍）。
func TestResponseJSONScalars(t *testing.T) {
	cases := []struct{ body, want string }{
		{`123`, "123"},
		{`"text"`, "text"},
		{`null`, "null"},
		{`[1,2]`, "2"},
	}
	for _, c := range cases {
		vm := responseRuntime(t, c.body)
		out, err := vm.RunString(`String(Array.isArray(nd.response.json()) ? nd.response.json()[1] : nd.response.json())`)
		if err != nil {
			t.Fatalf("body %s: run script: %v", c.body, err)
		}
		if out.String() != c.want {
			t.Errorf("body %s: json() = %q, want %q", c.body, out.String(), c.want)
		}
	}
}
