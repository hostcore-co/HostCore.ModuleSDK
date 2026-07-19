package sdk

import (
	"strconv"

	"github.com/tidwall/gjson"
)

// Parse parses a raw JSON string into a Result. This is useful for
// configuration values that themselves contain embedded JSON, such as a
// "port ranges" field that holds {"SERVER_PORT":"25565-25669"}.
//
//	ports := sdk.Parse(args.ServiceConfig("ports_ranges"))
//	server := ports.Get("SERVER_PORT").String()
func Parse(json string) Result {
	return Result{r: gjson.Parse(json)}
}

// ParseBytes parses raw JSON bytes into a Result.
func ParseBytes(json []byte) Result {
	return Result{r: gjson.ParseBytes(json)}
}

// Atoi64 converts a string to an int64, returning 0 when the string is not a
// valid integer.
func Atoi64(s string) int64 {
	n, _ := strconv.ParseInt(TrimSpace(s), 10, 64)
	return n
}

// ParseFloat converts a string to a float64, returning 0 when the string is
// not a valid number.
func ParseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(TrimSpace(s), 64)
	return f
}

// ParseBool interprets common truthy string representations produced by
// host panels and control frameworks. It returns true for "1", "true",
// "yes", "on" and "y" (case-insensitive); everything else is false.
//
// This is intentionally more permissive than strconv.ParseBool because
// boolean configuration options frequently arrive as "on"/"yes" rather than
// the canonical "true".
func ParseBool(s string) bool {
	switch lower(TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true
	default:
		return false
	}
}

// GBToMB converts a gigabyte value (which may be fractional, e.g. "0.5") to
// whole megabytes. A zero or empty input returns 0 unchanged, matching the
// convention that 0 means "unlimited" in most panels.
func GBToMB(s string) int {
	s = TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	return int(ParseFloat(s) * 1024)
}

// lower lowercases an ASCII string without importing the strings package,
// keeping the compiled module small.
func lower(s string) string {
	var changed bool
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			changed = true
			break
		}
	}
	if !changed {
		return s
	}
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
