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
// and of every scenario that lacks one. Editing the node tree rather than
// re-marshalling keeps the user's comments and key order intact. Returns nil
// output when nothing was added.
func insertScenarioIDs(data []byte) ([]byte, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, 0, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, 0, nil
	}
	root := doc.Content[0]
	added := 0
	if addMissingID(root, utils.ScenarioGroupPrefix) {
		added++
	}
	if scenarios := mappingValue(root, "scenarios"); scenarios != nil && scenarios.Kind == yaml.SequenceNode {
		for _, s := range scenarios.Content {
			if s.Kind == yaml.MappingNode && addMissingID(s, utils.ScenarioPrefix) {
				added++
			}
		}
	}
	if added == 0 {
		return nil, 0, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(detectIndent(data))
	if err := enc.Encode(&doc); err != nil {
		return nil, 0, err
	}
	return out.Bytes(), added, nil
}

// detectIndent is the leading-space width of the first indented line, so the
// rewrite keeps the file's own indentation.
func detectIndent(data []byte) int {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if n := len(line) - len(bytes.TrimLeft(line, " ")); n > 0 && n < len(line) {
			return n
		}
	}
	return 2
}

func addMissingID(m *yaml.Node, prefix string) bool {
	if mappingValue(m, "id") != nil {
		return false
	}
	m.Content = append([]*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "id"},
		{Kind: yaml.ScalarNode, Value: utils.NewGuid(prefix)},
	}, m.Content...)
	return true
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
