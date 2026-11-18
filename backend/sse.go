package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// maxSSELineBytes is the longest line of an event stream we accept; a server
// that sends more than this is not speaking events we can parse.
const maxSSELineBytes = 1 << 20

// isEventStream reports whether the response headers announce an event stream.
func isEventStream(h http.Header) bool {
	mediaType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mediaType == "text/event-stream"
}

// sseSink is where the SSE path pushes its messages; the bound method's
// *mygo.Channel[SSEMessage] is the one sink there is.
type sseSink interface {
	Send(SSEMessage) error
}

// streamEvents pushes the response head and then every event of body to the
// page, and returns the meta of the stream once it ends.
func streamEvents(body io.Reader, meta APIResponse, sink sseSink, start time.Time) (APIResponse, error) {
	if err := sink.Send(SSEMessage{Open: &SSEOpen{
		Status:     meta.Status,
		StatusText: meta.StatusText,
		Proto:      meta.Proto,
		Headers:    meta.Headers,
	}}); err != nil {
		return APIResponse{}, err
	}

	size := 0
	err := parseSSE(body, func(event SSEEvent) error {
		size += len(event.Data)
		return sink.Send(SSEMessage{Event: &event})
	})
	if err != nil {
		if errors.Is(err, mygo.ErrChannelClosed) {
			return APIResponse{}, err
		}
		return APIResponse{}, fmt.Errorf("reading the event stream failed: %w", err)
	}

	meta.TimeMS = time.Since(start).Milliseconds()
	meta.SizeBytes = size
	return meta, nil
}

// parseSSE reads an event stream and calls emit for each event it dispatches:
// a blank line dispatches, data lines join with \n, comments and retry are
// ignored, and the end of the stream flushes what it left pending.
func parseSSE(body io.Reader, emit func(SSEEvent) error) error {
	reader := bufio.NewReaderSize(body, maxSSELineBytes)

	var (
		data  []string
		id    string
		event string
		n     int
	)

	dispatch := func() error {
		if len(data) == 0 && id == "" && event == "" {
			return nil
		}
		n++
		next := SSEEvent{N: n, ID: id, Event: event, Data: strings.Join(data, "\n")}
		if next.Event == "" {
			next.Event = "message"
		}
		data = data[:0]
		id, event = "", ""
		return emit(next)
	}

	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return fmt.Errorf("event stream line is longer than %d bytes", maxSSELineBytes)
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		switch {
		case text == "":
			// A blank line dispatches the event being built, if any.
			if len(line) > 0 {
				if derr := dispatch(); derr != nil {
					return derr
				}
			}
		case strings.HasPrefix(text, ":"):
			// A comment, often a keep-alive: nothing to dispatch.
		default:
			field, value, found := strings.Cut(text, ":")
			if found {
				value = strings.TrimPrefix(value, " ")
			}
			switch field {
			case "data":
				data = append(data, value)
			case "event":
				event = value
			case "id":
				if !strings.ContainsRune(value, 0) {
					id = value
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			return err
		}
	}
}
