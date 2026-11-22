package main

import (
	"context"

	"github.com/egoist/mygo"

	"netdream/backend/protocol"
)

// GrpcService lists and calls the gRPC methods of a target. The connections
// themselves live in backend/protocol; this shell maps the page's contract types
// onto it. Every exported method here is bound to the frontend, so helpers stay
// unexported.
type GrpcService struct{}

// List asks params.Target what it serves: through server reflection, or through
// the local .proto files params.ProtoFiles names.
func (GrpcService) List(ctx context.Context, params GrpcListParams) ([]GrpcServiceInfo, error) {
	services, err := protocol.GrpcList(ctx, protocolGrpcListParams(params))
	if err != nil {
		return nil, err
	}
	return grpcServiceInfos(services), nil
}

// Connect opens the call params.ID names and stays until it completes or the
// page disconnects it, pushing what happens on stream as it happens.
func (GrpcService) Connect(ctx context.Context, params GrpcConnectParams, stream *mygo.Channel[GrpcEvent]) error {
	return protocol.GrpcConnect(ctx, protocolGrpcConnectParams(params), grpcChannelSink{stream: stream})
}

// Send writes one request message to the streaming call id names.
func (GrpcService) Send(id string, payload string) error {
	return protocol.GrpcSend(id, payload)
}

// CloseSend half-closes the request stream of the call id names, so the server
// knows no more messages follow; the call keeps receiving until it completes.
func (GrpcService) CloseSend(id string) error {
	return protocol.GrpcCloseSend(id)
}

// Disconnect ends the call id names; an id that is already gone is not an error.
func (GrpcService) Disconnect(id string) {
	protocol.GrpcDisconnect(id)
}

// protocolGrpcListParams is the target as the protocol package takes it.
func protocolGrpcListParams(params GrpcListParams) protocol.GrpcListParams {
	return protocol.GrpcListParams{
		Target:        params.Target,
		Plaintext:     params.Plaintext,
		TLSSkipVerify: params.TLSSkipVerify,
		ServerName:    params.ServerName,
		Metadata:      protocolPairs(params.Metadata),
		ProtoFiles:    params.ProtoFiles,
		ImportPaths:   params.ImportPaths,
		Timeout:       params.Timeout,
	}
}

// protocolGrpcConnectParams is the call as the protocol package takes it; the
// page's flat connect params fold back into the embedded target params.
func protocolGrpcConnectParams(params GrpcConnectParams) protocol.GrpcConnectParams {
	return protocol.GrpcConnectParams{
		GrpcListParams: protocolGrpcListParams(GrpcListParams{
			Target:        params.Target,
			Plaintext:     params.Plaintext,
			TLSSkipVerify: params.TLSSkipVerify,
			ServerName:    params.ServerName,
			Metadata:      params.Metadata,
			ProtoFiles:    params.ProtoFiles,
			ImportPaths:   params.ImportPaths,
			Timeout:       params.Timeout,
		}),
		ID:      params.ID,
		Method:  params.Method,
		Payload: params.Payload,
	}
}

// grpcServiceInfos is the services as the page reads them; a target that serves
// nothing reads as an empty list, never null.
func grpcServiceInfos(services []protocol.GrpcServiceInfo) []GrpcServiceInfo {
	out := make([]GrpcServiceInfo, 0, len(services))
	for _, svc := range services {
		methods := make([]GrpcMethodInfo, 0, len(svc.Methods))
		for _, m := range svc.Methods {
			methods = append(methods, GrpcMethodInfo{
				Service:         m.Service,
				Name:            m.Name,
				FullName:        m.FullName,
				ClientStreaming: m.ClientStreaming,
				ServerStreaming: m.ServerStreaming,
				InputType:       m.InputType,
				OutputType:      m.OutputType,
				InputTemplate:   m.InputTemplate,
			})
		}
		out = append(out, GrpcServiceInfo{Name: svc.Name, Methods: methods})
	}
	return out
}

// grpcChannelSink is the page's channel as the protocol package's sink.
type grpcChannelSink struct {
	stream *mygo.Channel[GrpcEvent]
}

// Send pushes one event to the page.
func (s grpcChannelSink) Send(ev protocol.GrpcEvent) error {
	return s.stream.Send(GrpcEvent{
		Type:      ev.Type,
		Direction: ev.Direction,
		Payload:   ev.Payload,
		At:        ev.At,
		Code:      ev.Code,
		Reason:    ev.Reason,
		Headers:   pairsFromProtocol(ev.Headers),
		Trailers:  pairsFromProtocol(ev.Trailers),
	})
}
