package sdk

// A minimal, dependency-free XML reader for modules that consume an XML API
// (many domain registrars, some panels). It is pure byte scanning — no
// encoding/xml and no reflect — so it stays small and TinyGo-friendly, matching
// the SDK's hand-rolled JSON approach. It is a reader only: it decodes
// elements, attributes and text, which is all a module needs to read a
// response.

type xmlNode struct {
	name  string
	attrs []xmlAttr
	kids  []*xmlNode
	chars string
}

type xmlAttr struct{ k, v string }

// XMLNode is a handle to a parsed element. A nil underlying node is valid and
// yields empty results, so callers can chain accessors without nil checks.
type XMLNode struct{ n *xmlNode }

// ParseXML parses an XML document and returns its root element.
//
//	root := sdk.ParseXML(body)
//	status := root.Attr("Status")
//	expiry := root.Get("CommandResponse.DomainGetInfoResult.DomainDetails.ExpiredDate").Text()
func ParseXML(data []byte) XMLNode {
	p := &xmlParser{data: data}
	doc := &xmlNode{}
	p.parseContent(doc)
	for _, k := range doc.kids {
		return XMLNode{n: k}
	}
	return XMLNode{}
}

// Exists reports whether the node was found.
func (x XMLNode) Exists() bool { return x.n != nil }

// Attr returns the value of the named attribute, or "".
func (x XMLNode) Attr(name string) string {
	if x.n == nil {
		return ""
	}
	for _, a := range x.n.attrs {
		if a.k == name {
			return a.v
		}
	}
	return ""
}

// Text returns the node's trimmed character data.
func (x XMLNode) Text() string {
	if x.n == nil {
		return ""
	}
	return TrimSpace(x.n.chars)
}

// Child returns the first direct child element with the given name.
func (x XMLNode) Child(name string) XMLNode {
	if x.n == nil {
		return XMLNode{}
	}
	for _, k := range x.n.kids {
		if k.name == name {
			return XMLNode{n: k}
		}
	}
	return XMLNode{}
}

// Get walks a dot-separated path of child element names.
func (x XMLNode) Get(path string) XMLNode {
	cur := x
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '.' {
			cur = cur.Child(path[start:i])
			start = i + 1
		}
	}
	return cur
}

// Children returns all direct child elements with the given name.
func (x XMLNode) Children(name string) []XMLNode {
	if x.n == nil {
		return nil
	}
	var out []XMLNode
	for _, k := range x.n.kids {
		if k.name == name {
			out = append(out, XMLNode{n: k})
		}
	}
	return out
}

type xmlParser struct {
	data []byte
	pos  int
}

func (p *xmlParser) parseContent(parent *xmlNode) {
	for p.pos < len(p.data) {
		start := p.pos
		for p.pos < len(p.data) && p.data[p.pos] != '<' {
			p.pos++
		}
		if p.pos > start {
			parent.chars += xmlUnescape(p.data[start:p.pos])
		}
		if p.pos >= len(p.data) {
			return
		}
		switch {
		case p.has("</"):
			return
		case p.has("<?"):
			p.skipPast("?>")
		case p.has("<!--"):
			p.skipPast("-->")
		case p.has("<![CDATA["):
			p.pos += len("<![CDATA[")
			s := p.pos
			p.skipPast("]]>")
			end := p.pos - len("]]>")
			if end > s {
				parent.chars += string(p.data[s:end])
			}
		case p.has("<!"):
			p.skipPast(">")
		default:
			parent.kids = append(parent.kids, p.parseElement())
		}
	}
}

