package ndapi

import (
	"testing"

	"github.com/dop251/goja"
)

// messageRuntime builds a runtime with nd.message injected.
func messageRuntime(t *testing.T, msg *Message, writable bool) (*goja.Runtime, *Run) {
	t.Helper()
	vm := goja.New()
	run := NewRun(vm, map[string]string{"token": "secret"}, nil)
	nd := vm.NewObject()
	_ = nd.Set("message", run.MessageGlobal(msg, writable))
	if err := vm.Set("nd", nd); err != nil {
		t.Fatalf("set nd: %v", err)
	}
	return vm, run
}

// 发送钩子的 payload 可写：写进去的值能读回，脚本自己读到的也是新值。
func TestMessagePayloadIsWritable(t *testing.T) {
	vm, run := messageRuntime(t, &Message{Payload: "ping", Event: "chat"}, true)
	out, err := vm.RunString(`nd.message.payload = "pong:" + nd.message.event; nd.message.payload`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got := out.String(); got != "pong:chat" {
		t.Fatalf("nd.message.payload = %q, want the rewritten text", got)
	}
	payload, changed := run.EditedPayload()
	if payload != "pong:chat" || !changed {
		t.Fatalf("EditedPayload = (%q, %v), want the rewritten text", payload, changed)
	}
}

// 没动过 payload 就不算改写：调用方据此保持原样。
func TestMessagePayloadUnchanged(t *testing.T) {
	vm, run := messageRuntime(t, &Message{Payload: "ping"}, true)
	if _, err := vm.RunString(`nd.message.payload`); err != nil {
		t.Fatalf("run script: %v", err)
	}
	payload, changed := run.EditedPayload()
	if payload != "ping" || changed {
		t.Fatalf("EditedPayload = (%q, %v), want the injected text and no change", payload, changed)
	}

	// 清空或删掉字段都不是改写：发出去的仍是页面写的那条消息。
	if _, err := vm.RunString(`nd.message.payload = undefined`); err != nil {
		t.Fatalf("run script: %v", err)
	}
	if _, changed := run.EditedPayload(); changed {
		t.Fatal("EditedPayload reported a change after the script cleared the field")
	}
}

// isBinary/event 只读：脚本改了也读不回去，改写 payload 不影响这两个字段。
func TestMessageIsBinaryAndEventAreReadOnly(t *testing.T) {
	vm, _ := messageRuntime(t, &Message{Payload: "AAEC", IsBinary: true, Event: "file"}, true)
	out, err := vm.RunString(`
		nd.message.isBinary = false;
		nd.message.event = "other";
		nd.message.payload = "rewritten";
		[nd.message.isBinary, nd.message.event, nd.message.payload].join("|")
	`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got, want := out.String(), "true|file|rewritten"; got != want {
		t.Fatalf("nd.message = %q, want %q", got, want)
	}
}

// 接收钩子看的是只读视图：payload 也改不动，消息本身保持原样。
func TestMessagePayloadIsReadOnlyForAReceiveRun(t *testing.T) {
	vm, run := messageRuntime(t, &Message{Payload: "incoming"}, false)
	out, err := vm.RunString(`nd.message.payload = "tampered"; nd.message.payload`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got := out.String(); got != "incoming" {
		t.Fatalf("nd.message.payload = %q, want the message as it arrived", got)
	}
	if payload, changed := run.EditedPayload(); payload != "incoming" || changed {
		t.Fatalf("EditedPayload = (%q, %v), want the message as it arrived", payload, changed)
	}
}

// 三个字段都可枚举，脚本能整体读出来（JSON.stringify 不会漏掉任何一项）。
func TestMessageFieldsAreEnumerable(t *testing.T) {
	vm, _ := messageRuntime(t, &Message{Payload: "p", IsBinary: true, Event: "e"}, false)
	out, err := vm.RunString(`JSON.stringify(nd.message)`)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got, want := out.String(), `{"payload":"p","isBinary":true,"event":"e"}`; got != want {
		t.Fatalf("JSON.stringify(nd.message) = %s, want %s", got, want)
	}
}
