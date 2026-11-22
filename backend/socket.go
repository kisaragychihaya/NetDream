package main

import (
	"context"

	"github.com/egoist/mygo"

	"netdream/backend/protocol"
)

// SocketService opens WebSocket, TCP and Socket.IO connections the page talks
// over. The connections themselves live in backend/protocol; this shell maps
// the page's contract types onto it, and runs the connection's scripts around
// what it asks the protocol package to do. Every exported method here is bound
// to the frontend, so helpers stay unexported.
type SocketService struct{}

// Connect opens the connection params names and stays until it closes or the
// page disconnects it, pushing what happens on stream as it happens.
func (SocketService) Connect(ctx context.Context, params SocketConnectParams, stream *mygo.Channel[SocketEvent]) error {
	// 脚本随连接存续：注册早于拨号，Send 才能在拨号前就找到它。
	host := registerSocketScripts(ctx, params, socketEmitter(stream))
	defer unregisterSocketScripts(params.ID, host)

	// 预连接脚本在拨号之前跑：它出错就不拨号，页面按失败展示那条 script 事件。
	if err := host.preConnect(&params); err != nil {
		return err
	}
	// {{var}} 展开在预连接脚本之后，与 HTTP 请求同一条规则。
	expandSocketVars(&params, host.varsSnapshot(params.Vars))

	return protocol.Connect(ctx, protocolConnectParams(params), channelSink{stream: stream, script: host})
}

// Send writes one message to the connection params.ID names.
func (SocketService) Send(params SocketSendParams) error {
	// 发送脚本先跑：它出错这条消息就不发出去，payload 被改写则发改写后的内容。
	if err := lookupSocketScripts(params.ID).beforeSend(&params); err != nil {
		return err
	}
	return protocol.Send(protocol.SendParams{
		ID:       params.ID,
		Payload:  params.Payload,
		IsBinary: params.IsBinary,
		Event:    params.Event,
		Ack:      params.Ack,
	})
}

// Disconnect closes the connection id names; an id that is already gone is not
// an error.
func (SocketService) Disconnect(id string) {
	// 断开也结束正在跑的脚本：读循环停在脚本里时，只有取消能让它返回。
	if host := lookupSocketScripts(id); host != nil {
		host.cancel()
	}
	protocol.Disconnect(id)
}

// protocolConnectParams is the connection as the protocol package takes it.
func protocolConnectParams(params SocketConnectParams) protocol.ConnectParams {
	return protocol.ConnectParams{
		ID:       params.ID,
		Kind:     params.Kind,
		URL:      params.URL,
		Params:   protocolPairs(params.Params),
		Headers:  protocolPairs(params.Headers),
		Protocol: params.Protocol,
		Path:     params.Path,
		Version:  params.Version,
	}
}

// channelSink is the page's channel as the protocol package's sink: events go
// through as they are, and a message coming in also reaches the connection's
// receive script, right after the message itself.
type channelSink struct {
	stream *mygo.Channel[SocketEvent]
	script *socketScriptHost
}

// Send pushes one event to the page, then lets the receive script see it.
func (s channelSink) Send(ev protocol.SocketEvent) error {
	return deliverSocketEvent(socketEvent(ev), s.emit, s.script)
}

// emit pushes one event to the page; no channel means nobody is listening.
func (s channelSink) emit(ev SocketEvent) error {
	if s.stream == nil {
		return nil
	}
	return s.stream.Send(ev)
}

// deliverSocketEvent puts one event on the page, then runs the script of a
// message coming in. The message goes first, on this same goroutine, so neither
// a failing script nor a closed channel can swallow it, and the scripts see the
// messages in the order they arrived.
func deliverSocketEvent(ev SocketEvent, emit func(SocketEvent) error, script *socketScriptHost) error {
	if err := emit(ev); err != nil {
		return err
	}
	if ev.Type == "message" && ev.Direction == "receive" {
		script.received(ev)
	}
	return nil
}

// socketEmitter adapts the page's channel to the function a connection's
// scripts push their reports through.
func socketEmitter(stream *mygo.Channel[SocketEvent]) func(SocketEvent) error {
	if stream == nil {
		return nil
	}
	return stream.Send
}

// socketEvent is one protocol event as the page reads it.
func socketEvent(ev protocol.SocketEvent) SocketEvent {
	return SocketEvent{
		Type:      ev.Type,
		Direction: ev.Direction,
		Payload:   ev.Payload,
		IsBinary:  ev.IsBinary,
		At:        ev.At,
		Code:      ev.Code,
		Reason:    ev.Reason,
		Headers:   pairsFromProtocol(ev.Headers),
		Protocol:  ev.Protocol,
		Event:     ev.Event,
		IsAck:     ev.IsAck,
	}
}
