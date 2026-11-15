// Package nbt defines the bounded, typed NBT identity shared with the OC bridge.
package nbt

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"sort"
	"strconv"
)

const (
	MaxCompressed = 12288
	MaxExpanded   = 65536
	MaxDepth      = 32
	MaxNodes      = 1024
)

type node struct {
	prefix, value []byte
	children      []node
	compound      bool
}

func (n node) write(h hash.Hash) {
	h.Write(n.prefix)
	h.Write(n.value)
	for _, child := range n.children {
		child.write(h)
	}
	if n.compound {
		h.Write([]byte{0})
	}
}

type parser struct {
	data       []byte
	pos, nodes int
}

func (p *parser) take(n int) ([]byte, error) {
	if n < 0 || n > len(p.data)-p.pos {
		return nil, fmt.Errorf("NBT: truncated payload")
	}
	value := p.data[p.pos : p.pos+n]
	p.pos += n
	return value, nil
}

func (p *parser) text() error {
	length, err := p.take(2)
	if err != nil {
		return err
	}
	_, err = p.take(int(binary.BigEndian.Uint16(length)))
	return err
}

func (p *parser) payload(kind byte, depth int) (node, error) {
	var result node
	p.nodes++
	if depth > MaxDepth || p.nodes > MaxNodes {
		return result, fmt.Errorf("NBT: structure exceeds budget")
	}
	start := p.pos
	switch kind {
	case 1, 2, 3, 4, 5, 6:
		sizes := [7]int{0, 1, 2, 4, 8, 4, 8}
		if _, err := p.take(sizes[kind]); err != nil {
			return result, err
		}
	case 7, 11, 12:
		count, err := p.take(4)
		if err != nil {
			return result, err
		}
		width := uint32(1)
		if kind == 11 {
			width = 4
		}
		if kind == 12 {
			width = 8
		}
		n := binary.BigEndian.Uint32(count)
		if n > uint32((len(p.data)-p.pos))/width {
			return result, fmt.Errorf("NBT: invalid array length")
		}
		if _, err = p.take(int(n * width)); err != nil {
			return result, err
		}
	case 8:
		if err := p.text(); err != nil {
			return result, err
		}
	case 9:
		header, err := p.take(5)
		if err != nil {
			return result, err
		}
		n := binary.BigEndian.Uint32(header[1:])
		if header[0] > 12 || (header[0] == 0 && n != 0) || n > uint32(MaxNodes-p.nodes) {
			return result, fmt.Errorf("NBT: invalid list")
		}
		result.value = header
		for range int(n) {
			child, err := p.payload(header[0], depth+1)
			if err != nil {
				return result, err
			}
			result.children = append(result.children, child)
		}
		return result, nil
	case 10:
		result.compound = true
		for {
			headerStart := p.pos
			tag, err := p.take(1)
			if err != nil {
				return result, err
			}
			if tag[0] == 0 {
				break
			}
			if err = p.text(); err != nil {
				return result, err
			}
			prefix := p.data[headerStart:p.pos]
			child, err := p.payload(tag[0], depth+1)
			if err != nil {
				return result, err
			}
			child.prefix = prefix
			result.children = append(result.children, child)
		}
		sort.Slice(result.children, func(i, j int) bool {
			return bytes.Compare(result.children[i].prefix[3:], result.children[j].prefix[3:]) < 0
		})
		for i := 1; i < len(result.children); i++ {
			if bytes.Equal(result.children[i-1].prefix[3:], result.children[i].prefix[3:]) {
				return result, fmt.Errorf("NBT: duplicate compound name")
			}
		}
		return result, nil
	default:
		return result, fmt.Errorf("NBT: unsupported tag type")
	}
	result.value = p.data[start:p.pos]
	return result, nil
}

