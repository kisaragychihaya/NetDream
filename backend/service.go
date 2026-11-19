package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
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

	target, err := targetURL(req)
	if err != nil {
		return failure(err)
	}

	body, contentType, sentBody, err := buildBody(req)
	if err != nil {
		return failure(err)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, methodOf(req.Method), target.String(), body)
	if err != nil {
		return failure(fmt.Errorf("invalid request: %w", err))
	}

	explicit := map[string]bool{}
	for _, h := range req.Headers {
		if h.Key == "" {
			continue
		}
		httpReq.Header.Add(h.Key, h.Value)
		explicit[http.CanonicalHeaderKey(h.Key)] = true
	}

	if contentType != "" && !explicit["Content-Type"] {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if !explicit["Authorization"] {
		switch strings.ToLower(strings.TrimSpace(req.Auth.Type)) {
		case "basic":
			httpReq.SetBasicAuth(req.Auth.Username, req.Auth.Password)
		case "bearer":
			httpReq.Header.Set("Authorization", "Bearer "+req.Auth.Token)
		}
	}
	if !explicit["Cookie"] {
		if cookie := cookieHeader(req.Cookies); cookie != "" {
			httpReq.Header.Set("Cookie", cookie)
		}
	}

	sentHeaders := flattenHeaders(httpReq.Header)
	finalURL := httpReq.URL.String()

	// The deadline covers a plain response, headers and body alike. An event
	// stream drops it as soon as its headers show what it is, and then runs
	// until the page stops it or the window closes.
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	var timedOut atomic.Bool
	expire := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer expire.Stop()

	client := &http.Client{Transport: proxiedTransport()}
	if req.HTTP2 && target.Scheme == "http" {
		client.Transport = h2cTransport()
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return failure(fmt.Errorf("request failed: %w", deadlineCause(err, timedOut.Load())))
	}
	defer resp.Body.Close()

	meta := APIResponse{
		Status:      resp.StatusCode,
		StatusText:  resp.Status,
		Proto:       resp.Proto,
		Headers:     flattenHeaders(resp.Header),
		Cookies:     responseCookies(resp.Cookies()),
		FinalURL:    finalURL,
		SentHeaders: sentHeaders,
		SentBody:    sentBody,
	}

	if isEventStream(resp.Header) {
		expire.Stop()
		streamed, err := streamEvents(resp.Body, meta, stream, start)
		if err != nil {
			return failure(err)
		}
		return finish(streamed)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return failure(fmt.Errorf("reading the response failed: %w", deadlineCause(err, timedOut.Load())))
	}
	truncated := len(data) > maxResponseBytes
	if truncated {
		data = data[:maxResponseBytes]
	}

	meta.Body = string(data)
	meta.TimeMS = time.Since(start).Milliseconds()
	meta.SizeBytes = len(data)
	meta.Truncated = truncated
	return finish(meta)
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

// deadlineCause reports err as the deadline it came from once the request's
// own timeout fired, so a plain response reads as the timeout it always was.
func deadlineCause(err error, timedOut bool) error {
	if timedOut {
		return context.DeadlineExceeded
	}
	return err
}
