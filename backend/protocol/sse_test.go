package protocol

import (
	"strings"
	"testing"
)

// collectSSE parses body and returns the events it dispatched.
func collectSSE(t *testing.T, body string) ([]SSEEvent, error) {
	t.Helper()
	var events []SSEEvent
	err := parseSSE(strings.NewReader(body), func(ev SSEEvent) error {
		events = append(events, ev)
		return nil
	})
	return events, err
}

func TestParseSSEEvents(t *testing.T) {
	body := strings.Join([]string{
		": keep-alive",
		"event: greeting",
		"id: 7",
		"data: hello",
		"data: world",
		"",
		"data: plain",
		"",
		"",
	}, "\n") + ""

	events, err := collectSSE(t, body)
	if err != nil {
		t.Fatalf("parseSSE failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	first := events[0]
	if first.N != 1 || first.ID != "7" || first.Event != "greeting" || first.Data != "hello\nworld" {
		t.Fatalf("first event = %+v", first)
	}
	second := events[1]
	// The id and event of the previous event do not carry over.
	if second.N != 2 || second.ID != "" || second.Event != "message" || second.Data != "plain" {
		t.Fatalf("second event = %+v", second)
	}
}

func TestParseSSEFlushesPendingAtEOF(t *testing.T) {
	events, err := collectSSE(t, "data: tail")
	if err != nil {
		t.Fatalf("parseSSE failed: %v", err)
	}
	if len(events) != 1 || events[0].Data != "tail" {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseSSERejectsLongLine(t *testing.T) {
	line := "data: " + strings.Repeat("x", maxSSELineBytes) + "\n\n"
	if _, err := collectSSE(t, line); err == nil {
		t.Fatal("a line over the line limit was accepted")
	}
}

func TestParseSSERejectsOversizedEvent(t *testing.T) {
	// Each line stays under the line limit, so only the per-event budget can
	// stop this from growing until the page runs out of memory.
	line := "data: " + strings.Repeat("x", maxSSELineBytes/2) + "\n"
	perEvent := strings.Repeat(line, maxSSEEventBytes/maxSSELineBytes*2+2) + "\n"

	_, err := collectSSE(t, perEvent)
	if err == nil {
		t.Fatal("an event over the per-event limit was accepted")
	}
	if !strings.Contains(err.Error(), "larger than 10 MiB") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// sseEventBody builds one event whose data joins to about size bytes, split
// into as many data lines as the line limit requires. It reports the joined
// length, which gains one newline per line boundary.
func sseEventBody(size int) (string, int) {
	var b strings.Builder
	joined, lines := 0, 0
	for remaining := size; remaining > 0; {
		chunk := min(remaining, maxSSELineBytes/2)
		b.WriteString("data: ")
		b.WriteString(strings.Repeat("x", chunk))
		b.WriteString("\n")
		joined += chunk
		lines++
		remaining -= chunk
	}
	b.WriteString("\n")
	return b.String(), joined + lines - 1
}

func TestParseSSEAcceptsEventUnderBudget(t *testing.T) {
	// A big event that stays inside the budget must still dispatch whole.
	body, joined := sseEventBody(maxSSEEventBytes - 4096)
	events, err := collectSSE(t, body)
	if err != nil {
		t.Fatalf("parseSSE failed: %v", err)
	}
	if len(events) != 1 || len(events[0].Data) != joined {
		t.Fatalf("events = %d, data = %d bytes, want %d", len(events), len(events[0].Data), joined)
	}
}

func TestParseSSEResetsBudgetAfterDispatch(t *testing.T) {
	// The budget is per event: two events that each fit must both arrive.
	first, firstLen := sseEventBody(maxSSEEventBytes/2 + 4096)
	second, secondLen := sseEventBody(maxSSEEventBytes/2 + 8192)
	events, err := collectSSE(t, first+second)
	if err != nil {
		t.Fatalf("parseSSE failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if len(events[0].Data) != firstLen || len(events[1].Data) != secondLen {
		t.Fatalf("data lengths = %d, %d; want %d, %d",
			len(events[0].Data), len(events[1].Data), firstLen, secondLen)
	}
}
