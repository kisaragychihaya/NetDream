package ndapi

import (
	"github.com/dop251/goja"
)

// Message is what a socket script sees in nd.message: one WebSocket, TCP or
// Socket.IO message, either the one about to be sent or the one just received.
type Message struct {
	// Payload is the message's content: text as written, or the base64 text of a
	// binary message.
	Payload string
	// IsBinary says Payload is base64 of bytes rather than text.
	IsBinary bool
	// Event is the Socket.IO event name; empty for a WebSocket or TCP message.
	Event string
}

// MessageGlobal is nd.message: payload is the message's content and can be
// rewritten when writable; isBinary and event only say what the message is, and
// are read-only either way (an assignment to them is ignored).
func (run *Run) MessageGlobal(msg *Message, writable bool) *goja.Object {
	vm := run.vm
	obj := vm.NewObject()
	run.message = obj
	run.messagePayload = msg.Payload

	_ = obj.DefineDataProperty("payload", vm.ToValue(msg.Payload), goja.ToFlag(writable), goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = obj.DefineDataProperty("isBinary", vm.ToValue(msg.IsBinary), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = obj.DefineDataProperty("event", vm.ToValue(msg.Event), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	return obj
}

// MessageObject is nd.message as injected, so the caller can read a send
// script's edits back.
func (run *Run) MessageObject() *goja.Object { return run.message }

// EditedPayload is the payload a send script left in nd.message.payload, and
// whether it differs from what the view was built with. A script that clears
// the field leaves the message as it came in.
func (run *Run) EditedPayload() (payload string, changed bool) {
	if run.message == nil {
		return "", false
	}
	run.vm.Try(func() {
		value := run.message.Get("payload")
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			return
		}
		payload = value.String()
		changed = payload != run.messagePayload
	})
	return payload, changed
}
