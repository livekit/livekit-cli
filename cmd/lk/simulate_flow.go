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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// builtinNodePrefix marks the nodes the server provides, such as
// livekit/hangup. Which built-ins exist is for the server to say.
const builtinNodePrefix = "livekit/"

// maxNodeTimeout is the documented limit of SimulatorNode.Timeout.after.
const maxNodeTimeout = 20 * time.Second

// decodeNode reads one entry of a scenarios.yaml's `nodes`.
func decodeNode(name string, value *yaml.Node) (*livekit.SimulatorNode, error) {
	if strings.HasPrefix(name, builtinNodePrefix) {
		return nil, fmt.Errorf("node %q: the %s prefix is reserved for built-in nodes", name, builtinNodePrefix)
	}
	node := &livekit.SimulatorNode{}
	if err := yamlToProto(value, node); err != nil {
		return nil, fmt.Errorf("node %q: %w", name, err)
	}
	return node, nil
}

// jsonPosition is where protojson saw an error in the JSON it was given,
// which is not a place in the user's file.
var jsonPosition = regexp.MustCompile(`\(line \d+:\d+\):\s*`)

// yamlToProto decodes YAML into m through protojson, so field names, enum
// names, and durations follow the server's parser. A scalar goes in as its
// source text, except an integer for an enum field: otherwise YAML reads
// `2026-01-01` as a date and `0123` as an octal number. Errors name the line
// in the YAML.
func yamlToProto(n *yaml.Node, m proto.Message) error {
	d := yamlDecoder{open: map[yamlPlace]bool{}}
	v, err := d.message(n, m.ProtoReflect().Descriptor())
	if err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := protojson.Unmarshal(b.Bytes(), m); err != nil {
		return fmt.Errorf("line %d: %s", n.Line, jsonPosition.ReplaceAllString(err.Error(), ""))
	}
	return nil
}

// yamlDecoder turns YAML into the JSON form of a message.
type yamlDecoder struct {
	// the mappings and sequences being read, so an anchor that contains itself
	// fails instead of recursing forever; keyed by place, since a node can be
	// a copy of the one an alias points to
	open map[yamlPlace]bool
}

type yamlPlace struct{ line, column int }

func placeOf(n *yaml.Node) yamlPlace { return yamlPlace{n.Line, n.Column} }

func (d *yamlDecoder) message(n *yaml.Node, md protoreflect.MessageDescriptor) (any, error) {
	n, err := d.resolve(n)
	if err != nil {
		return nil, err
	}
	if isYAMLNull(n) {
		return nil, fmt.Errorf("line %d: no fields", n.Line)
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	if err := d.enter(n); err != nil {
		return nil, err
	}
	defer d.leave(n)
	pairs, err := d.pairs(n)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		fd := md.Fields().ByJSONName(key.Value)
		if fd == nil {
			fd = md.Fields().ByTextName(key.Value)
		}
		if fd == nil {
			return nil, fmt.Errorf("line %d: unknown field %q", key.Line, key.Value)
		}
		v, err := d.field(key, value, fd)
		if err != nil {
			return nil, err
		}
		out[key.Value] = v
	}
	return out, nil
}

func (d *yamlDecoder) field(key, n *yaml.Node, fd protoreflect.FieldDescriptor) (any, error) {
	n, err := d.resolve(n)
	if err != nil {
		return nil, err
	}
	if isYAMLNull(n) {
		return nil, fmt.Errorf("line %d: %s has no value (quote a value that starts with #)", key.Line, key.Value)
	}
	if !fd.IsList() {
		return d.single(n, fd)
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: %s takes a list", n.Line, key.Value)
	}
	if err := d.enter(n); err != nil {
		return nil, err
	}
	defer d.leave(n)
	out := make([]any, 0, len(n.Content))
	for _, item := range n.Content {
		item, err := d.resolve(item)
		if err != nil {
			return nil, err
		}
		if isYAMLNull(item) {
			return nil, fmt.Errorf("line %d: empty item in %s", item.Line, key.Value)
		}
		v, err := d.single(item, fd)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// single is one value of fd: a nested message, or a scalar.
func (d *yamlDecoder) single(n *yaml.Node, fd protoreflect.FieldDescriptor) (any, error) {
	if fd.Kind() == protoreflect.MessageKind && n.Kind == yaml.MappingNode {
		return d.message(n, fd.Message())
	}
	// every other value is a scalar, a duration included
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("line %d: %s takes a single value", n.Line, fd.TextName())
	}
	if n.Style&yaml.TaggedStyle != 0 && n.ShortTag() != "!!str" && n.ShortTag() != "!!int" {
		return nil, fmt.Errorf("line %d: the %s tag is not supported", n.Line, n.ShortTag())
	}
	if fd.Kind() == protoreflect.EnumKind && n.ShortTag() == "!!int" {
		return json.Number(n.Value), nil // protojson takes an enum's number as well as its name
	}
	return n.Value, nil
}

// pairs is a mapping's keys and values, with `<<` merge keys expanded. A key
// the mapping sets itself wins over a merged one, and an earlier merge wins
// over a later one, as YAML defines.
func (d *yamlDecoder) pairs(n *yaml.Node) ([][2]*yaml.Node, error) {
	var own, merged [][2]*yaml.Node
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key.ShortTag() == "!!merge" {
			sources, err := d.mergeSources(value)
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				if err := d.enter(source); err != nil {
					return nil, err
				}
				inner, err := d.pairs(source)
				d.leave(source)
				if err != nil {
					return nil, err
				}
				merged = append(merged, inner...)
			}
			continue
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("line %d: duplicate key %q", key.Line, key.Value)
		}
		seen[key.Value] = true
		own = append(own, [2]*yaml.Node{key, value})
	}
	for _, pair := range merged {
		if !seen[pair[0].Value] {
			seen[pair[0].Value] = true
			own = append(own, pair)
		}
	}
	return own, nil
}

func (d *yamlDecoder) mergeSources(n *yaml.Node) ([]*yaml.Node, error) {
	n, err := d.resolve(n)
	if err != nil {
		return nil, err
	}
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	sources := make([]*yaml.Node, 0, len(items))
	for _, item := range items {
		item, err := d.resolve(item)
		if err != nil {
			return nil, err
		}
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("line %d: `<<` merges only mappings", item.Line)
		}
		sources = append(sources, item)
	}
	return sources, nil
}

// resolve follows aliases and document wrappers to the node they stand for.
func (d *yamlDecoder) resolve(n *yaml.Node) (*yaml.Node, error) {
	for {
		switch n.Kind {
		case yaml.AliasNode:
			n = n.Alias
		case yaml.DocumentNode:
			if len(n.Content) == 0 {
				return nil, fmt.Errorf("line %d: empty document", n.Line)
			}
			n = n.Content[0]
		default:
			return n, nil
		}
	}
}

func (d *yamlDecoder) enter(n *yaml.Node) error {
	if d.open[placeOf(n)] {
		return fmt.Errorf("line %d: anchor %q contains itself", n.Line, n.Anchor)
	}
	d.open[placeOf(n)] = true
	return nil
}

func (d *yamlDecoder) leave(n *yaml.Node) {
	delete(d.open, placeOf(n))
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
			switch after := timeout.GetAfter().AsDuration(); {
			case after <= 0:
				errs = append(errs, fmt.Errorf("%s: timeout.after must be more than 0s", where))
			case after > maxNodeTimeout:
				errs = append(errs, fmt.Errorf("%s: timeout.after is %s, more than %s", where, after, maxNodeTimeout))
			}
		}
	}
	return errors.Join(errs...)
}
