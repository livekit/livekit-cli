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
	Include []string             `yaml:"include"`
	Nodes   map[string]yaml.Node `yaml:"nodes"`
}

// sourcedNode is a node in YAML form and the file that defines it, named as
// errors name it.
type sourcedNode struct {
	value yaml.Node
	file  string
}

// nodeResolver merges the nodes of a scenarios file and everything it
// includes. A file's relative includes resolve beside the path it was named
// by, with every directory on that path resolved physically, so `link/..` is
// the parent of where link points. The file itself is identified by
// os.SameFile, so a file reached through two includes, a symlink, two path
// spellings, or two letter cases loads once, beside the name it was first
// reached by.
type nodeResolver struct {
	root   string // directory errors name files relative to
	loaded []os.FileInfo
	chain  []os.FileInfo // files from the root file to the one loading now
	named  []string      // the same files, as named
	nodes  map[string]sourcedNode
}

func resolveNodes(path string, nodes map[string]yaml.Node, include []string) (map[string]sourcedNode, error) {
	named, err := physicalDir(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(named)
	if err != nil {
		return nil, err
	}
	r := &nodeResolver{
		root:   filepath.Dir(named),
		loaded: []os.FileInfo{info},
		chain:  []os.FileInfo{info},
		named:  []string{named},
		nodes:  map[string]sourcedNode{},
	}
	if err := r.add(named, nodes); err != nil {
		return nil, err
	}
	if err := r.includeAll(named, include); err != nil {
		return nil, err
	}
	return r.nodes, nil
}

// physicalDir makes path absolute and resolves every directory on it through
// symlinks before any `..` is applied, keeping the file's own name. Cleaning
// first, as filepath.Abs and filepath.Join do, would make `link/../x.yaml` the
// x.yaml beside link rather than the one in link's target's parent.
func physicalDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = wd + string(filepath.Separator) + path
	}
	i := strings.LastIndexAny(path, "/"+string(filepath.Separator))
	dir, err := filepath.EvalSymlinks(path[:i+1])
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, path[i+1:]), nil
}

func (r *nodeResolver) includeAll(from string, include []string) error {
	for _, rel := range include {
		if rel == "" {
			return fmt.Errorf("%s: include has an empty entry", r.name(from))
		}
		path := rel
		if !filepath.IsAbs(path) {
			path = filepath.Dir(from) + string(filepath.Separator) + rel
		}
		named, err := physicalDir(path)
		if err != nil {
			return fmt.Errorf("%s: include %q: %w", r.name(from), rel, err)
		}
		info, err := os.Stat(named)
		if err != nil {
			return fmt.Errorf("%s: include %q: %w", r.name(from), rel, err)
		}
		if sameFileIn(r.chain, info) {
			return fmt.Errorf("include cycle: %s", r.chainNames(append(slices.Clone(r.named), named)))
		}
		if sameFileIn(r.loaded, info) {
			continue
		}
		if info.IsDir() {
			return fmt.Errorf("%s: include %q is a directory, not a file", r.name(from), rel)
		}
		r.loaded = append(r.loaded, info)
		if err := r.load(named, info); err != nil {
			return err
		}
	}
	return nil
}

func sameFileIn(files []os.FileInfo, info os.FileInfo) bool {
	return slices.ContainsFunc(files, func(f os.FileInfo) bool { return os.SameFile(f, info) })
}

func (r *nodeResolver) load(named string, info os.FileInfo) error {
	data, err := os.ReadFile(named)
	if err != nil {
		return fmt.Errorf("%s: %w", r.name(named), err)
	}
	if err := onlyKeys(data, "an included file", "nodes", "include"); err != nil {
		return fmt.Errorf("%s: %w", r.name(named), err)
	}
	var f includedFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("%s: %w", r.name(named), err)
	}
	if err := r.add(named, f.Nodes); err != nil {
		return err
	}
	r.chain = append(r.chain, info)
	r.named = append(r.named, named)
	defer func() {
		r.chain = r.chain[:len(r.chain)-1]
		r.named = r.named[:len(r.named)-1]
	}()
	return r.includeAll(named, f.Include)
}

func (r *nodeResolver) add(file string, nodes map[string]yaml.Node) error {
	for name, value := range nodes {
		if prev, ok := r.nodes[name]; ok {
			return fmt.Errorf("node %q is defined in both %s and %s", name, prev.file, r.name(file))
		}
		r.nodes[name] = sourcedNode{value: value, file: r.name(file)}
	}
	return nil
}

// anchorKeyPrefix marks a top-level key that only holds YAML anchors, as in
// Docker Compose files: `x-defaults: &machine {speaker: MACHINE}`.
const anchorKeyPrefix = "x-"

// onlyKeys rejects a top-level key other than allowed or an anchor key, so a
// misspelled key fails instead of being ignored.
func onlyKeys(data []byte, what string, allowed ...string) error {
	var keys map[string]yaml.Node // keys only: values are read against the schema later
	if err := yaml.Unmarshal(data, &keys); err != nil {
		return err
	}
	var unknown []string
	for key := range keys {
		if !slices.Contains(allowed, key) && !strings.HasPrefix(key, anchorKeyPrefix) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	quoted := make([]string, len(allowed))
	for i, key := range allowed {
		quoted[i] = "`" + key + "`"
	}
	list := quoted[len(quoted)-1]
	if len(quoted) == 2 {
		list = quoted[0] + " and " + list
	} else if len(quoted) > 2 {
		list = strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + list
	}
	return fmt.Errorf("%s holds only %s, and keys that start with %q for anchors, not %q", what, list, anchorKeyPrefix, unknown[0])
}

// name is how errors refer to a file: relative to the root file's directory,
// with forward slashes as in `include`.
func (r *nodeResolver) name(named string) string {
	if rel, err := filepath.Rel(r.root, named); err == nil {
		return filepath.ToSlash(rel)
	}
	return named
}

func (r *nodeResolver) chainNames(chain []string) string {
	names := make([]string, len(chain))
	for i, p := range chain {
		names[i] = r.name(p)
	}
	return strings.Join(names, " -> ")
}
