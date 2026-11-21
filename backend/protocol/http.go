package protocol

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/net/http2"
)

// Bounds on what crosses the IPC boundary, so neither a huge response nor a
// huge file can stall the page.
const (
	maxResponseBytes = 10 << 20  // response bodies are cut here
	maxBinaryBytes   = 100 << 20 // binary request bodies may be this large
	maxSentBodyBytes = 64 << 10  // what the echoed request shows of its body
	defaultTimeout   = 30 * time.Second
)

// Do performs req and returns the response, along with what was actually sent.
// An event stream arrives on emit instead of in the body; a nil emit reads the
// stream to its end and only reports the meta, which is what a test wants.
func Do(ctx context.Context, req HTTPRequest, emit Emit) (HTTPResponse, error) {
	target, err := targetURL(req)
	if err != nil {
		return HTTPResponse{}, err
	}

	body, contentType, sentBody, err := buildBody(req)
	if err != nil {
		return HTTPResponse{}, err
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, methodOf(req.Method), target.String(), body)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("invalid request: %w", err)
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
		return HTTPResponse{}, fmt.Errorf("request failed: %w", deadlineCause(err, timedOut.Load()))
	}
	defer resp.Body.Close()

	meta := HTTPResponse{
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
		return streamEvents(resp.Body, meta, emit, start)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("reading the response failed: %w", deadlineCause(err, timedOut.Load()))
	}
	truncated := len(data) > maxResponseBytes
	if truncated {
		data = data[:maxResponseBytes]
	}

	meta.Body = string(data)
	meta.TimeMS = time.Since(start).Milliseconds()
	meta.SizeBytes = len(data)
	meta.Truncated = truncated
	return meta, nil
}

// deadlineCause reports err as the deadline it came from once the request's
// own timeout fired, so a plain response reads as the timeout it always was.
func deadlineCause(err error, timedOut bool) error {
	if timedOut {
		return context.DeadlineExceeded
	}
	return err
}

// targetURL parses req.URL and puts its query parameters on it.
func targetURL(req HTTPRequest) (*url.URL, error) {
	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		return nil, fmt.Errorf("URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q: it needs a scheme and a host, like https://api.example.com", raw)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid query string: %w", err)
	}
	for _, p := range req.Params {
		if p.Key == "" {
			continue
		}
		q.Add(p.Key, p.Value)
	}
	u.RawQuery = q.Encode()
	return u, nil
}

// methodOf uppercases the method, defaulting to GET.
func methodOf(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

var (
	h2cOnce   sync.Once
	h2cShared *http2.Transport
)

// h2cTransport speaks HTTP/2 to a plain-text server with prior knowledge, for
// the http:// requests the page marks as h2c. One transport serves every such
// request: it pools connections itself and is safe for concurrent use.
func h2cTransport() *http2.Transport {
	h2cOnce.Do(func() {
		h2cShared = &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				// 配了 http(s) 代理（环境变量或系统代理）时先 CONNECT 开隧道，
				// 再在隧道里讲 h2c；socks 代理没有 CONNECT 语义，保持直连。
				if proxy := proxyForSchemeHost("http", addr); proxy != nil &&
					(proxy.Scheme == "http" || proxy.Scheme == "https") {
					return dialViaProxy(network, proxy, addr)
				}
				return net.DialTimeout(network, addr, defaultTimeout)
			},
		}
	})
	return h2cShared
}

// buildBody turns the request's body type into a body, the Content-Type it
// implies, and the text preview the page shows. A nil body is no body.
func buildBody(req HTTPRequest) (io.Reader, string, string, error) {
	switch req.BodyType {
	case "formData":
		buf := new(bytes.Buffer)
		w := multipart.NewWriter(buf)
		fields := 0
		files := []string{}
		for _, f := range req.FormData {
			if f.Key == "" {
				continue
			}
			if f.Kind == "file" {
				if strings.TrimSpace(f.FilePath) == "" {
					return nil, "", "", fmt.Errorf("form-data 文件字段 %q 未选择文件", f.Key)
				}
				size, err := writeFormFile(w, f)
				if err != nil {
					return nil, "", "", err
				}
				fields++
				files = append(files, fmt.Sprintf("%s (%d bytes)", filepath.Base(f.FilePath), size))
				continue
			}
			fields++
			if f.ContentType == "" {
				if err := w.WriteField(f.Key, f.Value); err != nil {
					return nil, "", "", fmt.Errorf("building the multipart body: %w", err)
				}
				continue
			}
			part, err := w.CreatePart(textproto.MIMEHeader{
				"Content-Disposition": {`form-data; name="` + quoteMultipart(f.Key) + `"`},
				"Content-Type":        {f.ContentType},
			})
			if err != nil {
				return nil, "", "", fmt.Errorf("building the multipart body: %w", err)
			}
			if _, err := io.WriteString(part, f.Value); err != nil {
				return nil, "", "", fmt.Errorf("building the multipart body: %w", err)
			}
		}
		if err := w.Close(); err != nil {
			return nil, "", "", fmt.Errorf("building the multipart body: %w", err)
		}
		// The page cannot show a multipart body as text, so the preview names
		// what went into it instead.
		preview := "[multipart/form-data]"
		if len(files) > 0 {
			preview = fmt.Sprintf("[multipart/form-data: %d 字段，含文件 %s]", fields, strings.Join(files, "、"))
		}
		return buf, w.FormDataContentType(), preview, nil

	case "urlencoded":
		values := url.Values{}
		for _, kv := range req.URLEncoded {
			if kv.Key == "" {
				continue
			}
			values.Add(kv.Key, kv.Value)
		}
		encoded := values.Encode()
		if encoded == "" {
			return nil, "", "", nil
		}
		return strings.NewReader(encoded), "application/x-www-form-urlencoded", encoded, nil

	case "raw":
		contentType := rawContentType(req.RawType)
		if req.Raw == "" {
			return nil, contentType, "", nil
		}
		return strings.NewReader(req.Raw), contentType, truncateText(req.Raw, maxSentBodyBytes), nil

	case "binary":
		data, err := readBinary(req.BinaryPath)
		if err != nil {
			return nil, "", "", err
		}
		preview := fmt.Sprintf("[binary: %s, %d bytes]", filepath.Base(req.BinaryPath), len(data))
		return bytes.NewReader(data), "application/octet-stream", preview, nil

	default: // "none" and anything unknown
		return nil, "", "", nil
	}
}

