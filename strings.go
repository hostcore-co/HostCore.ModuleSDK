package sdk

// URLEncode percent-encodes a string for safe use as a URL query-parameter
// value. Everything outside the RFC 3986 unreserved set is escaped. It avoids
// importing net/url so the compiled module stays small.
//
//	resp := sdk.HTTPGet(base+"/users?search="+sdk.URLEncode(email), headers)
func URLEncode(s string) string {
	const upperhex = "0123456789ABCDEF"
	b := make([]byte, 0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b = append(b, c)
			continue
		}
		b = append(b, '%', upperhex[c>>4], upperhex[c&0x0f])
	}
	return string(b)
}

// EqualFold reports whether a and b are equal under ASCII case-folding. Unlike
// strings.EqualFold it does not handle Unicode case mapping, which module
// scripts rarely need and which would pull in the unicode tables.
func EqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// StringList normalises a value that may be either a single string or a JSON
// array of strings into a trimmed, de-duplicated slice. It is convenient for
// address fields (to, cc, …) that accept one or many recipients.
//
//	to := sdk.StringList(args.Get("to"))
func StringList(r Result) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if r.IsArray() {
		for _, item := range r.Array() {
			add(item.String())
		}
	} else {
		add(r.String())
	}
	return out
}

// SplitCSV splits a comma-separated string, trimming whitespace from each
// element and dropping empties. It returns nil for an all-empty input.
//
//	tags := sdk.SplitCSV("premium, ssd ,") // ["premium", "ssd"]
func SplitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if part := TrimSpace(s[start:i]); part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
