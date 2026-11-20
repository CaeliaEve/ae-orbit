package nbt

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"unicode/utf16"
)

var typedEncoders = sync.Pool{New: func() any { writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed); return writer }}

// EncodeTypedJSON preserves NESQL's tag types and float bit patterns. Browser
// JSON numbers are never used for integers or 64-bit values.
func EncodeTypedJSON(data []byte) ([]byte, error) {
	var out bytes.Buffer
	nodes := 0
	var write func(json.RawMessage, int, bool) (byte, error)
	text := func(value string) error {
		var encoded []byte
		for _, unit := range utf16.Encode([]rune(value)) {
			switch {
			case unit > 0 && unit < 128:
				encoded = append(encoded, byte(unit))
			case unit < 2048:
				encoded = append(encoded, 0xc0|byte(unit>>6), 0x80|byte(unit&63))
			default:
				encoded = append(encoded, 0xe0|byte(unit>>12), 0x80|byte(unit>>6&63), 0x80|byte(unit&63))
			}
		}
		if len(encoded) > 65535 {
			return fmt.Errorf("NBT: string exceeds budget")
		}
		binary.Write(&out, binary.BigEndian, uint16(len(encoded)))
		out.Write(encoded)
		return nil
	}
	write = func(raw json.RawMessage, depth int, header bool) (byte, error) {
		nodes++
		if depth > MaxDepth || nodes > MaxNodes || out.Len() > MaxExpanded {
			return 0, fmt.Errorf("NBT: structure exceeds budget")
		}
		var tag struct {
			Type    string          `json:"type"`
			Value   json.RawMessage `json:"value"`
			Element string          `json:"element"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&tag); err != nil {
			return 0, err
		}
		kind := byte(255)
		for i, name := range typeNames {
			if name == tag.Type {
				kind = byte(i)
			}
		}
		if tag.Type == "long_array" {
			kind = 12
		}
		if kind == 255 || kind == 0 {
			return 0, fmt.Errorf("NBT: unsupported type %s", tag.Type)
		}
		if header {
			out.WriteByte(kind)
		}
		str := func() (string, error) { var s string; err := json.Unmarshal(tag.Value, &s); return s, err }
		switch kind {
		case 1, 2, 3, 4:
			s, err := str()
			if err != nil {
				return 0, err
			}
			bits := map[byte]int{1: 8, 2: 16, 3: 32, 4: 64}[kind]
			value, err := strconv.ParseInt(s, 10, bits)
			if err != nil || strconv.FormatInt(value, 10) != s {
				return 0, fmt.Errorf("NBT: invalid integer")
			}
			switch kind {
			case 1:
				out.WriteByte(byte(value))
			case 2:
				binary.Write(&out, binary.BigEndian, int16(value))
			case 3:
				binary.Write(&out, binary.BigEndian, int32(value))
			case 4:
				binary.Write(&out, binary.BigEndian, value)
			}
		case 5, 6:
			s, err := str()
			if err != nil {
				return 0, err
			}
			size := 4
			if kind == 6 {
				size = 8
			}
			value, err := hex.DecodeString(s)
			if err != nil || len(value) != size {
				return 0, fmt.Errorf("NBT: invalid float bits")
			}
			out.Write(value)
		case 7:
			s, err := str()
			if err != nil {
				return 0, err
			}
			value, err := base64.StdEncoding.Strict().DecodeString(s)
			if err != nil {
				return 0, err
			}
			binary.Write(&out, binary.BigEndian, int32(len(value)))
			out.Write(value)
		case 8:
			s, err := str()
			if err != nil {
				return 0, err
			}
			if err = text(s); err != nil {
				return 0, err
			}
		case 9:
			var list []json.RawMessage
			if err := json.Unmarshal(tag.Value, &list); err != nil {
				return 0, err
			}
			sub := byte(255)
			for i, name := range typeNames {
				if tag.Element == name {
					sub = byte(i)
				}
			}
			if tag.Element == "long_array" {
				sub = 12
			}
			if sub == 255 || sub == 0 && len(list) > 0 {
				return 0, fmt.Errorf("NBT: invalid list type")
			}
			out.WriteByte(sub)
			binary.Write(&out, binary.BigEndian, int32(len(list)))
			for _, value := range list {
				k, err := write(value, depth+1, false)
				if err != nil {
					return 0, err
				}
				if k != sub {
					return 0, fmt.Errorf("NBT: heterogeneous list")
				}
			}
		case 10:
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(tag.Value, &fields); err != nil {
				return 0, err
			}
			names := make([]string, 0, len(fields))
			for name := range fields {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				var child struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(fields[name], &child); err != nil {
					return 0, err
				}
				sub := byte(255)
				for i, n := range typeNames {
					if n == child.Type {
						sub = byte(i)
					}
				}
				if child.Type == "long_array" {
					sub = 12
				}
				if sub == 255 || sub == 0 {
					return 0, fmt.Errorf("NBT: invalid compound child")
				}
				out.WriteByte(sub)
				if err := text(name); err != nil {
					return 0, err
				}
				if _, err := write(fields[name], depth+1, false); err != nil {
					return 0, err
				}
			}
			out.WriteByte(0)
		case 11, 12:
			var list []string
			if err := json.Unmarshal(tag.Value, &list); err != nil {
				return 0, err
			}
			binary.Write(&out, binary.BigEndian, int32(len(list)))
			bits := 32
			if kind == 12 {
				bits = 64
			}
			for _, s := range list {
				v, err := strconv.ParseInt(s, 10, bits)
				if err != nil || strconv.FormatInt(v, 10) != s {
					return 0, fmt.Errorf("NBT: invalid array integer")
				}
				if kind == 11 {
					binary.Write(&out, binary.BigEndian, int32(v))
				} else {
					binary.Write(&out, binary.BigEndian, v)
				}
			}
		}
		return kind, nil
	}
	out.Write([]byte{10, 0, 0})
	kind, err := write(data, 1, false)
	if err != nil {
		return nil, err
	}
	if kind != 10 || out.Len() > MaxExpanded {
		return nil, fmt.Errorf("NBT: root must be a bounded compound")
	}
	var compressed bytes.Buffer
	zip := typedEncoders.Get().(*gzip.Writer)
	zip.Reset(&compressed)
	defer func() { zip.Reset(io.Discard); typedEncoders.Put(zip) }()
	if _, err = zip.Write(out.Bytes()); err != nil {
		return nil, err
	}
	if err = zip.Close(); err != nil {
		return nil, err
	}
	if _, err = Digest(compressed.Bytes()); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func TypedJSON(compressed []byte) (any, error) { return typedJSON(compressed) }
