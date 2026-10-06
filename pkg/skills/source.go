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

package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SourceRepo is the GitHub repo LiveKit's skills are published from.
	SourceRepo = "livekit/agent-skills"
	// DefaultRef is the branch installs track. Skills are written to stay
	// valid as the SDKs evolve, so the tip of main is what everyone gets.
	DefaultRef = "main"

	skillsRoot = "skills"

	// Guards against a runaway download. The whole repo is well under 1 MiB.
	maxArchiveBytes = 32 << 20
	maxFileBytes    = 8 << 20
)

// ArchiveURL is the tarball for a ref. It is a variable so tests can serve
// their own archive.
var ArchiveURL = func(ref string) string {
	return "https://codeload.github.com/" + SourceRepo + "/tar.gz/" + ref
}

func sourceURL() string { return "https://github.com/" + SourceRepo + ".git" }

// File is one file of a skill, relative to the skill's directory.
type File struct {
	Path string // slash-separated
	Mode int64  // 0o644 or 0o755
	Data []byte
}

// Skill is one skill as published upstream.
type Skill struct {
	Name        string
	Description string
	Files       []File // sorted by Path
}

// SkillPath is the SKILL.md path inside the source repo, as recorded in lock
// files.
func (s *Skill) SkillPath() string { return skillsRoot + "/" + s.Name + "/SKILL.md" }

// Bundle is every skill at one commit of the source repo.
type Bundle struct {
	Ref    string
	Commit string // full SHA, when the archive carries it
	Skills []*Skill
	// Skipped explains skill directories that failed validation, so one bad
	// skill upstream doesn't block installing the rest.
	Skipped []string
}

// Find returns the named skill, or nil.
func (b *Bundle) Find(name string) *Skill {
	for _, s := range b.Skills {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Names lists the bundle's skill names.
func (b *Bundle) Names() []string {
	names := make([]string, len(b.Skills))
	for i, s := range b.Skills {
		names[i] = s.Name
	}
	return names
}

// Fetch downloads the source repo at ref and parses every skill in it.
func Fetch(ctx context.Context, client *http.Client, ref string) (*Bundle, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ArchiveURL(ref), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading skills from %s: %w", SourceRepo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("ref %q not found in %s", ref, SourceRepo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading skills from %s: %s", SourceRepo, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading skills from %s: %w", SourceRepo, err)
	}
	if len(data) > maxArchiveBytes {
		return nil, errors.New("skills archive is unexpectedly large")
	}
	b, err := ParseArchive(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b.Ref = ref
	return b, nil
}

// ParseArchive reads a GitHub source tarball (one top-level directory holding
// the repo) and returns the skills under skills/<name>/.
func ParseArchive(r io.Reader) (*Bundle, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("reading skills archive: %w", err)
	}
	tr := tar.NewReader(gz)
	b := &Bundle{}
	files := map[string][]File{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading skills archive: %w", err)
		}
		// GitHub archives record the commit in the global header's comment.
		if c := hdr.PAXRecords["comment"]; c != "" && b.Commit == "" {
			b.Commit = c
		}
		// Only regular files are installed. Symlinks and anything else are
		// skipped so an entry can never point outside the skill directory.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name, rel, ok := skillEntry(hdr.Name)
		if !ok {
			continue
		}
		if hdr.Size > maxFileBytes {
			return nil, fmt.Errorf("skills archive: skill %s: %s is too large", name, rel)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading skills archive: %w", err)
		}
		mode := int64(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		files[name] = append(files[name], File{Path: rel, Mode: mode, Data: data})
	}

	for name, fs := range files {
		s, err := newSkill(name, fs)
		if err != nil {
			b.Skipped = append(b.Skipped, err.Error())
			continue
		}
		if s != nil {
			b.Skills = append(b.Skills, s)
		}
	}
	sort.Slice(b.Skills, func(i, j int) bool { return b.Skills[i].Name < b.Skills[j].Name })
	sort.Strings(b.Skipped)
	if len(b.Skills) == 0 {
		return nil, fmt.Errorf("no skills found in %s", SourceRepo)
	}
	return b, nil
}

// skillEntry maps an archive path like "repo-main/skills/foo/references/a.md"
// to ("foo", "references/a.md").
func skillEntry(name string) (skill, rel string, ok bool) {
	parts := strings.Split(path.Clean(name), "/")
	// top-level dir, "skills", skill name, then at least one path element
	if len(parts) < 4 || parts[1] != skillsRoot {
		return "", "", false
	}
	for _, p := range parts[2:] {
		if p == ".." || p == "." || p == "" {
			return "", "", false
		}
	}
	return parts[2], strings.Join(parts[3:], "/"), true
}

// newSkill validates a skill's files. Directories without a SKILL.md are not
// skills and are ignored.
func newSkill(dir string, files []File) (*Skill, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var manifest []byte
	for _, f := range files {
		if f.Path == "SKILL.md" {
			manifest = f.Data
		}
	}
	if manifest == nil {
		return nil, nil
	}
	fm, err := ParseFrontmatter(manifest)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", dir, err)
	}
	if fm.Name != dir {
		return nil, fmt.Errorf("skill %s: name %q does not match its directory", dir, fm.Name)
	}
	if len(fm.Name) > 64 || !validName.MatchString(fm.Name) {
		return nil, fmt.Errorf("skill %s: invalid name", dir)
	}
	return &Skill{
		Name:        fm.Name,
		Description: fm.Description,
		Files:       files,
	}, nil
}

// validName is the Agent Skills naming rule: lowercase letters, digits and
// single hyphens, 1-64 characters.
var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9]|-[a-z0-9]){0,63}$`)

// Frontmatter is the subset of SKILL.md frontmatter lk reads.
type Frontmatter struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Metadata    map[string]any `yaml:"metadata"`
}

// ParseFrontmatter reads the YAML block at the top of a SKILL.md.
func ParseFrontmatter(data []byte) (*Frontmatter, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, errors.New("SKILL.md has no frontmatter")
	}
	body, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return nil, errors.New("SKILL.md frontmatter is not terminated")
	}
	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(body), &fm); err != nil {
		return nil, fmt.Errorf("SKILL.md frontmatter: %w", err)
	}
	if fm.Name == "" {
		return nil, errors.New("SKILL.md frontmatter has no name")
	}
	return &fm, nil
}