// rawContentType maps a raw body's subtype to its media type.
func rawContentType(rawType string) string {
	switch strings.ToLower(strings.TrimSpace(rawType)) {
	case "text":
		return "text/plain"
	case "xml":
		return "application/xml"
	case "html":
		return "text/html"
	default: // "json" and anything unknown
		return "application/json"
	}
}

// quoteMultipart escapes a name for a part's Content-Disposition header.
func quoteMultipart(name string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
}

// writeFormFile writes one file field as a part and returns the file's size.
// Its Content-Type is the field's own when set, else what the bytes look like.
func writeFormFile(w *multipart.Writer, f FormField) (int64, error) {
	fail := func(err error) (int64, error) {
		return 0, fmt.Errorf("form-data 文件字段 %q 读取 %s 失败: %w", f.Key, f.FilePath, err)
	}

	src, err := os.Open(f.FilePath)
	if err != nil {
		return fail(err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("form-data 文件字段 %q 的路径不是文件: %s", f.Key, f.FilePath)
	}
	if info.Size() > maxBinaryBytes {
		return 0, fmt.Errorf("form-data 文件字段 %q 的 %s 是 %d bytes，超过 %d MiB 上限", f.Key, f.FilePath, info.Size(), maxBinaryBytes>>20)
	}

	r := bufio.NewReader(src)
	contentType := f.ContentType
	if contentType == "" {
		contentType = sniffContentType(r)
	}

	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="` + quoteMultipart(f.Key) + `"; filename="` + quoteMultipart(filepath.Base(f.FilePath)) + `"`},
		"Content-Type":        {contentType},
	})
	if err != nil {
		return 0, fmt.Errorf("building the multipart body: %w", err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return fail(err)
	}
	return info.Size(), nil
}

// sniffContentType reports what the start of a part looks like, without
// consuming it. A file too short or too odd to read falls back to bytes.
func sniffContentType(r *bufio.Reader) string {
	head, err := r.Peek(512)
	if len(head) == 0 && err != nil {
		return "application/octet-stream"
	}
	if ct := http.DetectContentType(head); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// readBinary reads the file a binary body sends, refusing one over the limit.
func readBinary(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("binary body needs a file: pick one first")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s failed: %w", filepath.Base(path), err)
	}
	defer f.Close()

	if info, err := f.Stat(); err == nil && info.Size() > maxBinaryBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d MiB limit", filepath.Base(path), info.Size(), maxBinaryBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBinaryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s failed: %w", filepath.Base(path), err)
	}
	if len(data) > maxBinaryBytes {
		return nil, fmt.Errorf("%s is over the %d MiB limit", filepath.Base(path), maxBinaryBytes>>20)
	}
	return data, nil
}

// cookieHeader joins the request's cookies into one Cookie header value.
func cookieHeader(cookies []KeyValue) string {
	pairs := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Key == "" {
			continue
		}
		pairs = append(pairs, c.Key+"="+c.Value)
	}
	return strings.Join(pairs, "; ")
}

// flattenHeaders lists h sorted by key, one row per value.
func flattenHeaders(h http.Header) []KeyValue {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	out := make([]KeyValue, 0, len(h))
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, KeyValue{Key: k, Value: v})
		}
	}
	return out
}

// responseCookies converts what the response set, as the page shows it.
func responseCookies(cookies []*http.Cookie) []ResponseCookie {
	out := make([]ResponseCookie, 0, len(cookies))
	for _, c := range cookies {
		expires := ""
		if !c.Expires.IsZero() {
			expires = c.Expires.Format(time.RFC3339)
		}
		out = append(out, ResponseCookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	return out
}

// truncateText cuts s to at most max bytes, on a rune boundary.
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
