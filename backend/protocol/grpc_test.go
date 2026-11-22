package protocol

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhump/protoreflect/desc/protoparse"
	"google.golang.org/grpc"
	"google.golang.org/grpc/interop"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// grpcLog is the sink these tests read events from; a channel tells a test when
// the call produced something new, so no assertion has to sleep.
type grpcLog struct {
	mu     sync.Mutex
	events []GrpcEvent
	wake   chan struct{}
}

func newGrpcLog() *grpcLog {
	return &grpcLog{wake: make(chan struct{})}
}

func (l *grpcLog) Send(ev GrpcEvent) error {
	l.mu.Lock()
	l.events = append(l.events, ev)
	close(l.wake)
	l.wake = make(chan struct{})
	l.mu.Unlock()
	return nil
}

func (l *grpcLog) snapshot() []GrpcEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]GrpcEvent(nil), l.events...)
}

// count counts the events of a type; with a direction it counts only the
// messages travelling that way.
func (l *grpcLog) count(kind, direction string) int {
	n := 0
	for _, ev := range l.snapshot() {
		if ev.Type == kind && (direction == "" || ev.Direction == direction) {
			n++
		}
	}
	return n
}

// last is the event the call ended with.
func (l *grpcLog) last(t *testing.T) GrpcEvent {
	t.Helper()
	events := l.snapshot()
	if len(events) == 0 {
		t.Fatal("the call produced no events")
	}
	return events[len(events)-1]
}

// waitFor blocks until want is happy with the events so far, or fails the test.
func (l *grpcLog) waitFor(t *testing.T, what string, want func([]GrpcEvent) bool) []GrpcEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		l.mu.Lock()
		events := append([]GrpcEvent(nil), l.events...)
		wake := l.wake
		l.mu.Unlock()
		if want(events) {
			return events
		}
		select {
		case <-wake:
		case <-deadline:
			t.Fatalf("the call never %s (events: %v)", what, events)
		}
	}
}

// assertClose asserts the call ended with one status code.
func assertClose(t *testing.T, sink *grpcLog, code, reason string) {
	t.Helper()
	ev := sink.last(t)
	if ev.Type != "close" {
		t.Fatalf("last event is %q, want close (events: %v)", ev.Type, sink.snapshot())
	}
	if ev.Code != code {
		t.Errorf("close code is %q, want %q (reason: %q)", ev.Code, code, ev.Reason)
	}
	if reason != "" && ev.Reason != reason {
		t.Errorf("close reason is %q, want %q", ev.Reason, reason)
	}
}

// startGrpcTestServer serves the interop test service with reflection on, on a
// plain-text loopback port.
func startGrpcTestServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening failed: %v", err)
	}
	srv := grpc.NewServer()
	testgrpc.RegisterTestServiceServer(srv, interop.NewTestServer())
	reflection.Register(srv)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// grpcConnectAsync runs GrpcConnect in its own goroutine, so a call that streams
// can be fed while it runs.
func grpcConnectAsync(ctx context.Context, params GrpcConnectParams, sink GrpcSink) <-chan error {
	done := make(chan error, 1)
	go func() { done <- GrpcConnect(ctx, params, sink) }()
	return done
}

// waitGrpcConnect returns what GrpcConnect answered within a few seconds.
func waitGrpcConnect(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("GrpcConnect did not return")
		return nil
	}
}

