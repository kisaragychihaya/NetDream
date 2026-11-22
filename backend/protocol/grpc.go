package protocol

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fullstorydev/grpcurl"
	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The reflection service is how this package reads descriptors, not something
// the page wants to call, so it never appears in a listing.
const grpcReflectionPrefix = "grpc.reflection."

// grpcEmptyPayload stands in for a payload the page left blank: an empty call
// still needs a message, and every message accepts the empty JSON object.
const grpcEmptyPayload = "{}"

// GrpcList reports what the target serves: every service it exposes, with the
// methods of each and a JSON template of their input. Descriptors come from the
// page's own .proto files when it names any, and from server reflection
// otherwise.
func GrpcList(ctx context.Context, params GrpcListParams) ([]GrpcServiceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, grpcTimeout(params.Timeout))
	defer cancel()

	// Local .proto files describe the methods all by themselves: listing them
	// must not need the target at all.
	if files := grpcProtoFiles(params.ProtoFiles); len(files) > 0 {
		source, err := grpcurl.DescriptorSourceFromProtoFiles(grpcImportPaths(params.ImportPaths, files), files...)
		if err != nil {
			return nil, fmt.Errorf("reading the proto files failed: %w", err)
		}
		return grpcServiceInfos(source)
	}

	cc, err := dialGRPC(ctx, params)
	if err != nil {
		return nil, err
	}
	defer cc.Close()

	source, err := grpcDescriptorSource(ctx, params, cc)
	if err != nil {
		return nil, err
	}
	return grpcServiceInfos(source)
}

// GrpcConnect runs the call params names and stays until it ends or the caller
// disconnects it, pushing what happens on sink as it happens. Like Connect, it
// keeps the call in a registry so that GrpcSend, GrpcCloseSend and
// GrpcDisconnect reach it by id, and the entry exists before the dial so that a
// disconnect can end a call that is still being opened.
func GrpcConnect(ctx context.Context, params GrpcConnectParams, sink GrpcSink) error {
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return fmt.Errorf("a gRPC call needs an id")
	}
	service, methodName := grpcMethodParts(params.Method)
	if service == "" || methodName == "" {
		return fmt.Errorf("invalid method %q: name it like \"pkg.Service/Method\"", params.Method)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	call := &grpcCall{
		id:     id,
		method: service + "/" + methodName,
		sink:   sink,
		cancel: cancel,
		ctx:    ctx,
		inbox:  newGrpcInbox(),
		ready:  make(chan struct{}),
	}
	if err := registerGrpcCall(call); err != nil {
		return err
	}
	defer func() {
		unregisterGrpcCall(call)
		call.stop()
	}()
	// The callbacks below run inside grpcurl's invocation, which ends once the
	// connection closes: this is what makes a disconnect reach it.
	stopOnCancel := context.AfterFunc(ctx, call.stop)
	defer stopOnCancel()

	cc, err := dialGRPC(ctx, params.GrpcListParams)
	if err != nil {
		return call.failErr(ctx, err)
	}
	if !call.attachConn(cc) {
		// The caller disconnected while the dial was in flight: stop() ran
		// before a connection existed, so close the one the dial produced.
		_ = cc.Close()
		return nil
	}

	source, err := grpcDescriptorSource(ctx, params.GrpcListParams, cc)
	if err != nil {
		return call.failErr(ctx, err)
	}
	dsc, err := source.FindSymbol(service)
	if err != nil {
		return call.failErr(ctx, fmt.Errorf("resolving service %q failed: %w", service, err))
	}
	sd, ok := dsc.(*desc.ServiceDescriptor)
	if !ok {
		return call.failErr(ctx, fmt.Errorf("%q is not a service", service))
	}
	md := sd.FindMethodByName(methodName)
	if md == nil {
		return call.failErr(ctx, fmt.Errorf("service %q has no method %q: name it like \"%s/YourMethod\"", service, methodName, service))
	}

	// The method's shape is what GrpcSend and GrpcCloseSend wait for: a send
	// that arrived while the call was still being opened is only now decidable.
	call.setMethodShape(md)

	resolver := grpcurl.AnyResolverFromDescriptorSource(source)
	handler := &grpcHandler{call: call, formatter: grpcurl.NewJSONFormatter(true, resolver)}

	var supplier grpcurl.RequestSupplier
	if md.IsClientStreaming() {
		// A streaming call takes as many messages as the page sends, so its
		// payload field is not used at all.
		supplier = call.streamSupplier(resolver)
	} else {
		supplier = call.singleSupplier(params.Payload, resolver)
	}

	// A unary call is bounded like a listing; a stream runs until the page ends
	// it or the server does.
	callCtx := ctx
	if !md.IsClientStreaming() && !md.IsServerStreaming() {
		var cancelCall context.CancelFunc
		callCtx, cancelCall = context.WithTimeout(ctx, grpcTimeout(params.Timeout))
		defer cancelCall()
	}

	err = grpcurl.InvokeRPC(callCtx, source, cc, call.method, grpcHeaders(params.Metadata), handler, supplier)
	// A sink error means the page's channel is gone: that is what ended the
	// call, so it is what the caller hears about.
	if sinkErr := call.sinkError(); sinkErr != nil {
		return sinkErr
	}
	// A non-OK gRPC status already travelled as the close event; anything left
	// here is a transport or parse failure the page has not heard of yet.
	return call.failErr(ctx, err)
}

