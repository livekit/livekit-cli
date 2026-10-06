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
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/utils"
)

// ensureScenarioIDs refuses to run a scenarios file whose group or scenarios
// lack an `id`, offering to insert generated ones in place first. Without a
// stable id nothing correlates a scenario across runs once its text changes.
func ensureScenarioIDs(cmd *cli.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read scenarios file: %w", err)
	}
	fixed, added, err := insertScenarioIDs(data)
	if err != nil {
		return fmt.Errorf("failed to parse scenarios file: %w", err)
	}
	if added == 0 {
		return nil
	}
	if !cmd.Bool("yes") {
		if !isInteractive() {
			return fmt.Errorf("%s is missing %d scenario id(s); re-run with --yes to have them added to the file", path, added)
		}
		confirmed := false
		err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
			Title("Add scenario IDs?").
			Description(fmt.Sprintf(
				"%d entries in %s have no `id`. IDs let LiveKit track a scenario\n"+
					"across runs even as its text changes, so every scenario needs one.",
				added, util.Accented(path),
			)).
			Affirmative("Add IDs").
			Negative("Cancel").
			Value(&confirmed))).
			WithTheme(util.FormTheme()).
			Run()
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted: scenarios must have ids to run")
		}
	}
	if err := os.WriteFile(path, fixed, 0o644); err != nil {
		return fmt.Errorf("failed to write scenarios file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Added %d id(s) to %s\n", added, path)
	return nil
}

// insertScenarioIDs adds a generated `id` as the first key of the document
// and of every scenario that lacks one, and fills empty `id` values. Edits are
// spliced into the original bytes, so everything else in the file (comments,
// blank lines, indentation, scalar styles) is unchanged. Returns nil output
// when nothing was added.
func insertScenarioIDs(data []byte) ([]byte, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, 0, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, 0, nil
	}
	root := doc.Content[0]
	src := newYAMLSource(data)
	var edits []yamlEdit
	if e, ok := missingIDEdit(src, root, utils.ScenarioGroupPrefix); ok {
		edits = append(edits, e)
	}
	if scenarios := mappingValue(root, "scenarios"); scenarios != nil && scenarios.Kind == yaml.SequenceNode {
		for _, s := range scenarios.Content {
			if s.Kind != yaml.MappingNode {
				continue
			}
			if e, ok := missingIDEdit(src, s, utils.ScenarioPrefix); ok {
				edits = append(edits, e)
			}
		}
	}
	if len(edits) == 0 {
		return nil, 0, nil
	}
	// edits are collected in document order; applying them back to front
	// keeps every earlier offset valid
	out := bytes.Clone(data)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = append(out[:e.offset:e.offset], append([]byte(e.text), out[e.offset+e.remove:]...)...)
	}
	return out, len(edits), nil
}

// yamlEdit replaces `remove` bytes at `offset` with `text`.
type yamlEdit struct {
	offset, remove int
	text           string
}

// missingIDEdit returns the edit that gives mapping m an id: filling an empty
// `id` value where it stands, or else inserting `id` before the first key, at
// that key's position, so comments above or beside the first key stay put.
func missingIDEdit(src yamlSource, m *yaml.Node, prefix string) (yamlEdit, bool) {
	id := utils.NewGuid(prefix)
	if v := mappingValue(m, "id"); v != nil {
		if v.Value != "" && v.Tag != "!!null" {
			return yamlEdit{}, false
		}
		off := src.offset(v.Line, v.Column)
		switch {
		case v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0:
			return yamlEdit{offset: off, remove: 2, text: id}, true // "" or ''
		case v.Value != "":
			return yamlEdit{offset: off, remove: len(v.Value), text: id}, true // ~ or null
		case off > 0 && (src.data[off-1] == ' ' || src.data[off-1] == '\t'):
			return yamlEdit{offset: off, text: id}, true
		default:
			return yamlEdit{offset: off, text: " " + id}, true // bare `id:`
		}
	}
	if len(m.Content) == 0 {
		return yamlEdit{}, false
	}
	first := m.Content[0]
	off := src.offset(first.Line, first.Column)
	if m.Style&yaml.FlowStyle != 0 {
		return yamlEdit{offset: off, text: "id: " + id + ", "}, true
	}
	indent := strings.Repeat(" ", first.Column-1)
	return yamlEdit{offset: off, text: "id: " + id + src.newline + indent}, true
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
