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

package assistant

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// FolderKind is what the user's current folder holds.
type FolderKind string

const (
	FolderEmpty   FolderKind = "empty"   // nothing in it
	FolderLiveKit FolderKind = "livekit" // a project that uses LiveKit
	FolderApp     FolderKind = "app"     // a project that doesn't use LiveKit yet
	FolderOther   FolderKind = "other"   // no project found
)

// Folder describes the user's current folder, so the assistant can greet them
// with what's in it. It reads only project manifests, such as package.json and
// go.mod, in the folder and its direct subfolders.
type Folder struct {
	Kind     FolderKind `json:"kind"`
	Summary  string     `json:"summary,omitempty"` // spoken, such as "a Go project that uses the LiveKit Go server SDK"
	Projects []Project  `json:"projects,omitempty"`
	// HasLiveKitTOML is true when the folder has a livekit.toml, which a
	// LiveKit Cloud agent deployment uses.
	HasLiveKitTOML bool `json:"has_livekit_toml,omitempty"`
	// HasAgents is true when one or two projects use LiveKit Agents.
	HasAgents bool `json:"has_agents,omitempty"`
}

// Project is one project in the folder.
type Project struct {
	Path      string    `json:"path"` // relative to the folder; "." is the folder itself
	Language  string    `json:"language"`
	Framework string    `json:"framework,omitempty"`
	LiveKit   []Package `json:"livekit_packages,omitempty"`
}

// Package is a LiveKit package that a project depends on.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"` // as the manifest gives it, such as "^2.1.0" or "~=1.8"
}

// usesAgents reports whether p uses LiveKit Agents.
func (p Project) usesAgents() bool {
	return slices.ContainsFunc(p.LiveKit, func(pkg Package) bool {
		return pkg.Name == "livekit-agents" || pkg.Name == "@livekit/agents"
	})
}

// AgentPaths returns the paths of the projects that use LiveKit Agents.
func (f Folder) AgentPaths() []string {
	var paths []string
	for _, p := range f.Projects {
		if p.usesAgents() {
			paths = append(paths, p.Path)
		}
	}
	return paths
}

// hasAgents reports whether one or two projects use LiveKit Agents. With
// more, it isn't clear which agent the user means.
func (f Folder) hasAgents() bool {
	n := 0
	for _, p := range f.Projects {
		if p.usesAgents() {
			n++
		}
	}
	return n > 0 && n <= 2
}

// JSON returns f as JSON for the agent.
func (f Folder) JSON() string {
	b, _ := json.Marshal(f)
	return string(b)
}

// skipDirs are subfolders that never hold the user's own projects.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "venv": true, "env": true, "dist": true,
	"build": true, "target": true, "out": true, "__pycache__": true, "Pods": true,
}

// maxDirs limits how many folders DescribeFolder reads, so a large folder,
// such as the home folder, stays fast.
const maxDirs = 200

// DescribeFolder describes dir: whether it's empty, and the projects in it,
// in its subfolders, and in their subfolders. The second level finds the
// projects in a monorepo, such as apps/web and apps/agent. It doesn't look
// inside a project's own folders.
func DescribeFolder(dir string) Folder {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Folder{Kind: FolderOther}
	}
	empty := true
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			empty = false
			break
		}
	}
	if empty {
		return Folder{Kind: FolderEmpty}
	}

	var f Folder
	root, rootFound := detectProject(dir)
	visited := 0
	var scan func(rel string, entries []os.DirEntry, depth int)
	scan = func(rel string, entries []os.DirEntry, depth int) {
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			if visited++; visited > maxDirs {
				return
			}
			path := filepath.Join(rel, name)
			if p, ok := detectProject(filepath.Join(dir, path)); ok {
				p.Path = filepath.ToSlash(path)
				f.Projects = append(f.Projects, p)
			} else if depth < 2 {
				if sub, err := os.ReadDir(filepath.Join(dir, path)); err == nil {
					scan(path, sub, depth+1)
				}
			}
		}
	}
	scan("", entries, 1)
	// A workspace root, such as a pnpm or npm workspace, only ties its
	// projects together, so it isn't a project of its own.
	if rootFound && !(len(f.Projects) > 0 && isWorkspaceRoot(dir)) {
		root.Path = "."
		f.Projects = append([]Project{root}, f.Projects...)
	}
	if _, err := os.Stat(filepath.Join(dir, "livekit.toml")); err == nil {
		f.HasLiveKitTOML = true
	}

	switch {
	case f.HasLiveKitTOML || slices.ContainsFunc(f.Projects, func(p Project) bool { return len(p.LiveKit) > 0 }):
		f.Kind = FolderLiveKit
	case len(f.Projects) > 0:
		f.Kind = FolderApp
	default:
		f.Kind = FolderOther
	}
	f.Summary = summarize(f.Projects)
	f.HasAgents = f.hasAgents()
	return f
}