// GrpcSend queues one message for a client-streaming or bidirectional call.
// A unary or server-streaming method takes its single message as the call's
// payload, so there is nothing for this to do there.
func GrpcSend(id, payload string) error {
	call, err := lookupGrpcCall(id)
	if err != nil {
		return err
	}
	return call.send(payload)
}

// GrpcCloseSend ends the sending side of a client-streaming or bidirectional
// call: the server sees the end of the request stream and answers with its
// status. Calling it again, or on a method that never had a sending side, is
// not an error.
func GrpcCloseSend(id string) error {
	call, err := lookupGrpcCall(id)
	if err != nil {
		return err
	}
	call.closeSend()
	return nil
}

// GrpcDisconnect ends the call id names; an id that is already gone is not an
// error.
func GrpcDisconnect(id string) {
	call, err := lookupGrpcCall(id)
	if err != nil {
		return
	}
	call.stop()
}

// grpcTimeout is how long a listing or a unary call may take; a stream is not
// bounded at all.
func grpcTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

// grpcMethodParts splits a method name the way grpcurl does: "pkg.Service/Method",
// or "pkg.Service.Method" when the page wrote it with a dot.
func grpcMethodParts(name string) (service, method string) {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
	}
	return "", ""
}

// grpcDescriptorSource builds the descriptor source the page asked for: the
// local .proto files it named, or server reflection.
func grpcDescriptorSource(ctx context.Context, params GrpcListParams, cc *grpc.ClientConn) (grpcurl.DescriptorSource, error) {
	if files := grpcProtoFiles(params.ProtoFiles); len(files) > 0 {
		source, err := grpcurl.DescriptorSourceFromProtoFiles(grpcImportPaths(params.ImportPaths, files), files...)
		if err != nil {
			return nil, fmt.Errorf("reading the proto files failed: %w", err)
		}
		return source, nil
	}

	// The reflection client keeps its own context for every request it makes, so
	// the page's metadata rides along there rather than on the call itself.
	refCtx := grpcMetadataContext(ctx, params.Metadata)
	refClient := grpcreflect.NewClientAuto(refCtx, cc)
	return grpcurl.DescriptorSourceFromServer(refCtx, refClient), nil
}