// waitGrpcRegistered blocks until the call is in the registry, which is what
// makes GrpcSend reach it: the entry exists before the dial starts.
func waitGrpcRegistered(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := lookupGrpcCall(id); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("call %s never registered", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// findMethod returns one method of a listing.
func findMethod(t *testing.T, services []GrpcServiceInfo, service, method string) GrpcMethodInfo {
	t.Helper()
	for _, svc := range services {
		if svc.Name != service {
			continue
		}
		for _, m := range svc.Methods {
			if m.Name == method {
				return m
			}
		}
	}
	t.Fatalf("service %s has no method %s (listing: %v)", service, method, services)
	return GrpcMethodInfo{}
}

// TestGrpcListReflection lists the interop test service through server
// reflection: the streaming flags and the input template are what the page needs
// to fill a call in.
func TestGrpcListReflection(t *testing.T) {
	addr := startGrpcTestServer(t)

	services, err := GrpcList(context.Background(), GrpcListParams{Target: addr, Plaintext: true})
	if err != nil {
		t.Fatalf("GrpcList failed: %v", err)
	}
	for _, svc := range services {
		if strings.HasPrefix(svc.Name, grpcReflectionPrefix) {
			t.Errorf("listing includes the reflection service %q", svc.Name)
		}
	}

	unary := findMethod(t, services, "grpc.testing.TestService", "UnaryCall")
	if unary.FullName != "grpc.testing.TestService.UnaryCall" {
		t.Errorf("unary full name is %q", unary.FullName)
	}
	if unary.ClientStreaming || unary.ServerStreaming {
		t.Errorf("UnaryCall is streaming (client %v, server %v)", unary.ClientStreaming, unary.ServerStreaming)
	}
	if unary.InputType != "grpc.testing.SimpleRequest" || unary.OutputType != "grpc.testing.SimpleResponse" {
		t.Errorf("UnaryCall types are %s -> %s", unary.InputType, unary.OutputType)
	}
	if !strings.Contains(unary.InputTemplate, "responseSize") || !strings.Contains(unary.InputTemplate, "payload") {
		t.Errorf("UnaryCall template is not the input message: %q", unary.InputTemplate)
	}

	shapes := map[string][2]bool{
		"StreamingOutputCall": {false, true},
		"StreamingInputCall":  {true, false},
		"FullDuplexCall":      {true, true},
	}
	for name, want := range shapes {
		m := findMethod(t, services, "grpc.testing.TestService", name)
		if m.ClientStreaming != want[0] || m.ServerStreaming != want[1] {
			t.Errorf("%s is (client %v, server %v), want (%v, %v)", name, m.ClientStreaming, m.ServerStreaming, want[0], want[1])
		}
		if m.InputTemplate == "" {
			t.Errorf("%s has no input template", name)
		}
	}
}

// TestGrpcUnaryCall runs one unary call: one request in, one message and a close
// back.
func TestGrpcUnaryCall(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/UnaryCall",
		Payload:        `{"payload":{"body":"aGk="},"responseSize":1}`,
	}
	if err := GrpcConnect(context.Background(), params, sink); err != nil {
		t.Fatalf("GrpcConnect failed: %v", err)
	}

	if got := sink.count("message", "send"); got != 1 {
		t.Errorf("%d send events, want 1 (events: %v)", got, sink.snapshot())
	}
	if got := sink.count("message", "receive"); got != 1 {
		t.Errorf("%d receive events, want 1 (events: %v)", got, sink.snapshot())
	}
	if got := sink.count("open", ""); got != 1 {
		t.Errorf("%d open events, want 1", got)
	}
	assertClose(t, sink, "OK", "")
	if _, err := lookupGrpcCall(params.ID); err == nil {
		t.Error("call is still registered after GrpcConnect returned")
	}
}

// TestGrpcServerStreaming reads a response stream to its end.
func TestGrpcServerStreaming(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService.StreamingOutputCall",
		Payload:        `{"responseParameters":[{"size":1},{"size":2},{"size":3}]}`,
	}
	if err := GrpcConnect(context.Background(), params, sink); err != nil {
		t.Fatalf("GrpcConnect failed: %v", err)
	}

	if got := sink.count("message", "receive"); got != 3 {
		t.Errorf("%d receive events, want 3 (events: %v)", got, sink.snapshot())
	}
	assertClose(t, sink, "OK", "")
}

// TestGrpcClientStreaming sends three messages and half-closes: the server
// answers once all of them arrived.
func TestGrpcClientStreaming(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/StreamingInputCall",
	}
	done := grpcConnectAsync(context.Background(), params, sink)
	waitGrpcRegistered(t, params.ID)

	for range 3 {
		if err := GrpcSend(params.ID, `{"payload":{"body":"aA=="}}`); err != nil {
			t.Fatalf("GrpcSend failed: %v", err)
		}
	}
	if err := GrpcCloseSend(params.ID); err != nil {
		t.Fatalf("GrpcCloseSend failed: %v", err)
	}
	// A second half-close is not an error.
	if err := GrpcCloseSend(params.ID); err != nil {
		t.Fatalf("the second GrpcCloseSend failed: %v", err)
	}
	if err := GrpcSend(params.ID, `{"payload":{"body":"aA=="}}`); err == nil {
		t.Error("GrpcSend after GrpcCloseSend succeeded, want an error")
	}

	if err := waitGrpcConnect(t, done); err != nil {
		t.Fatalf("GrpcConnect failed: %v", err)
	}
	if got := sink.count("message", "send"); got != 3 {
		t.Errorf("%d send events, want 3 (events: %v)", got, sink.snapshot())
	}
	events := sink.snapshot()
	if got := sink.count("message", "receive"); got != 1 {
		t.Fatalf("%d receive events, want 1 (events: %v)", got, events)
	}
	for _, ev := range events {
		if ev.Direction == "receive" && !strings.Contains(ev.Payload, "aggregatedPayloadSize") {
			t.Errorf("reply is %q, want the aggregated payload size", ev.Payload)
		}
	}
	assertClose(t, sink, "OK", "")
}

