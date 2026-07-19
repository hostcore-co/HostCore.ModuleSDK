package sdk

import (
	"math/rand"
	"strconv"

	"github.com/extism/go-pdk"
	"github.com/tidwall/gjson"
)

type User struct {
	ID        string
	Email     string
	FirstName string
	LastName  string
}

// Args wraps the raw input JSON and provided typed accessors for fixed fields
// and flexible access for module-specific fields
type Args struct {
	raw []byte
}

// GetArgs reads the input passed by the HostCore Module Engine into the current
// function call and returns a typed Args accessor
func GetArgs() Args {
	return Args{
		raw: pdk.Input(),
	}
}

// User returns a limited user object containing information useful for module executions
func (args Args) User() User {
	return User{
		ID:        gjson.GetBytes(args.raw, "user.id").String(),
		Email:     gjson.GetBytes(args.raw, "user.email").String(),
		FirstName: gjson.GetBytes(args.raw, "user.first_name").String(),
		LastName:  gjson.GetBytes(args.raw, "user.last_name").String(),
	}
}

// ServiceConfig looks up a value from the service_config key-value array by key.
// Returns an empty string if the key is not found
//
// location := args.ServiceConfig("location")
func (args Args) ServiceConfig(key string) string {
	results := gjson.GetBytes(args.raw, "service_config")
	for _, item := range results.Array() {
		if item.Get("key").String() == key {
			return item.Get("value").String()
		}
	}
	return ""
}

// Get returns a Result for any field in the args using dot notation.
// This can be used for module specific fields, that the SDK doesn't know about
//
// region := args.Get("region").String()
// backups := args.Get("limits.backups").Int()
func (args Args) Get(path string) Result {
	return Result{r: gjson.GetBytes(args.raw, path)}
}

// Raw returns the underlying JSON bytes for direct access
func (args Args) Raw() []byte {
	return args.raw
}

// Success writes a JSON result and returns a success exit code.
// WARNING - `data` will not be sanitized and will be stored in a module log
// Pass nil if there is no data to return.
//
// return sdk.Success(nil)
//
// return sdk.Success(sdk.NewJSON().Set("deployed", true).Build())
func Success(data []byte) int32 {
	pdk.Output(buildResult(true, "", data))
	return 0
}

// Fail records an error message and returns a failure exit code
// WARNING - `data` will not be sanitized and will be stored in a module log
// Pass nil if there is no data to return.
//
// return sdk.Fail("no available servers", nil)
//
// return sdk.Fail("invalid location", data)
func Fail(message string, data []byte) int32 {
	pdk.Output(buildResult(false, message, data))
	return 1
}

// Itoa converts an int to a string
func Itoa(n int) string {
	return strconv.Itoa(n)
}

func Itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

func Atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func GetBytes(data []byte, field string) Result {
	return Result{r: gjson.GetBytes(data, field)}
}

// buildResult constructs the ExecutionResponse JSON without using encoding/json.
func buildResult(success bool, errMsg string, data []byte) []byte {
	var b []byte
	b = append(b, '{')

	if success {
		b = append(b, `"success":true`...)
	} else {
		b = append(b, `"success":false`...)
	}

	if errMsg != "" {
		b = append(b, `,"error":`...)
		b = append(b, JsonString(errMsg)...)
	}

	if len(data) > 0 {
		b = append(b, `,"data":`...)
		b = append(b, data...)
	}

	b = append(b, '}')
	return b
}

// JsonString wraps a plain string in JSON quotes and escapes special characters.
func JsonString(s string) []byte {
	b := make([]byte, 0)
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return b
}

type Result struct {
	r gjson.Result
}

func (r Result) String() string { return r.r.String() }
func (r Result) Int() int       { return int(r.r.Int()) }
func (r Result) Int64() int64   { return r.r.Int() }
func (r Result) Float() float64 { return r.r.Float() }
func (r Result) Bool() bool     { return r.r.Bool() }
func (r Result) Exists() bool   { return r.r.Exists() }
func (r Result) IsArray() bool  { return r.r.IsArray() }
func (r Result) IsObject() bool { return r.r.IsObject() }
func (r Result) Raw() string    { return r.r.Raw }
func (r Result) Bytes() []byte  { return []byte(r.r.Raw) }

// Array returns the results as a slice of Result.
func (r Result) Array() []Result {
	raw := r.r.Array()
	out := make([]Result, len(raw))
	for i, v := range raw {
		out[i] = Result{r: v}
	}
	return out
}

// Get retrieves a nested value using dot notation.
func (r Result) Get(path string) Result {
	return Result{r: r.r.Get(path)}
}

// ForEach iterates over array elements or object key-value pairs.
func (r Result) ForEach(fn func(key, value Result) bool) {
	r.r.ForEach(func(k, v gjson.Result) bool {
		return fn(Result{r: k}, Result{r: v})
	})
}

func ParsePortRange(s string) (int, int, bool) {
	s = TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	// find the separator dash (skip index 0 to avoid negative number prefix)
	for i := 1; i < len(s); i++ {
		if s[i] == '-' {
			start, err1 := strconv.Atoi(TrimSpace(s[:i]))
			end, err2 := strconv.Atoi(TrimSpace(s[i+1:]))
			if err1 != nil || err2 != nil {
				return 0, 0, false
			}
			if start > end {
				start, end = end, start
			}
			return start, end, true
		}
	}
	// single port
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, false
	}
	return n, n, true
}

func TrimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

// isSpace reports whether c is an ASCII whitespace byte (space, tab, newline,
// carriage return, vertical tab or form feed).
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

const letters = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandomString returns a random alphanumeric string of length n.
// Used for generating Pterodactyl usernames.
func RandomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