// Digest ignores gzip representation, compound field order and the root name.
// Leaf bytes, numeric types/bits and list/array order are preserved exactly.
// Input must be one complete gzip member containing one named root compound.
func Digest(compressed []byte) ([32]byte, error) {
	var empty [32]byte
	if len(compressed) < 18 || len(compressed) > MaxCompressed || compressed[3]&0xe0 != 0 {
		return empty, fmt.Errorf("NBT: invalid gzip size or flags")
	}
	input := bytes.NewReader(compressed)
	r, err := gzip.NewReader(input)
	if err != nil {
		return empty, fmt.Errorf("NBT: invalid gzip header: %w", err)
	}
	r.Multistream(false)
	data, err := io.ReadAll(io.LimitReader(r, MaxExpanded+1))
	r.Close()
	if err != nil {
		return empty, fmt.Errorf("NBT: invalid gzip payload: %w", err)
	}
	if len(data) > MaxExpanded || input.Len() != 0 {
		return empty, fmt.Errorf("NBT: expanded size or trailing gzip data")
	}
	p := parser{data: data}
	kind, err := p.take(1)
	if err != nil || kind[0] != 10 {
		return empty, fmt.Errorf("NBT: root must be a compound")
	}
	if err = p.text(); err != nil {
		return empty, err
	}
	root, err := p.payload(10, 1)
	if err != nil {
		return empty, err
	}
	if p.pos != len(data) {
		return empty, fmt.Errorf("NBT: trailing payload")
	}
	root.prefix = []byte{10, 0, 0}
	h := sha256.New()
	root.write(h)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

// NESQLItemID returns the identity used by NESQL++/Elysium Source for an
// exact Forge item stack. The source identity is the SHA-256 digest of the
// canonical JSON object {kind:"item",registry,nbt,meta}. The NBT value is
// NESQL++'s lossless typed representation rather than the wire gzip bytes.
func NESQLItemID(registry string, meta int, encoded any) (string, error) {
	if registry == "" {
		return "", fmt.Errorf("NESQL item registry is empty")
	}
	if separator := bytes.IndexByte([]byte(registry), ':'); separator <= 0 || separator == len(registry)-1 {
		return "", fmt.Errorf("NESQL item registry must be namespaced")
	}
	var compressed []byte
	switch value := encoded.(type) {
	case nil:
	case []byte:
		compressed = value
	case string:
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil {
			return "", fmt.Errorf("NBT: invalid base64 payload: %w", err)
		}
		compressed = decoded
	default:
		return "", fmt.Errorf("NBT: unsupported encoded payload %T", encoded)
	}
	typed, err := typedJSON(compressed)
	if err != nil {
		return "", err
	}
	key := map[string]any{
		"kind":     "item",
		"registry": registry,
		"nbt":      typed,
		"meta":     meta,
	}
	digest, err := canonicalDigest(key)
	if err != nil {
		return "", err
	}
	return "item_" + digest, nil
}

func typedJSON(compressed []byte) (any, error) {
	if len(compressed) == 0 {
		return nil, nil
	}
	if len(compressed) < 18 || len(compressed) > MaxCompressed || compressed[3]&0xe0 != 0 {
		return nil, fmt.Errorf("NBT: invalid gzip size or flags")
	}
	input := bytes.NewReader(compressed)
	reader, err := gzip.NewReader(input)
	if err != nil {
		return nil, fmt.Errorf("NBT: invalid gzip header: %w", err)
	}
	reader.Multistream(false)
	data, err := io.ReadAll(io.LimitReader(reader, MaxExpanded+1))
	reader.Close()
	if err != nil {
		return nil, fmt.Errorf("NBT: invalid gzip payload: %w", err)
	}
	if len(data) > MaxExpanded || input.Len() != 0 {
		return nil, fmt.Errorf("NBT: expanded size or trailing gzip data")
	}
	parser := parser{data: data}
	kind, err := parser.take(1)
	if err != nil || kind[0] != 10 {
		return nil, fmt.Errorf("NBT: root must be a compound")
	}
	if err = parser.text(); err != nil {
		return nil, err
	}
	root, err := parser.payload(10, 1)
	if err != nil {
		return nil, err
	}
	if parser.pos != len(data) {
		return nil, fmt.Errorf("NBT: trailing payload")
	}
	value, err := nodeJSON(root)
	if err != nil {
		return nil, err
	}
	return value, nil
}

var typeNames = [...]string{
	"end", "byte", "short", "int", "long", "float", "double",
	"byte_array", "string", "list", "compound", "int_array",
}

func nodeJSON(value node) (any, error) {
	if value.compound {
		fields := make(map[string]any, len(value.children))
		for _, child := range value.children {
			if len(child.prefix) < 3 {
				return nil, fmt.Errorf("NBT: invalid compound field")
			}
			nameLen := int(binary.BigEndian.Uint16(child.prefix[1:3]))
			if len(child.prefix) != 3+nameLen {
				return nil, fmt.Errorf("NBT: invalid compound field name")
			}
			name := string(child.prefix[3:])
			field, err := nodeJSON(child)
			if err != nil {
				return nil, err
			}
			fields[name] = field
		}
		return map[string]any{"type": "compound", "value": fields}, nil
	}
	if len(value.prefix) > 0 {
		// A compound child stores its tag header in prefix; the payload node
		// itself still carries the type-specific bytes in value/children.
		if value.prefix[0] >= byte(len(typeNames)) {
			return nil, fmt.Errorf("NBT: unsupported tag type")
		}
	}
	kind := byte(0)
	if len(value.prefix) > 0 {
		kind = value.prefix[0]
	}
	if kind == 0 {
		// Root payloads are compounds and are handled above. A non-compound
		// root is rejected by the parser before reaching this branch.
		return nil, fmt.Errorf("NBT: missing tag type")
	}
	if int(kind) >= len(typeNames) {
		return nil, fmt.Errorf("NBT: unsupported tag type")
	}
	result := map[string]any{"type": typeNames[kind]}
	switch kind {
	case 1:
		if len(value.value) != 1 {
			return nil, fmt.Errorf("NBT: invalid byte")
		}
		result["value"] = strconv.FormatInt(int64(int8(value.value[0])), 10)
	case 2:
		if len(value.value) != 2 {
			return nil, fmt.Errorf("NBT: invalid short")
		}
		result["value"] = strconv.FormatInt(int64(int16(binary.BigEndian.Uint16(value.value))), 10)
	case 3:
		if len(value.value) != 4 {
			return nil, fmt.Errorf("NBT: invalid int")
		}
		result["value"] = strconv.FormatInt(int64(int32(binary.BigEndian.Uint32(value.value))), 10)
	case 4:
		if len(value.value) != 8 {
			return nil, fmt.Errorf("NBT: invalid long")
		}
		result["value"] = strconv.FormatInt(int64(binary.BigEndian.Uint64(value.value)), 10)
	case 5:
		if len(value.value) != 4 {
			return nil, fmt.Errorf("NBT: invalid float")
		}
		result["value"] = fmt.Sprintf("%08x", binary.BigEndian.Uint32(value.value))
	case 6:
		if len(value.value) != 8 {
			return nil, fmt.Errorf("NBT: invalid double")
		}
		result["value"] = fmt.Sprintf("%016x", binary.BigEndian.Uint64(value.value))
	case 7:
		if len(value.value) < 4 {
			return nil, fmt.Errorf("NBT: invalid byte array")
		}
		result["value"] = base64.StdEncoding.EncodeToString(value.value[4:])
	case 8:
		if len(value.value) < 2 {
			return nil, fmt.Errorf("NBT: invalid string")
		}
		length := int(binary.BigEndian.Uint16(value.value[:2]))
		if len(value.value) != length+2 {
			return nil, fmt.Errorf("NBT: invalid string")
		}
		result["value"] = string(value.value[2:])
	case 9:
		if len(value.value) != 5 || value.value[0] >= byte(len(typeNames)) {
			return nil, fmt.Errorf("NBT: invalid list")
		}
		values := make([]any, 0, len(value.children))
		for _, child := range value.children {
			// List elements do not have a field header. Carry the element type
			// from the list header into a temporary prefix for nodeJSON.
			child.prefix = []byte{value.value[0]}
			decoded, err := nodeJSON(child)
			if err != nil {
				return nil, err
			}
			values = append(values, decoded)
		}
		result["element"] = typeNames[value.value[0]]
		result["value"] = values
	case 10:
		// Compound nodes are marked by compound=true and returned above.
		return nil, fmt.Errorf("NBT: invalid compound node")
	case 11:
		if len(value.value) < 4 || (len(value.value)-4)%4 != 0 {
			return nil, fmt.Errorf("NBT: invalid int array")
		}
		count := int(binary.BigEndian.Uint32(value.value[:4]))
		if count < 0 || count != (len(value.value)-4)/4 {
			return nil, fmt.Errorf("NBT: invalid int array length")
		}
		values := make([]any, count)
		for index := range values {
			start := 4 + index*4
			values[index] = strconv.FormatInt(int64(int32(binary.BigEndian.Uint32(value.value[start:start+4]))), 10)
		}
		result["value"] = values
	case 12:
		if len(value.value) < 4 || (len(value.value)-4)%8 != 0 {
			return nil, fmt.Errorf("NBT: invalid long array")
		}
		count := int(binary.BigEndian.Uint32(value.value[:4]))
		if count < 0 || count != (len(value.value)-4)/8 {
			return nil, fmt.Errorf("NBT: invalid long array length")
		}
		values := make([]any, count)
		for index := range values {
			start := 4 + index*8
			values[index] = strconv.FormatInt(int64(binary.BigEndian.Uint64(value.value[start:start+8])), 10)
		}
		// NESQL++ Source 41 does not expose long_array in its type table. Keep
		// the value typed instead of silently flattening it.
		result["type"] = "long_array"
		result["value"] = values
	default:
		return nil, fmt.Errorf("NBT: unsupported tag type")
	}
	return result, nil
}

func canonicalDigest(value any) (string, error) {
	var out bytes.Buffer
	if err := writeCanonicalJSON(&out, value, 0); err != nil {
		return "", err
	}
	sum := sha256.Sum256(out.Bytes())
	return fmt.Sprintf("%x", sum[:]), nil
}

func writeCanonicalJSON(out *bytes.Buffer, value any, depth int) error {
	if depth > 64 {
		return fmt.Errorf("NESQL identity JSON exceeds nesting limit")
	}
	switch current := value.(type) {
	case nil:
		out.WriteString("null")
	case string:
		writeJSONString(out, current)
	case int:
		out.WriteString(strconv.Itoa(current))
	case int64:
		out.WriteString(strconv.FormatInt(current, 10))
	case []any:
		out.WriteByte('[')
		for index, item := range current {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, item, depth+1); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return bytes.Compare([]byte(keys[i]), []byte(keys[j])) < 0
		})
		out.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, key, depth+1); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := writeCanonicalJSON(out, current[key], depth+1); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("NESQL identity JSON has unsupported value %T", value)
	}
	return nil
}

func writeJSONString(out *bytes.Buffer, value string) {
	const hexDigits = "0123456789abcdef"
	out.WriteByte('"')
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch char {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if char < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hexDigits[char>>4])
				out.WriteByte(hexDigits[char&0xf])
			} else {
				out.WriteByte(char)
			}
		}
	}
	out.WriteByte('"')
}
