// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// yamlEdit replaces `remove` bytes at `offset` with `text`.
type yamlEdit struct {
	offset, remove int
	text           string
}

// applyYAMLEdits returns data with edits applied. Edits must be in document
// order and must not overlap.
func applyYAMLEdits(data []byte, edits []yamlEdit) []byte {
	// applying back to front keeps every earlier offset valid
	out := bytes.Clone(data)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = append(out[:e.offset:e.offset], append([]byte(e.text), out[e.offset+e.remove:]...)...)
	}
	return out
}

// yamlSource maps yaml.v3's 1-based line and rune column positions to byte
// offsets in the original document.
type yamlSource struct {
	data       []byte
	lineStarts []int
	newline    string
}

func newYAMLSource(data []byte) yamlSource {
	src := yamlSource{data: data, lineStarts: []int{0}, newline: "\n"}
	for i, b := range data {
		if b == '\n' {
			src.lineStarts = append(src.lineStarts, i+1)
		}
	}
	if bytes.Contains(data, []byte("\r\n")) {
		src.newline = "\r\n"
	}
	return src
}

func (s yamlSource) offset(line, column int) int {
	off := s.lineStarts[line-1]
	for range column - 1 {
		_, size := utf8.DecodeRune(s.data[off:])
		off += size
	}
	return off
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
