package ndapi

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTranslateTimeLayout(t *testing.T) {
	tests := []struct{ in, want string }{
		{"YYYY-MM-DD HH:mm:ss", "2006-01-02 15:04:05"},
		{"YY/M/D H:mm", "06/1/2 3:04"},
		{"ss.SSS", "05.000"},
		{"2006-01-02", "2006-01-02"},     // already a Go layout, passes through
		{"15:04:05.000", "15:04:05.000"}, // no year token, passes through
	}
	for _, tc := range tests {
		if got := translateTimeLayout(tc.in); got != tc.want {
			t.Errorf("translateTimeLayout(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTimeFormatParse(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Shanghai zone: %v", err)
	}
	// 2024-03-05 12:00:00.123 in Shanghai, the value the round-trip parses to.
	wantMS := time.Date(2024, 3, 5, 12, 0, 0, 123_000_000, shanghai).UnixMilli()

	out := evalScript(t, map[string]any{"MS": wantMS}, `
	var formatted = nd.time.format(MS, "YYYY-MM-DD HH:mm:ss.SSS", "Asia/Shanghai");
	var parsed = nd.time.parse(formatted, "YYYY-MM-DD HH:mm:ss.SSS", "Asia/Shanghai");
	[
		nd.time.format(0, "YYYY-MM-DD HH:mm:ss.SSS", "UTC"),
		nd.time.format(0, "2006-01-02", "UTC"),
		nd.time.format(0, "YYYY-MM-DD HH:mm:ss", "Asia/Shanghai"),
		nd.time.format(1500, "YYYY-MM-DD HH:mm:ss.SSS", "UTC"),
		nd.time.format(MS, "YYYY-MM-DD HH:mm:ss.SSS", "UTC"),
		formatted,
		String(parsed),
		String(nd.time.parse("1970-01-02", "2006-01-02", "UTC")),
		String(nd.time.parse("nope", "YYYY-MM-DD", "UTC"))
	].join("|")`)

	parts := splitParts(out)
	want := []string{
		"1970-01-01 00:00:00.000",
		"1970-01-01",
		"1970-01-01 08:00:00",
		"1970-01-01 00:00:01.500",
		time.UnixMilli(wantMS).UTC().Format("2006-01-02 15:04:05.000"),
		"2024-03-05 12:00:00.123",
		strconv.FormatInt(wantMS, 10),
		"86400000",
		"null",
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d values, want %d: %v", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("time value %d = %q, want %q", i, parts[i], want[i])
		}
	}
}

func TestTimeErrors(t *testing.T) {
	out := evalScript(t, nil, `
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		msg(function () { nd.time.format(0, "   ", "UTC"); }),
		msg(function () { nd.time.format(0, "YYYY", "Mars/Olympus"); }),
		msg(function () { nd.time.format("now", "YYYY", "UTC"); })
	].join("|")`)

	contains := []string{"layout 不能为空", "不支持的 timeZone", "必须是数字"}
	parts := splitParts(out)
	for i, want := range contains {
		if !strings.Contains(parts[i], want) {
			t.Errorf("time error %d = %q, want it to contain %q", i, parts[i], want)
		}
	}
}