// isWorkspaceRoot reports whether dir is the root of a JavaScript or Python
// workspace.
func isWorkspaceRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(b, &pkg) == nil && len(pkg.Workspaces) > 0 {
			return true
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	return strings.Contains(string(b), "[tool.uv.workspace]")
}

// detectProject finds the project in dir from its manifest files.
func detectProject(dir string) (Project, bool) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		return string(b)
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}

	var p Project
	found := false
	if text := read("go.mod"); text != "" {
		found = true
		p.Language = "Go"
		p.LiveKit = append(p.LiveKit, goPackages(text)...)
	}
	if text := read("package.json"); text != "" {
		found = true
		p.Language = "JavaScript"
		if exists("tsconfig.json") {
			p.Language = "TypeScript"
		}
		framework, pkgs := nodePackages(text)
		p.Framework = framework
		p.LiveKit = append(p.LiveKit, pkgs...)
	}
	if text := read("pyproject.toml") + "\n" + read("requirements.txt"); strings.TrimSpace(text) != "" {
		found = true
		if p.Language == "" {
			p.Language = "Python"
		}
		lower := strings.ToLower(text)
		for _, fw := range pythonFrameworks {
			if p.Framework == "" && strings.Contains(lower, fw.dep) {
				p.Framework = fw.name
			}
		}
		p.LiveKit = append(p.LiveKit, pythonPackages(nameLineRe.ReplaceAllString(lower, ""))...)
	}
	if text := read("Cargo.toml"); text != "" {
		found = true
		p.Language = "Rust"
		p.LiveKit = append(p.LiveKit, rustPackages(text)...)
	}
	if text := read("Package.swift"); text != "" {
		found = true
		p.Language = "Swift"
		if m := swiftRe.FindStringSubmatch(text); m != nil {
			p.LiveKit = append(p.LiveKit, Package{Name: "client-sdk-swift", Version: m[1]})
		}
	}
	if text := read("pubspec.yaml"); text != "" {
		found = true
		p.Language = "Dart"
		if strings.Contains(text, "sdk: flutter") {
			p.Framework = "Flutter"
		}
		if m := pubspecRe.FindStringSubmatch(text); m != nil {
			p.LiveKit = append(p.LiveKit, Package{Name: "livekit_client", Version: strings.Trim(m[1], `"'`)})
		}
	}
	for _, name := range []string{"build.gradle.kts", "build.gradle", "app/build.gradle.kts", "app/build.gradle", "gradle/libs.versions.toml"} {
		if text := read(name); text != "" {
			found = true
			p.Language = "Android"
			if m := gradleRe.FindStringSubmatch(text); m != nil && len(p.LiveKit) == 0 {
				p.LiveKit = append(p.LiveKit, Package{Name: "io.livekit:livekit-android", Version: m[1]})
			}
		}
	}
	return p, found
}

// appFrameworks maps package.json dependencies to the frameworks they mean,
// most specific first.
var appFrameworks = []struct{ dep, name string }{
	{"next", "Next.js"},
	{"expo", "Expo"},
	{"react-native", "React Native"},
	{"@sveltejs/kit", "SvelteKit"},
	{"nuxt", "Nuxt"},
	{"@angular/core", "Angular"},
	{"svelte", "Svelte"},
	{"vue", "Vue"},
	{"react", "React"},
	{"express", "Express"},
}

// pythonFrameworks are Python web frameworks found in project files.
var pythonFrameworks = []struct{ dep, name string }{
	{"fastapi", "FastAPI"},
	{"django", "Django"},
	{"flask", "Flask"},
}

func nodePackages(text string) (string, []Package) {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(text), &pkg) != nil {
		return "", nil
	}
	deps := map[string]string{}
	maps.Copy(deps, pkg.DevDependencies)
	maps.Copy(deps, pkg.Dependencies)
	framework := ""
	for _, f := range appFrameworks {
		if _, ok := deps[f.dep]; ok {
			framework = f.name
			break
		}
	}
	var pkgs []Package
	for d, v := range deps {
		if strings.HasPrefix(d, "@livekit/") || strings.HasPrefix(d, "livekit-") {
			pkgs = append(pkgs, Package{Name: d, Version: v})
		}
	}
	slices.SortFunc(pkgs, func(a, b Package) int { return strings.Compare(a.Name, b.Name) })
	return framework, pkgs
}