// TestGrpcBidiStreaming interleaves sends on a bidirectional call: each request
// carries one response parameter, so three sends mean three responses.
func TestGrpcBidiStreaming(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/FullDuplexCall",
	}
	done := grpcConnectAsync(context.Background(), params, sink)
	waitGrpcRegistered(t, params.ID)

	for i := range 3 {
		if err := GrpcSend(params.ID, `{"responseParameters":[{"size":1}]}`); err != nil {
			t.Fatalf("GrpcSend %d failed: %v", i, err)
		}
		// Wait for the reply before sending the next one: the events of a bidi
		// call may arrive in any order, but this keeps the counting readable.
		sink.waitFor(t, "answered a message", func(events []GrpcEvent) bool {
			n := 0
			for _, ev := range events {
				if ev.Type == "message" && ev.Direction == "receive" {
					n++
				}
			}
			return n > i
		})
	}
	if err := GrpcCloseSend(params.ID); err != nil {
		t.Fatalf("GrpcCloseSend failed: %v", err)
	}
	if err := waitGrpcConnect(t, done); err != nil {
		t.Fatalf("GrpcConnect failed: %v", err)
	}

	if got := sink.count("message", "receive"); got != 3 {
		t.Errorf("%d receive events, want 3 (events: %v)", got, sink.snapshot())
	}
	assertClose(t, sink, "OK", "")
}

// TestGrpcUnaryNotFound is the non-OK path: the status arrives as the close
// event, and GrpcConnect is not an error.
func TestGrpcUnaryNotFound(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/UnaryCall",
		Payload:        `{"responseStatus":{"code":5,"message":"missing"}}`,
	}
	if err := GrpcConnect(context.Background(), params, sink); err != nil {
		t.Fatalf("GrpcConnect returned %v for a non-OK status, want nil", err)
	}

	if got := sink.count("message", "receive"); got != 0 {
		t.Errorf("%d receive events, want none", got)
	}
	assertClose(t, sink, "NOT_FOUND", "missing")
}

// TestGrpcSendNeedsStreaming keeps the single-message methods honest: there is
// nowhere for a second message to go. The call is a server stream that answers
// slowly, so it is still open when the send arrives.
func TestGrpcSendNeedsStreaming(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/StreamingOutputCall",
		Payload:        `{"responseParameters":[{"size":1,"intervalUs":2000000}]}`,
	}
	done := grpcConnectAsync(context.Background(), params, sink)
	waitGrpcRegistered(t, params.ID)

	err := GrpcSend(params.ID, `{"responseParameters":[{"size":1}]}`)
	if err == nil {
		t.Error("GrpcSend on a server-streaming call succeeded, want an error")
	} else if !strings.Contains(err.Error(), "single message") {
		t.Errorf("GrpcSend answered %v, want an error about a single message", err)
	}

	GrpcDisconnect(params.ID)
	if err := waitGrpcConnect(t, done); err != nil && !strings.Contains(strings.ToLower(err.Error()), "canceled") {
		t.Fatalf("GrpcConnect returned %v after GrpcDisconnect, want nil or a cancellation", err)
	}
}

// TestGrpcDisconnectEndsConnect is the regression for a disconnect that has to
// reach a call in flight: GrpcConnect must return instead of waiting for a
// server that never ends the stream.
func TestGrpcDisconnectEndsConnect(t *testing.T) {
	addr := startGrpcTestServer(t)
	sink := newGrpcLog()

	params := GrpcConnectParams{
		GrpcListParams: GrpcListParams{Target: addr, Plaintext: true},
		ID:             t.Name(),
		Method:         "grpc.testing.TestService/FullDuplexCall",
	}
	done := grpcConnectAsync(context.Background(), params, sink)
	waitGrpcRegistered(t, params.ID)

	// One exchange proves the call is up: the server answers, then waits for more.
	if err := GrpcSend(params.ID, `{"responseParameters":[{"size":1}]}`); err != nil {
		t.Fatalf("GrpcSend failed: %v", err)
	}
	sink.waitFor(t, "answered the first message", func(events []GrpcEvent) bool {
		for _, ev := range events {
			if ev.Type == "message" && ev.Direction == "receive" {
				return true
			}
		}
		return false
	})

	GrpcDisconnect(params.ID)
	GrpcDisconnect(params.ID) // a call already gone is not an error

	if err := waitGrpcConnect(t, done); err != nil && !strings.Contains(strings.ToLower(err.Error()), "canceled") {
		t.Fatalf("GrpcConnect returned %v after GrpcDisconnect, want nil or a cancellation", err)
	}
	if _, err := lookupGrpcCall(params.ID); err == nil {
		t.Error("call is still registered after GrpcConnect returned")
	}
}