// grpcProtoFiles drops the empty entries of the page's file list.
func grpcProtoFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// grpcImportPaths adds the directory of every proto file to the import paths:
// that is where a page's imports of its sibling files resolve.
func grpcImportPaths(paths, files []string) []string {
	out := make([]string, 0, len(paths)+len(files))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, f := range files {
		if dir := filepath.Dir(f); !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}

// grpcServiceInfos lists the services of source with their methods.
func grpcServiceInfos(source grpcurl.DescriptorSource) ([]GrpcServiceInfo, error) {
	names, err := grpcurl.ListServices(source)
	if err != nil {
		return nil, fmt.Errorf("listing the services failed: %w", err)
	}

	resolver := grpcurl.AnyResolverFromDescriptorSource(source)
	formatter := grpcurl.NewJSONFormatter(true, resolver)

	out := make([]GrpcServiceInfo, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, grpcReflectionPrefix) {
			continue
		}
		dsc, err := source.FindSymbol(name)
		if err != nil {
			return nil, fmt.Errorf("resolving service %q failed: %w", name, err)
		}
		sd, ok := dsc.(*desc.ServiceDescriptor)
		if !ok {
			continue
		}
		methods, err := grpcurl.ListMethods(source, name)
		if err != nil {
			return nil, fmt.Errorf("listing the methods of %s failed: %w", name, err)
		}

		info := GrpcServiceInfo{Name: name}
		for _, full := range methods {
			// ListMethods answers fully-qualified names; the descriptor is
			// already here, so the last segment is enough to find the method.
			md := sd.FindMethodByName(full[strings.LastIndexByte(full, '.')+1:])
			if md == nil {
				continue
			}
			template, err := formatter(grpcurl.MakeTemplate(md.GetInputType()))
			if err != nil {
				return nil, fmt.Errorf("building the input template of %s failed: %w", md.GetFullyQualifiedName(), err)
			}
			info.Methods = append(info.Methods, GrpcMethodInfo{
				Service:         name,
				Name:            md.GetName(),
				FullName:        md.GetFullyQualifiedName(),
				ClientStreaming: md.IsClientStreaming(),
				ServerStreaming: md.IsServerStreaming(),
				InputType:       md.GetInputType().GetFullyQualifiedName(),
				OutputType:      md.GetOutputType().GetFullyQualifiedName(),
				InputTemplate:   template,
			})
		}
		out = append(out, info)
	}
	return out, nil
}

// dialGRPC opens the connection a call or a listing uses: plaintext h2c unless
// the page asked for TLS.
func dialGRPC(ctx context.Context, p GrpcListParams) (*grpc.ClientConn, error) {
	target := strings.TrimSpace(p.Target)
	if target == "" {
		return nil, fmt.Errorf("a gRPC call needs a target, like 127.0.0.1:50051")
	}

	// The proxy lookup below keys on the URL scheme the same address would have.
	scheme := "https"
	var creds credentials.TransportCredentials
	if p.Plaintext {
		scheme = "http"
		creds = insecure.NewCredentials()
	} else {
		conf, err := grpcurl.ClientTLSConfig(p.TLSSkipVerify, "", "", "")
		if err != nil {
			return nil, fmt.Errorf("building the TLS config failed: %w", err)
		}
		if name := strings.TrimSpace(p.ServerName); name != "" {
			conf.ServerName = name
		}
		creds = credentials.NewTLS(conf)
	}

	// The dial has its own bound: a target that never answers must not hold the
	// page longer than any other request.
	dialCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		return grpcDialContext(ctx, scheme, addr)
	}
	cc, err := grpcurl.BlockingDial(dialCtx, "tcp", target, creds, grpc.WithContextDialer(dial))
	if err != nil {
		return nil, fmt.Errorf("connecting to %s failed: %w", target, err)
	}
	return cc, nil
}

// grpcDialContext 拨到 addr：命中 http(s) 代理时先 CONNECT 开隧道，与 h2c 同一套
// 规则；socks 代理没有 CONNECT 语义，保持直连。
func grpcDialContext(ctx context.Context, scheme, addr string) (net.Conn, error) {
	if proxy := proxyForSchemeHost(scheme, addr); proxy != nil &&
		(proxy.Scheme == "http" || proxy.Scheme == "https") {
		return dialViaProxyContext(ctx, "tcp", proxy, addr)
	}
	return (&net.Dialer{Timeout: defaultTimeout}).DialContext(ctx, "tcp", addr)
}

