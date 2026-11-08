// Package nbt defines the bounded, typed NBT identity shared with the OC bridge.
package nbt

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"sort"
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
