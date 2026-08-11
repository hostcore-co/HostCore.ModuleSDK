package sdk

// JSON is a simple ordered key-value builder for constructing JSON objects
// to pass back
//
// Only handles string, []byte (raw JSON), bool, int, and float64 values.
//
//	data := sdk.NewJSON().
//	    Set("deployed", true).
//	    Set("service_id", "abc123").
//	    Set("response", resp.Body).
//	    Build()
type JSON struct {
	keys   []string
	values [][]byte
}

// NewJSON creates a new JSON builder.
func NewJSON() *JSON {
	return &JSON{}
}

// Set adds a key-value pair. Supported value types:
//   - string    → JSON string
//   - []byte    → raw JSON (embedded as-is, assumed to be valid JSON)
//   - bool      → true / false
//   - int       → number
//   - float64   → number
func (j *JSON) Set(key string, value interface{}) *JSON {
	j.keys = append(j.keys, key)
	j.values = append(j.values, encodeValue(value))
	return j
}

// Build returns the JSON bytes.
func (j *JSON) Build() []byte {
	var b []byte
	b = append(b, '{')
	for i, k := range j.keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, JsonString(k)...)
		b = append(b, ':')
		b = append(b, j.values[i]...)
	}
	b = append(b, '}')
	return b
}

// JSONInt64Array encodes a slice of int64 values as a JSON array of numbers,
// e.g. []int64{7, 12} → [7,12]. An empty slice encodes as [].
//
// Useful for Pterodactyl allocation lists such as add_allocations.
func JSONInt64Array(items []int64) []byte {
	b := make([]byte, 0)
	b = append(b, '[')
	for i, v := range items {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, Itoa64(v)...)
	}
	b = append(b, ']')
	return b
}

// JSONStringArray encodes a slice of strings as a JSON array of strings,
// e.g. []string{"25565-25669"} → ["25565-25669"]. Each element is escaped.
func JSONStringArray(items []string) []byte {
	b := make([]byte, 0)
	b = append(b, '[')
	for i, v := range items {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, JsonString(v)...)
	}
	b = append(b, ']')
	return b
}

// encodeValue converts a Go value to its JSON bytes representation.
func encodeValue(v interface{}) []byte {
	switch val := v.(type) {
	case string:
		return JsonString(val)
	case []byte:
		if len(val) == 0 {
			return []byte("null")
		}
		return val
	case bool:
		if val {
			return []byte("true")
		}
		return []byte("false")
	case int:
		return intToBytes(val)
	case float64:
		return float64ToBytes(val)
	case nil:
		return []byte("null")
	default:
		return []byte("null")
	}
}

func intToBytes(n int) []byte {
	if n == 0 {
		return []byte("0")
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := 20
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return buf[pos:]
}

func float64ToBytes(f float64) []byte {
	// Simple float formatting — for precision-sensitive values use string formatting
	// This handles common cases (no scientific notation, 6 decimal places max)
	if f == 0 {
		return []byte("0")
	}

	neg := f < 0
	if neg {
		f = -f
	}

	intPart := int(f)
	fracPart := f - float64(intPart)

	b := intToBytes(intPart)
	if neg {
		b = append([]byte{'-'}, b...)
	}

	if fracPart > 0 {
		b = append(b, '.')
		for i := 0; i < 6 && fracPart > 0; i++ {
			fracPart *= 10
			digit := int(fracPart)
			b = append(b, byte('0'+digit))
			fracPart -= float64(digit)
		}
		// Trim trailing zeros
		for len(b) > 0 && b[len(b)-1] == '0' {
			b = b[:len(b)-1]
		}
	}

	return b
}