// grpcHeaders turns the page's metadata into the "key: value" list grpcurl
// takes, skipping entries that name no key.
func grpcHeaders(pairs []KeyValue) []string {
	out := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		key := strings.TrimSpace(kv.Key)
		if key == "" {
			continue
		}
		out = append(out, key+": "+kv.Value)
	}
	return out
}

// grpcMetadataContext puts the page's metadata on ctx, for the reflection
// requests the descriptor source makes: those are the ones the page's metadata
// has to reach as well.
func grpcMetadataContext(ctx context.Context, pairs []KeyValue) context.Context {
	pairsOn := make([]string, 0, 2*len(pairs))
	for _, kv := range pairs {
		if key := strings.TrimSpace(kv.Key); key != "" {
			pairsOn = append(pairsOn, key, kv.Value)
		}
	}
	if len(pairsOn) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, pairsOn...)
}

// flattenGRPCMetadata converts the metadata of one call into the page's
// key/value list: gRPC keys are case-insensitive (the library lowercases them)
// and one key may carry several values, each of which gets its own entry.
func flattenGRPCMetadata(md metadata.MD) []KeyValue {
	if len(md) == 0 {
		return nil
	}
	keys := make([]string, 0, len(md))
	for key := range md {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]KeyValue, 0, len(md))
	for _, key := range keys {
		for _, value := range md[key] {
			out = append(out, KeyValue{Key: key, Value: value})
		}
	}
	return out
}

// grpcCall is one live call: the registry finds it by id, the inbox feeds the
// request supplier of a streaming call, and the connection is what stop closes.
type grpcCall struct {
	id     string
	method string // "pkg.Service/Method", for error messages
	sink   GrpcSink
	cancel context.CancelFunc
	ctx    context.Context // what stop cancels; a pending send waits on it
	ready  chan struct{}   // closed once the method's shape is known, or the call ends
	inbox  *grpcInbox

	mu           sync.Mutex
	readyOnce    sync.Once
	cc           *grpc.ClientConn
	shut         bool
	clientStream bool

	sinkMu  sync.Mutex
	sinkErr error
}

// attachConn hands stop the connection the dial produced, so a disconnect
// closes what the call is using. It reports false when stop already ran, in
// which case the caller closes the connection itself.
func (c *grpcCall) attachConn(cc *grpc.ClientConn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shut {
		return false
	}
	c.cc = cc
	return true
}

// setMethodShape records whether the method takes a stream of messages, which
// is what lets a queued send through to the supplier.
func (c *grpcCall) setMethodShape(md *desc.MethodDescriptor) {
	c.mu.Lock()
	c.clientStream = md.IsClientStreaming()
	c.mu.Unlock()
	c.markReady()
}

// markReady releases the sends that are waiting to learn the method's shape. It
// runs on every way out of GrpcConnect, so a send never waits past the call.
func (c *grpcCall) markReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

// stop ends the call: closing the connection and cancelling the context is what
// makes the invocation return, and closing the inbox wakes a supplier that is
// waiting for the page's next message. It is safe to call repeatedly.
func (c *grpcCall) stop() {
	c.mu.Lock()
	c.shut = true
	cc := c.cc
	c.mu.Unlock()

	c.cancel()
	c.inbox.close()
	if cc != nil {
		_ = cc.Close()
	}
	c.markReady()
}

// isShut reports whether the caller, or GrpcConnect on its way out, closed the
// call.
func (c *grpcCall) isShut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shut
}

// emit pushes one event, and stops the call when the sink refuses it: a sink
// error means the page's channel is gone, so nothing more can be sent to it.
func (c *grpcCall) emit(ev GrpcEvent) {
	if ev.At == 0 {
		ev.At = time.Now().UnixMilli()
	}
	if err := c.sink.Send(ev); err != nil {
		c.sinkMu.Lock()
		if c.sinkErr == nil {
			c.sinkErr = err
		}
		c.sinkMu.Unlock()
		c.cancel()
	}
}

