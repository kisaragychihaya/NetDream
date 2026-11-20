package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"

	"netdream/backend/protocol"
)

// RequestService sends HTTP requests for the debug page and picks files for it.
// Every exported method here is bound to the frontend, so helpers stay unexported.
type RequestService struct{}

// Send performs req and returns the response, along with what was actually sent.
// An event stream arrives on stream instead of in the body.
func (RequestService) Send(ctx context.Context, req APIRequest, stream *mygo.Channel[SSEMessage]) (APIResponse, error) {
	// The variables the page sent travel out again as they came in when no
	// script touches them.
	if req.Vars == nil {
		req.Vars = map[string]string{}
	}
	vars := req.Vars
	logs := []ScriptLog{}
	tests := []ScriptTest{}

	// Every answer carries the variable snapshot and the script output, so the
	// page's types hold even when nothing ran or the request fails.
	failure := func(err error) (APIResponse, error) {
		return APIResponse{Vars: vars, ScriptLogs: logs, ScriptTests: tests}, err
	}

	if req.PreScript != "" {
		out, err := runPreScript(ctx, req.PreScript, &req, vars)
		if out != nil {
			vars = out.vars
			logs = append(logs, out.logs...)
			tests = append(tests, out.tests...)
		}
		if err != nil {
			// A pre script that fails keeps the request from going out; the
			// page shows the error with what the script logged until then.
			return APIResponse{
				Vars:           vars,
				ScriptLogs:     logs,
				ScriptTests:    tests,
				PreScriptError: err.Error(),
			}, nil
		}
	}
	applyVars(&req, vars)

	// finish runs the post script, once there is a response to run it against,
	// and hangs what the scripts produced on it.
	finish := func(res APIResponse) (APIResponse, error) {
		if req.PostScript != "" {
			out, err := runPostScript(ctx, req.PostScript, &req, &res, vars)
			if out != nil {
				vars = out.vars
				logs = append(logs, out.logs...)
				tests = append(tests, out.tests...)
			}
			if err != nil {
				// The response itself stands: only its script failed.
				res.PostScriptError = err.Error()
			}
		}
		res.Vars = vars
		res.ScriptLogs = logs
		res.ScriptTests = tests
		return res, nil
	}

	res, err := protocol.Do(ctx, protocolRequest(req), sseEmitter(stream))
	if err != nil {
		return failure(err)
	}
	return finish(apiResponse(res))
}

// PickFile asks the user for a file and returns its path, or "" when they
// cancel the dialog.
func (RequestService) PickFile() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择文件"})
	if err != nil {
		return "", fmt.Errorf("opening the file dialog failed: %w", err)
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// protocolRequest is the request as the protocol package takes it: the page's
// contract types stop at this package's edge.
func protocolRequest(req APIRequest) protocol.HTTPRequest {
	return protocol.HTTPRequest{
		Method:     req.Method,
		URL:        req.URL,
		Params:     protocolPairs(req.Params),
		Headers:    protocolPairs(req.Headers),
		BodyType:   req.BodyType,
		FormData:   protocolFields(req.FormData),
		URLEncoded: protocolPairs(req.URLEncoded),
		Raw:        req.Raw,
		RawType:    req.RawType,
		BinaryPath: req.BinaryPath,
		Auth: protocol.AuthInfo{
			Type:     req.Auth.Type,
			Username: req.Auth.Username,
			Password: req.Auth.Password,
			Token:    req.Auth.Token,
		},
		Cookies: protocolPairs(req.Cookies),
		Timeout: req.Timeout,
		HTTP2:   req.HTTP2,
	}
}

// apiResponse is what the protocol package answered, as the page reads it.
func apiResponse(res protocol.HTTPResponse) APIResponse {
	cookies := make([]ResponseCookie, 0, len(res.Cookies))
	for _, c := range res.Cookies {
		cookies = append(cookies, ResponseCookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	return APIResponse{
		Status:      res.Status,
		StatusText:  res.StatusText,
		Proto:       res.Proto,
		Headers:     pairsFromProtocol(res.Headers),
		Cookies:     cookies,
		Body:        res.Body,
		TimeMS:      res.TimeMS,
		SizeBytes:   res.SizeBytes,
		Truncated:   res.Truncated,
		FinalURL:    res.FinalURL,
		SentHeaders: pairsFromProtocol(res.SentHeaders),
		SentBody:    res.SentBody,
	}
}

// sseEmitter adapts the page's channel to the callback the protocol package
// streams an event stream through.
func sseEmitter(stream *mygo.Channel[SSEMessage]) protocol.Emit {
	if stream == nil {
		return nil
	}
	return func(msg protocol.SSEMessage) error {
		return stream.Send(SSEMessage{Open: sseOpen(msg.Open), Event: sseEvent(msg.Event)})
	}
}

// sseOpen is a stream head as the page reads it.
func sseOpen(open *protocol.SSEOpen) *SSEOpen {
	if open == nil {
		return nil
	}
	return &SSEOpen{
		Status:     open.Status,
		StatusText: open.StatusText,
		Proto:      open.Proto,
		Headers:    pairsFromProtocol(open.Headers),
	}
}

// sseEvent is one dispatched event as the page reads it.
func sseEvent(event *protocol.SSEEvent) *SSEEvent {
	if event == nil {
		return nil
	}
	return &SSEEvent{N: event.N, ID: event.ID, Event: event.Event, Data: event.Data}
}

// protocolPairs hands a list of key/value pairs to the protocol package.
func protocolPairs(list []KeyValue) []protocol.KeyValue {
	out := make([]protocol.KeyValue, 0, len(list))
	for _, kv := range list {
		out = append(out, protocol.KeyValue{Key: kv.Key, Value: kv.Value})
	}
	return out
}

// pairsFromProtocol is the reverse of protocolPairs.
func pairsFromProtocol(list []protocol.KeyValue) []KeyValue {
	out := make([]KeyValue, 0, len(list))
	for _, kv := range list {
		out = append(out, KeyValue{Key: kv.Key, Value: kv.Value})
	}
	return out
}

// protocolFields hands the multipart fields to the protocol package.
func protocolFields(list []FormField) []protocol.FormField {
	out := make([]protocol.FormField, 0, len(list))
	for _, f := range list {
		out = append(out, protocol.FormField{
			Key:         f.Key,
			Value:       f.Value,
			ContentType: f.ContentType,
			Description: f.Description,
			Kind:        f.Kind,
			FilePath:    f.FilePath,
		})
	}
	return out
}
