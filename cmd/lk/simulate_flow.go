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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// builtinNodePrefix marks the nodes the server provides, such as
// livekit/hangup. Which built-ins exist is for the server to say.
const builtinNodePrefix = "livekit/"

// maxNodeTimeout is the documented limit of SimulatorNode.Timeout.after.
const maxNodeTimeout = 20 * time.Second

// decodeNodes reads the `nodes` of a scenarios.yaml.
func decodeNodes(nodes map[string]yaml.Node) (map[string]*livekit.SimulatorNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string]*livekit.SimulatorNode, len(nodes))
	for _, name := range names {
		if strings.HasPrefix(name, builtinNodePrefix) {
			return nil, fmt.Errorf("node %q: the %s prefix is reserved for built-in nodes", name, builtinNodePrefix)
		}
		value := nodes[name]
		node := &livekit.SimulatorNode{}
		if err := yamlToProto(&value, node); err != nil {
			return nil, fmt.Errorf("node %q (line %d): %w", name, value.Line, err)
		}
		out[name] = node
	}
	return out, nil
}

// jsonPosition is where protojson saw an error in the JSON it was given,
// which is not a place in the user's file.
var jsonPosition = regexp.MustCompile(`\(line \d+:\d+\):\s*`)

// yamlToProto decodes YAML into m through protojson, so field names, enum
// names, and durations follow the server's parser. Every scalar goes in as its
// source text, because every leaf of a SimulatorNode is a string, an enum, or
// a duration: otherwise YAML reads `2026-01-01` as a date and `0123` as an
// octal number.
func yamlToProto(n *yaml.Node, m proto.Message) error {
	v, err := yamlSourceValue(n)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := protojson.Unmarshal(b, m); err != nil {
		return errors.New(jsonPosition.ReplaceAllString(err.Error(), ""))
	}
	return nil
}

func yamlSourceValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, fmt.Errorf("line %d: empty document", n.Line)
		}
		return yamlSourceValue(n.Content[0])
	case yaml.AliasNode:
		return yamlSourceValue(n.Alias)
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if _, ok := out[key.Value]; ok {
				return nil, fmt.Errorf("line %d: duplicate key %q", key.Line, key.Value)
			}
			if isYAMLNull(value) {
				return nil, fmt.Errorf("line %d: %s has no value (quote a value that starts with #)", key.Line, key.Value)
			}
			v, err := yamlSourceValue(value)
			if err != nil {
				return nil, err
			}
			out[key.Value] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			if isYAMLNull(item) {
				return nil, fmt.Errorf("line %d: empty list item", item.Line)
			}
			v, err := yamlSourceValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		return n.Value, nil
	}
	return nil, fmt.Errorf("line %d: unsupported YAML", n.Line)
}

func isYAMLNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// protoToYAML is the inverse of yamlToProto.
func protoToYAML(m proto.Message) (yaml.Node, error) {
	var n yaml.Node
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(m)
	if err != nil {
		return n, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return n, err
	}
	err = n.Encode(v)
	return n, err
}

// checkFlows reports what the server would reject in the nodes a scenario can
// reach, so a bad file fails before the agent starts. Nodes no scenario
// reaches are not checked, as on the server.
func checkFlows(group *livekit.ScenarioGroup) error {
	var errs []error
	var pending []string
	follow := func(where, field, to string) {
		switch {
		case to == "":
			errs = append(errs, fmt.Errorf("%s: %s names no node", where, field))
		case strings.HasPrefix(to, builtinNodePrefix):
			// a built-in: the server knows which exist
		case group.GetNodes()[to] == nil:
			errs = append(errs, fmt.Errorf("%s: %s %q is not a node", where, field, to))
		default:
			pending = append(pending, to)
		}
	}
	for _, s := range group.GetScenarios() {
		if s.GetStart() != "" {
			follow(fmt.Sprintf("scenario %q", s.GetLabel()), "start", s.GetStart())
		}
	}
	checked := map[string]bool{}
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if checked[name] {
			continue
		}
		checked[name] = true
		node := group.GetNodes()[name]
		where := fmt.Sprintf("node %q", name)
		if node.GetThen() != "" {
			follow(where, "then", node.GetThen())
		}
		for i, route := range node.GetRoutes() {
			follow(where, fmt.Sprintf("routes[%d].to", i), route.GetTo())
		}
		if timeout := node.GetTimeout(); timeout != nil {
			follow(where, "timeout.to", timeout.GetTo())
			if after := timeout.GetAfter().AsDuration(); after > maxNodeTimeout {
				errs = append(errs, fmt.Errorf("%s: timeout.after is %s, more than %s", where, after, maxNodeTimeout))
			}
		}
	}
	return errors.Join(errs...)
}