func (p *xmlParser) parseElement() *xmlNode {
	p.pos++ // skip '<'
	n := &xmlNode{name: p.readName()}

	for {
		p.skipSpaces()
		if p.pos >= len(p.data) {
			return n
		}
		switch p.data[p.pos] {
		case '/':
			p.pos++
			if p.pos < len(p.data) && p.data[p.pos] == '>' {
				p.pos++
			}
			return n
		case '>':
			p.pos++
			p.parseContent(n)
			if p.has("</") {
				p.pos += 2
				p.readName()
				p.skipPast(">")
			}
			return n
		default:
			k := p.readName()
			p.skipSpaces()
			v := ""
			if p.pos < len(p.data) && p.data[p.pos] == '=' {
				p.pos++
				p.skipSpaces()
				v = p.readQuoted()
			}
			if k != "" {
				n.attrs = append(n.attrs, xmlAttr{k, v})
			}
		}
	}
}

func (p *xmlParser) readName() string {
	start := p.pos
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == ':' || c == '-' || c == '_' || c == '.' {
			p.pos++
			continue
		}
		break
	}
	return string(p.data[start:p.pos])
}

func (p *xmlParser) readQuoted() string {
	if p.pos >= len(p.data) {
		return ""
	}
	q := p.data[p.pos]
	if q != '"' && q != '\'' {
		return ""
	}
	p.pos++
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != q {
		p.pos++
	}
	v := xmlUnescape(p.data[start:p.pos])
	if p.pos < len(p.data) {
		p.pos++ // closing quote
	}
	return v
}

func (p *xmlParser) skipSpaces() {
	for p.pos < len(p.data) && isSpace(p.data[p.pos]) {
		p.pos++
	}
}

func (p *xmlParser) has(s string) bool {
	if p.pos+len(s) > len(p.data) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if p.data[p.pos+i] != s[i] {
			return false
		}
	}
	return true
}

func (p *xmlParser) skipPast(marker string) {
	for p.pos < len(p.data) {
		if p.has(marker) {
			p.pos += len(marker)
			return
		}
		p.pos++
	}
}

// xmlUnescape resolves the five predefined entities plus numeric character
// references. Unknown entities are left verbatim.
func xmlUnescape(b []byte) string {
	amp := false
	for i := 0; i < len(b); i++ {
		if b[i] == '&' {
			amp = true
			break
		}
	}
	if !amp {
		return string(b)
	}

	out := make([]byte, 0)
	for i := 0; i < len(b); {
		if b[i] != '&' {
			out = append(out, b[i])
			i++
			continue
		}
		semi := -1
		for j := i + 1; j < len(b) && j < i+12; j++ {
			if b[j] == ';' {
				semi = j
				break
			}
		}
		if semi < 0 {
			out = append(out, b[i])
			i++
			continue
		}
		switch string(b[i+1 : semi]) {
		case "amp":
			out = append(out, '&')
		case "lt":
			out = append(out, '<')
		case "gt":
			out = append(out, '>')
		case "quot":
			out = append(out, '"')
		case "apos":
			out = append(out, '\'')
		default:
			if r, ok := numericEntity(b[i+1 : semi]); ok {
				out = appendRune(out, r)
			} else {
				out = append(out, b[i:semi+1]...)
			}
		}
		i = semi + 1
	}
	return string(out)
}

func numericEntity(b []byte) (rune, bool) {
	if len(b) < 2 || b[0] != '#' {
		return 0, false
	}
	base := 10
	digits := b[1:]
	if digits[0] == 'x' || digits[0] == 'X' {
		base = 16
		digits = digits[1:]
	}
	n := 0
	for _, c := range digits {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		n = n*base + d
	}
	return rune(n), true
}

func appendRune(b []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(b, byte(r))
	case r < 0x800:
		return append(b, byte(0xC0|r>>6), byte(0x80|r&0x3F))
	case r < 0x10000:
		return append(b, byte(0xE0|r>>12), byte(0x80|r>>6&0x3F), byte(0x80|r&0x3F))
	default:
		return append(b, byte(0xF0|r>>18), byte(0x80|r>>12&0x3F), byte(0x80|r>>6&0x3F), byte(0x80|r&0x3F))
	}
}
