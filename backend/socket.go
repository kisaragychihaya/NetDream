package main

import (
	"context"

	"github.com/egoist/mygo"

	"netdream/backend/protocol"
)

// SocketService opens WebSocket, TCP and Socket.IO connections the page talks
// over. The connections themselves live in backend/protocol; this shell maps
// the page's contract types onto it. Every exported method here is bound to the
// frontend, so helpers stay unexported.
type SocketService struct{}

// Connect opens the connection params names and stays until it closes or the
// page disconnects it, pushing what happens on stream as it happens.
func (SocketService) Connect(ctx context.Context, params SocketConnectParams, stream *mygo.Channel[SocketEvent]) error {
	return protocol.Connect(ctx, protocolConnectParams(params), channelSink{stream: stream})
}

// Send writes one message to the connection params.ID names.
func (SocketService) Send(params SocketSendParams) error {
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

// channelSink is the page's channel as the protocol package's sink.
type channelSink struct {
	stream *mygo.Channel[SocketEvent]
}

// Send pushes one event to the page.
func (s channelSink) Send(ev protocol.SocketEvent) error {
	return s.stream.Send(SocketEvent{
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
	})
}
