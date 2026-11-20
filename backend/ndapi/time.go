package ndapi

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// TimeGlobal is nd.time: epoch timestamps, Go layouts and the local zone. Date
// stays in the sandbox, but its parsing and locale formatting are ambiguous, so
// what has to be exact is done here instead.
func (run *Run) TimeGlobal() *goja.Object {
	vm := run.vm
	api := vm.NewObject()

	_ = api.Set("now", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(time.Now().UnixMilli())
	})
	_ = api.Set("unix", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(time.Now().Unix())
	})

	_ = api.Set("format", func(call goja.FunctionCall) goja.Value {
		const name = "nd.time.format"
		ms := run.timeMillis(name, call, 0, "ms")
		layout := run.timeLayout(name, call, 1)
		loc := run.timeLocation(name, call, 2)
		return vm.ToValue(time.UnixMilli(ms).In(loc).Format(layout))
	})
	_ = api.Set("parse", func(call goja.FunctionCall) goja.Value {
		const name = "nd.time.parse"
		text := run.timeText(name, call, 0, "s")
		layout := run.timeLayout(name, call, 1)
		loc := run.timeLocation(name, call, 2)
		parsed, err := time.ParseInLocation(layout, text, loc)
		if err != nil {
			// A string that does not fit the layout is an answer, not a mistake:
			// null lets a script branch on it, where Date.parse silently guesses.
			return goja.Null()
		}
		return vm.ToValue(parsed.UnixMilli())
	})

	_ = api.Set("offset", func(goja.FunctionCall) goja.Value {
		_, seconds := time.Now().Zone()
		// Date.getTimezoneOffset spells east of UTC negative, and so does this.
		return vm.ToValue(-seconds / 60)
	})
	_ = api.Set("zone", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(time.Local.String())
	})

	return api
}

// timeMillis reads argument i as a finite epoch timestamp in milliseconds.
func (run *Run) timeMillis(name string, call goja.FunctionCall, i int, label string) int64 {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 " + label))
	}
	switch value.Export().(type) {
	case int, int32, int64, float32, float64:
	default:
		panic(run.newError(name + "(): 参数 " + label + " 必须是数字"))
	}
	if f := value.ToFloat(); math.IsNaN(f) || math.IsInf(f, 0) {
		panic(run.newError(name + "(): 参数 " + label + " 必须是有限数字"))
	}
	return value.ToInteger()
}

// timeText reads argument i as text. A missing value or a non-string is a script
// error, so a mistaken call reports itself where it is written instead of
// coercing silently.
func (run *Run) timeText(name string, call goja.FunctionCall, i int, label string) string {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 " + label))
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(run.newError(name + "(): 参数 " + label + " 必须是字符串"))
	}
	return text
}

// timeLayoutTokens maps the date tokens JavaScript developers know onto the Go
// reference layout, longest first so "YYYY" wins over "YY" and "SSS" over "s".
var timeLayoutTokens = []struct{ token, repl string }{
	{"YYYY", "2006"}, {"YY", "06"}, {"SSS", "000"},
	{"MM", "01"}, {"DD", "02"}, {"HH", "15"}, {"mm", "04"}, {"ss", "05"},
	{"M", "1"}, {"D", "2"}, {"H", "3"}, {"m", "4"}, {"s", "5"},
}

// translateTimeLayout converts a layout mixing the tokens above (for example
// "YYYY-MM-DD HH:mm:ss") into the Go reference layout. A layout without a year
// or sub-second token is assumed to already be a Go layout and passes through
// unchanged, so "2006-01-02" keeps working.
func translateTimeLayout(layout string) string {
	if !strings.Contains(layout, "YY") && !strings.Contains(layout, "SSS") {
		return layout
	}
	for _, t := range timeLayoutTokens {
		layout = strings.ReplaceAll(layout, t.token, t.repl)
	}
	return layout
}

// timeLayout reads the layout argument, which has to say what to output or
// match. Either spelling is accepted: the token layout above or a plain Go
// layout.
func (run *Run) timeLayout(name string, call goja.FunctionCall, i int) string {
	layout := run.timeText(name, call, i, "layout")
	if strings.TrimSpace(layout) == "" {
		panic(run.newError(name + "(): 参数 layout 不能为空"))
	}
	return translateTimeLayout(layout)
}

// timeOptional reads argument i as text; absent, null, undefined and a blank
// string all read as "no value given".
func (run *Run) timeOptional(name string, call goja.FunctionCall, i int, label string) (string, bool) {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", false
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(run.newError(name + "(): 参数 " + label + " 必须是字符串"))
	}
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// timeLocation reads the optional time zone argument: an IANA name, or the local
// zone when nothing was given.
func (run *Run) timeLocation(name string, call goja.FunctionCall, i int) *time.Location {
	zone, ok := run.timeOptional(name, call, i, "timeZone")
	if !ok {
		return time.Local
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic(run.newError(name + "(): 不支持的 timeZone " + strconv.Quote(zone) + "，可用 IANA 时区名，如 Asia/Shanghai"))
	}
	return loc
}
