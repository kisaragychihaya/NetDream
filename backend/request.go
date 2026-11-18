package main

import (
	"bytes"
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

// targetURL parses req.URL and puts its query parameters on it.
func targetURL(req APIRequest) (*url.URL, error) {
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

// h2cTransport speaks HTTP/2 to a plain-text server with prior knowledge, for
// the http:// requests the page marks as h2c.
func h2cTransport() *http2.Transport {
	return &http2.Transport{
		AllowHTTP: true,
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.DialTimeout(network, addr, defaultTimeout)
		},
	}
}

// buildBody turns the request's body type into a body, the Content-Type it
// implies, and the text preview the page shows. A nil body is no body.
func buildBody(req APIRequest) (io.Reader, string, string, error) {
	switch req.BodyType {
	case "formData":
		buf := new(bytes.Buffer)
		w := multipart.NewWriter(buf)
		for _, f := range req.FormData {
			if f.Key == "" {
				continue
			}
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
		return buf, w.FormDataContentType(), "[multipart/form-data]", nil

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
