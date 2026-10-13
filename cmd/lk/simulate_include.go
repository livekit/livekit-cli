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
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// includedFile is a scenarios.yaml `include` target: it only contributes
// nodes, so running one file never runs scenarios it did not list itself.
type includedFile struct {
	Include []string       `yaml:"include"`
	Nodes   map[string]any `yaml:"nodes"`
}

// sourcedNode is a node in YAML form and the file that defines it.
type sourcedNode struct {
	value any
	file  string
}

// nodeResolver merges the nodes of a scenarios file and everything it
// includes. Paths resolve relative to the file that names them, and each file
// loads once by its real path, so a file reached through two includes is not a
// duplicate.
type nodeResolver struct {
	root   string // directory errors name files relative to
	loaded map[string]bool
	chain  []string // real paths from the root file to the one loading now
	nodes  map[string]sourcedNode
}

func resolveNodes(path string, nodes map[string]any, include []string) (map[string]sourcedNode, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	r := &nodeResolver{
		root:   filepath.Dir(real),
		loaded: map[string]bool{real: true},
		chain:  []string{real},
		nodes:  map[string]sourcedNode{},
	}
	if err := r.add(real, nodes); err != nil {
		return nil, err
	}
	if err := r.includeAll(real, include); err != nil {
		return nil, err
	}
	return r.nodes, nil
}

func (r *nodeResolver) includeAll(from string, include []string) error {
	for _, rel := range include {
		path := rel
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(from), rel)
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("%s: include %q: %w", r.name(from), rel, err)
		}
		if slices.Contains(r.chain, real) {
			return fmt.Errorf("include cycle: %s", r.chainNames(append(r.chain, real)))
		}
		if r.loaded[real] {
			continue
		}
		r.loaded[real] = true
		if err := r.load(real); err != nil {
			return err
		}
	}
	return nil
}

func (r *nodeResolver) load(real string) error {
	data, err := os.ReadFile(real)
	if err != nil {
		return fmt.Errorf("%s: %w", r.name(real), err)
	}
	var keys map[string]any
	if err := yaml.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("%s: %w", r.name(real), err)
	}
	for key := range keys {
		if key != "nodes" && key != "include" {
			return fmt.Errorf("%s: an included file holds only `nodes` and `include`, not %q", r.name(real), key)
		}
	}
	var f includedFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("%s: %w", r.name(real), err)
	}
	if err := r.add(real, f.Nodes); err != nil {
		return err
	}
	r.chain = append(r.chain, real)
	defer func() { r.chain = r.chain[:len(r.chain)-1] }()
	return r.includeAll(real, f.Include)
}

func (r *nodeResolver) add(file string, nodes map[string]any) error {
	for name, value := range nodes {
		if prev, ok := r.nodes[name]; ok {
			return fmt.Errorf("node %q is defined in both %s and %s", name, r.name(prev.file), r.name(file))
		}
		r.nodes[name] = sourcedNode{value: value, file: file}
	}
	return nil
}

// name is how errors refer to a file: relative to the root file's directory.
func (r *nodeResolver) name(real string) string {
	if rel, err := filepath.Rel(r.root, real); err == nil {
		return rel
	}
	return real
}

func (r *nodeResolver) chainNames(chain []string) string {
	names := make([]string, len(chain))
	for i, p := range chain {
		names[i] = r.name(p)
	}
	return strings.Join(names, " -> ")
}