// sinkError is what the sink answered, if it ended the call.
func (c *grpcCall) sinkError() error {
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	return c.sinkErr
}

// failErr reports err to the page and returns what GrpcConnect should hand back:
// a cancel is the caller's own doing and travels as itself, and a stopped call
// already told the page why it ended.
func (c *grpcCall) failErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if !c.isShut() {
		c.emit(GrpcEvent{Type: "error", Reason: err.Error()})
	}
	return err
}

// send queues one message for a streaming call. The method's shape is only
// known once its descriptors are in, so a send that beats that waits for it; a
// call that ends first is answered below.
func (c *grpcCall) send(payload string) error {
	select {
	case <-c.ready:
	case <-c.ctx.Done():
	}

	c.mu.Lock()
	streaming, shut := c.clientStream, c.shut
	c.mu.Unlock()
	if shut {
		return fmt.Errorf("call %s is closed", c.id)
	}
	if !streaming {
		return fmt.Errorf("method %s takes a single message: only client-streaming and bidirectional calls send more", c.method)
	}
	if !c.inbox.push(payload) {
		return fmt.Errorf("the sending side of call %s is closed", c.id)
	}
	return nil
}

// closeSend half-closes the call: the supplier sees the end of the queue once it
// has drained it, and grpcurl then tells the server the stream is over.
func (c *grpcCall) closeSend() {
	c.inbox.close()
}

// singleSupplier answers the one message a unary or server-streaming call takes:
// the page's payload once, then the end of the input.
func (c *grpcCall) singleSupplier(payload string, resolver jsonpb.AnyResolver) grpcurl.RequestSupplier {
	body := grpcPayload(payload)
	parser := grpcurl.NewJSONRequestParser(strings.NewReader(body), resolver)
	answered := false
	return func(m proto.Message) error {
		if answered {
			return io.EOF
		}
		answered = true
		if err := parser.Next(m); err != nil {
			return err
		}
		c.emit(GrpcEvent{Type: "message", Direction: "send", Payload: body})
		return nil
	}
}

// streamSupplier answers the messages a streaming call takes: it blocks until
// the page sends one or half-closes. A message is reported to the page once it
// parsed, for the same reason Send reports what the transport took rather than
// what the page asked for.
func (c *grpcCall) streamSupplier(resolver jsonpb.AnyResolver) grpcurl.RequestSupplier {
	return func(m proto.Message) error {
		payload, ok := c.inbox.pop(c.ctx)
		if !ok {
			return io.EOF
		}
		body := grpcPayload(payload)
		if err := grpcurl.NewJSONRequestParser(strings.NewReader(body), resolver).Next(m); err != nil {
			return err
		}
		c.emit(GrpcEvent{Type: "message", Direction: "send", Payload: body})
		return nil
	}
}

// grpcPayload is the JSON one message is written as; an empty payload still has
// to be a message.
func grpcPayload(payload string) string {
	if strings.TrimSpace(payload) == "" {
		return grpcEmptyPayload
	}
	return payload
}

// grpcInbox is the queue a streaming call reads its messages from: GrpcSend
// appends, GrpcCloseSend ends it, and the request supplier blocks on it. It is
// unbounded on purpose — the page decides how much it sends, and holding its
// own messages is cheaper than making it wait for a channel slot.
type grpcInbox struct {
	mu     sync.Mutex
	wake   chan struct{} // closed whenever items or closed change
	items  []string
	closed bool
}

func newGrpcInbox() *grpcInbox {
	return &grpcInbox{wake: make(chan struct{})}
}

// push appends one message; a half-closed or finished inbox refuses it.
func (b *grpcInbox) push(payload string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	b.items = append(b.items, payload)
	b.wakeLocked()
	return true
}