var (
	goRe     = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(github\.com/livekit/[\w./-]+)\s+(v[\w.+-]+)\s*$`)
	pythonRe = regexp.MustCompile(`(?m)(?:^|["'\s])(livekit(?:-[a-z0-9]+)*)(?:\[[^\]]*\])?\s*((?:[~=<>!]=?|==)\s*[0-9][\w.*]*)?`)
	rustRe   = regexp.MustCompile(`(?m)^\s*(livekit(?:-[a-z]+)*)\s*=\s*(?:"([^"]+)"|\{[^}]*version\s*=\s*"([^"]+)")`)
	swiftRe  = regexp.MustCompile(`livekit/client-sdk-swift[^)]*?(?:from:\s*|exact:\s*)"([^"]+)"`)
	// nameLineRe matches pyproject.toml's name and description, which can
	// mention LiveKit without depending on it.
	nameLineRe = regexp.MustCompile(`(?m)^\s*(?:name|description)\s*=.*$`)
	pubspecRe  = regexp.MustCompile(`(?m)^\s*livekit_client:\s*(\S+)`)
	gradleRe   = regexp.MustCompile(`io\.livekit:livekit-android(?::([\w.-]+))?`)
)

func goPackages(text string) []Package {
	var pkgs []Package
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "// indirect") {
			continue
		}
		if m := goRe.FindStringSubmatch(line); m != nil {
			pkgs = append(pkgs, Package{Name: m[1], Version: m[2]})
		}
	}
	return pkgs
}

func pythonPackages(text string) []Package {
	var pkgs []Package
	seen := map[string]bool{}
	for _, m := range pythonRe.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		pkgs = append(pkgs, Package{Name: m[1], Version: strings.ReplaceAll(m[2], " ", "")})
	}
	return pkgs
}

func rustPackages(text string) []Package {
	var pkgs []Package
	for _, m := range rustRe.FindAllStringSubmatch(text, -1) {
		pkgs = append(pkgs, Package{Name: m[1], Version: m[2] + m[3]})
	}
	return pkgs
}

// packageNames are the spoken names of the main LiveKit packages. Others,
// such as plugins, aren't named in the summary.
var packageNames = map[string]string{
	"livekit-agents":                      "LiveKit Agents",
	"@livekit/agents":                     "LiveKit Agents",
	"livekit-api":                         "the LiveKit server API",
	"livekit-server-sdk":                  "the LiveKit server SDK",
	"livekit-client":                      "the LiveKit client SDK",
	"@livekit/components-react":           "LiveKit React components",
	"@livekit/react-native":               "the LiveKit React Native SDK",
	"github.com/livekit/server-sdk-go":    "the LiveKit Go server SDK",
	"github.com/livekit/server-sdk-go/v2": "the LiveKit Go server SDK",
	"client-sdk-swift":                    "the LiveKit Swift SDK",
	"livekit_client":                      "the LiveKit Flutter SDK",
	"io.livekit:livekit-android":          "the LiveKit Android SDK",
}

// summarize describes projects in a short phrase for speech, such as "a
// Python project in agent that uses LiveKit Agents and a Next.js app in web
// that uses the LiveKit client SDK". It names up to two projects that use
// LiveKit, agents first, and counts the others.
func summarize(projects []Project) string {
	if len(projects) == 0 {
		return ""
	}
	var named, others []Project
	for _, p := range projects {
		if len(p.LiveKit) > 0 {
			named = append(named, p)
		} else {
			others = append(others, p)
		}
	}
	if len(named) == 0 && len(others) <= 2 {
		named, others = others, nil
	}
	if len(named) > 2 {
		return fmt.Sprintf("%d projects, and %d of them use LiveKit", len(projects), len(named))
	}
	if len(named) == 0 {
		return fmt.Sprintf("%d projects", len(projects))
	}
	slices.SortStableFunc(named, func(a, b Project) int {
		switch {
		case a.usesAgents() == b.usesAgents():
			return 0
		case a.usesAgents():
			return -1
		default:
			return 1
		}
	})

	var parts []string
	for _, p := range named {
		parts = append(parts, describe(p))
	}
	s := strings.Join(parts, " and ")
	switch len(others) {
	case 0:
	case 1:
		s += ", and one other project"
	default:
		s += fmt.Sprintf(", and %d other projects", len(others))
	}
	return s
}

// describe describes one project for speech.
func describe(p Project) string {
	what := p.Language + " project"
	if p.Framework != "" {
		what = p.Framework + " app"
	}
	s := article(what) + " " + what
	if p.Path != "." {
		s += " in " + p.Path
	}
	var names []string
	for _, pkg := range p.LiveKit {
		name := packageNames[pkg.Name]
		if name == "" && strings.HasPrefix(pkg.Name, "github.com/livekit/server-sdk-go") {
			name = packageNames["github.com/livekit/server-sdk-go"]
		}
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) > 2 {
		names = names[:2]
	}
	switch {
	case len(names) > 0:
		s += " that uses " + strings.Join(names, " and ")
	case len(p.LiveKit) > 0:
		s += " that uses LiveKit"
	}
	return s
}

func article(word string) string {
	if strings.ContainsRune("AEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}