// grpcEchoProto is the smallest service that needs no reflection to describe.
const grpcEchoProto = `syntax = "proto3";

package echo;

message EchoRequest {
  string message = 1;
}

service EchoService {
  rpc Echo(EchoRequest) returns (EchoRequest);
}
`

// startGrpcEchoServer serves echo.proto from a hand-written service descriptor,
// with reflection left off: the file the page names is the only description of
// it there is.
func startGrpcEchoServer(t *testing.T) (addr, protoPath string) {
	t.Helper()
	dir := t.TempDir()
	protoPath = filepath.Join(dir, "echo.proto")
	if err := os.WriteFile(protoPath, []byte(grpcEchoProto), 0o600); err != nil {
		t.Fatalf("writing echo.proto failed: %v", err)
	}

	files, err := (&protoparse.Parser{ImportPaths: []string{dir}}).ParseFiles("echo.proto")
	if err != nil {
		t.Fatalf("parsing echo.proto failed: %v", err)
	}
	file, err := protodesc.NewFile(files[0].AsFileDescriptorProto(), protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("building the echo descriptor failed: %v", err)
	}
	requestType := file.Messages().ByName("EchoRequest")
	if requestType == nil {
		t.Fatal("echo.proto declares no EchoRequest message")
	}

	// Echo answers with what it was given.
	echo := func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		in := dynamicpb.NewMessage(requestType)
		if err := dec(in); err != nil {
			return nil, err
		}
		return in, nil
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening failed: %v", err)
	}
	srv := grpc.NewServer()
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "echo.EchoService",
		HandlerType: (*any)(nil),
		Methods:     []grpc.MethodDesc{{MethodName: "Echo", Handler: echo}},
		Metadata:    "echo.proto",
	}, nil)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), protoPath
}

// TestGrpcProtoFiles lists and calls a service that has no reflection, using the
// .proto file the page hands over instead.
func TestGrpcProtoFiles(t *testing.T) {
	addr, protoPath := startGrpcEchoServer(t)
	params := GrpcListParams{Target: addr, Plaintext: true, ProtoFiles: []string{protoPath}}

	services, err := GrpcList(context.Background(), params)
	if err != nil {
		t.Fatalf("GrpcList failed: %v", err)
	}
	echo := findMethod(t, services, "echo.EchoService", "Echo")
	if echo.FullName != "echo.EchoService.Echo" {
		t.Errorf("full name is %q", echo.FullName)
	}
	if echo.InputType != "echo.EchoRequest" || echo.OutputType != "echo.EchoRequest" {
		t.Errorf("types are %s -> %s", echo.InputType, echo.OutputType)
	}
	if !strings.Contains(echo.InputTemplate, "message") {
		t.Errorf("template is %q, want the message field", echo.InputTemplate)
	}

	sink := newGrpcLog()
	call := GrpcConnectParams{
		GrpcListParams: params,
		ID:             t.Name(),
		Method:         "echo.EchoService/Echo",
		Payload:        `{"message":"hi"}`,
	}
	if err := GrpcConnect(context.Background(), call, sink); err != nil {
		t.Fatalf("GrpcConnect failed: %v", err)
	}
	events := sink.snapshot()
	if got := sink.count("message", "receive"); got != 1 {
		t.Fatalf("%d receive events, want 1 (events: %v)", got, events)
	}
	for _, ev := range events {
		if ev.Direction == "receive" && !strings.Contains(ev.Payload, `"hi"`) {
			t.Errorf("reply is %q, want the echoed message", ev.Payload)
		}
	}
	assertClose(t, sink, "OK", "")
}

// TestGrpcProtoFilesListNeedsNoServer lists from a .proto file alone: the
// target may be unreachable, the file is all the listing reads.
func TestGrpcProtoFilesListNeedsNoServer(t *testing.T) {
	_, protoPath := startGrpcEchoServer(t)
	services, err := GrpcList(context.Background(), GrpcListParams{
		Target:     "127.0.0.1:1", // nothing listens here
		Plaintext:  true,
		ProtoFiles: []string{protoPath},
		Timeout:    5,
	})
	if err != nil {
		t.Fatalf("GrpcList from proto files with an unreachable target failed: %v", err)
	}
	if echo := findMethod(t, services, "echo.EchoService", "Echo"); echo.FullName != "echo.EchoService.Echo" {
		t.Errorf("full name is %q", echo.FullName)
	}
}