// pop returns the next message, blocking until there is one, the inbox closes
// or ctx ends. It reports false when the stream of messages is over.
func (b *grpcInbox) pop(ctx context.Context) (string, bool) {
	for {
		b.mu.Lock()
		if len(b.items) > 0 {
			payload := b.items[0]
			b.items = b.items[1:]
			b.mu.Unlock()
			return payload, true
		}
		if b.closed {
			b.mu.Unlock()
			return "", false
		}
		wake := b.wake
		b.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			return "", false
		}
	}
}

// close ends the inbox; calling it again is a no-op.
func (b *grpcInbox) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.wakeLocked()
}

// wakeLocked releases everything waiting on the inbox, and arms it for the next
// wait.
func (b *grpcInbox) wakeLocked() {
	close(b.wake)
	b.wake = make(chan struct{})
}

// grpcHandler turns what grpcurl reports about the invocation into the page's
// events.
type grpcHandler struct {
	call      *grpcCall
	formatter grpcurl.Formatter
}

func (h *grpcHandler) OnResolveMethod(*desc.MethodDescriptor) {}

func (h *grpcHandler) OnSendHeaders(metadata.MD) {}

// OnReceiveHeaders is the head of the response: the server's metadata.
func (h *grpcHandler) OnReceiveHeaders(md metadata.MD) {
	h.call.emit(GrpcEvent{Type: "open", Headers: flattenGRPCMetadata(md)})
}

// OnReceiveResponse formats one response message the way the page shows it, with
// default values spelled out so the shape of a message is visible.
func (h *grpcHandler) OnReceiveResponse(m proto.Message) {
	if m == nil {
		return
	}
	payload, err := h.formatter(m)
	if err != nil {
		h.call.emit(GrpcEvent{Type: "error", Reason: fmt.Sprintf("formatting the response failed: %v", err)})
		return
	}
	h.call.emit(GrpcEvent{Type: "message", Direction: "receive", Payload: payload})
}

// OnReceiveTrailers is how the call ended: the status code, the reason the
// server gave, and the trailers it left behind. A call that ended well comes
// with no status at all, which is OK by definition. A call the page ended says
// nothing more, like a socket's close.
func (h *grpcHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
	if h.call.isShut() {
		return
	}
	ev := GrpcEvent{Type: "close", Code: grpcStatusCodeName(codes.OK), Trailers: flattenGRPCMetadata(md)}
	if stat != nil {
		ev.Code = grpcStatusCodeName(stat.Code())
		ev.Reason = stat.Message()
	}
	h.call.emit(ev)
}

// grpcStatusCodeName is the status written the way the page shows it:
// "NOT_FOUND" rather than the CamelCase codes.Code.String answers.
func grpcStatusCodeName(code codes.Code) string {
	name := code.String()
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' && i > 0 && name[i-1] >= 'a' && name[i-1] <= 'z' {
			b.WriteByte('_')
		}
		b.WriteByte(byte(r))
	}
	return strings.ToUpper(b.String())
}

var (
	grpcCallMu sync.Mutex
	grpcCalls  = map[string]*grpcCall{}
)

// registerGrpcCall takes id for call, refusing one already in use.
func registerGrpcCall(call *grpcCall) error {
	grpcCallMu.Lock()
	defer grpcCallMu.Unlock()
	if _, ok := grpcCalls[call.id]; ok {
		return fmt.Errorf("a gRPC call with id %s is already open", call.id)
	}
	grpcCalls[call.id] = call
	return nil
}

// unregisterGrpcCall forgets call, unless its id has come to name another one.
func unregisterGrpcCall(call *grpcCall) {
	grpcCallMu.Lock()
	defer grpcCallMu.Unlock()
	if grpcCalls[call.id] == call {
		delete(grpcCalls, call.id)
	}
}

// lookupGrpcCall finds the call id names.
func lookupGrpcCall(id string) (*grpcCall, error) {
	id = strings.TrimSpace(id)
	grpcCallMu.Lock()
	defer grpcCallMu.Unlock()
	call, ok := grpcCalls[id]
	if !ok {
		return nil, fmt.Errorf("no gRPC call with id %q: connect first", id)
	}
	return call, nil
}
