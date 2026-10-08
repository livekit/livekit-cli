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
	"fmt"
	"os"
	"strings"

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
	return applyYAMLEdits(data, edits), len(edits), nil
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
