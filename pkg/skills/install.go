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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// State is how an installed copy of a skill compares to upstream.
type State string

const (
	StateMissing  State = "missing"  // not installed in this directory
	StateCurrent  State = "current"  // matches upstream
	StateOutdated State = "outdated" // unchanged since install; upstream has moved on
	StateModified State = "modified" // differs from both upstream and what was installed
	StateRemoved  State = "removed"  // installed from LiveKit, but no longer published
	// StateUntracked is a LiveKit skill (by its frontmatter) that no lock file
	// records, e.g. one committed to a starter template. It can't be told
	// apart from an edited copy, so replacing it asks first.
	StateUntracked State = "untracked"
)

// Copy is one skill in one skills directory.
type Copy struct {
	Skill    string
	Dir      SkillsDir
	State    State
	Upstream *Skill // nil when the skill is no longer published
	// Version is the installed copy's metadata.version.
	Version string
}

// Path is the skill's directory.
func (c *Copy) Path() string { return filepath.Join(c.Dir.Path, c.Skill) }

// Installer compares and applies skills for one scope.
type Installer struct {
	Env    *Env
	Scope  Scope
	Bundle *Bundle
	Lock   *Lock
	Now    func() time.Time
}

// NewInstaller loads the scope's lock file. bundle may be nil for operations
// that don't need upstream (remove).
func NewInstaller(env *Env, scope Scope, bundle *Bundle) (*Installer, error) {
	lock, err := LoadLock(env, scope)
	if err != nil {
		return nil, err
	}
	return &Installer{Env: env, Scope: scope, Bundle: bundle, Lock: lock, Now: time.Now}, nil
}

func (in *Installer) hash(files []File) string {
	if in.Scope == ScopeGlobal {
		return TreeHash(files)
	}
	return ContentHash(files)
}

// Inspect reports the state of each named skill in each directory.
func (in *Installer) Inspect(names []string, dirs []SkillsDir) ([]Copy, error) {
	var copies []Copy
	for _, name := range names {
		var up *Skill
		if in.Bundle != nil {
			up = in.Bundle.Find(name)
		}
		for _, d := range dirs {
			c, err := in.inspect(name, up, d)
			if err != nil {
				return nil, err
			}
			copies = append(copies, c)
		}
	}
	return copies, nil
}

func (in *Installer) inspect(name string, up *Skill, d SkillsDir) (Copy, error) {
	c := Copy{Skill: name, Dir: d, Upstream: up, State: StateMissing}
	// Stat follows symlinks, so a dangling link counts as missing (and Write
	// replaces it).
	if _, err := os.Stat(c.Path()); errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	var modes []File
	if up != nil {
		modes = up.Files
	}
	files, err := readDir(c.Path(), modes)
	if err != nil {
		return c, fmt.Errorf("reading %s: %w", c.Path(), err)
	}
	livekit := false
	for _, f := range files {
		if f.Path == "SKILL.md" {
			if fm, err := ParseFrontmatter(f.Data); err == nil {
				c.Version = fm.version()
				livekit = fm.Metadata["author"] == "livekit" && fm.Name == name
			}
		}
	}
	have := in.hash(files)
	recorded := in.Lock.Hash(name)
	switch {
	case up != nil && have == in.hash(up.Files):
		c.State = StateCurrent
	case recorded == "" && livekit:
		c.State = StateUntracked
	case up == nil && have == recorded:
		c.State = StateRemoved
	case up != nil && have == recorded:
		c.State = StateOutdated
	default:
		c.State = StateModified
	}
	return c, nil
}

// Orphans lists LiveKit skills installed in dirs that upstream no longer
// publishes (removed or renamed).
func (in *Installer) Orphans(dirs []SkillsDir) ([]string, error) {
	installed, err := in.InstalledLiveKitSkills(dirs)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range installed {
		if in.Bundle != nil && in.Bundle.Find(name) == nil {
			out = append(out, name)
		}
	}
	return out, nil
}

// Write installs (or overwrites) the upstream skill into c's directory.
func (in *Installer) Write(c Copy) error {
	if c.Upstream == nil {
		return fmt.Errorf("%s is not published", c.Skill)
	}
	if err := os.MkdirAll(c.Dir.Path, 0o755); err != nil {
		return err
	}
	// Stage next to the destination, then swap, so an interrupted install
	// never leaves a half-written skill for an agent to load.
	tmp, err := os.MkdirTemp(c.Dir.Path, "."+c.Skill+".lk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, f := range c.Upstream.Files {
		p := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.Data, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	// RemoveAll on a symlink (as `npx skills` creates) removes only the link.
	if err := os.RemoveAll(c.Path()); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path())
}

// Delete removes c's directory.
func (in *Installer) Delete(c Copy) error {
	return os.RemoveAll(c.Path())
}

// Record notes an installed skill in the lock.
func (in *Installer) Record(s *Skill) {
	in.Lock.Record(s, in.Bundle.Ref, in.Now())
}

// Forget drops name from the lock unless a copy remains in one of the scope's
// known directories.
func (in *Installer) Forget(name string) error {
	copies, err := in.Inspect([]string{name}, GroupSkillsDirs(in.Env, in.Scope, Agents))
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(copies, func(c Copy) bool { return c.State != StateMissing }) {
		in.Lock.Remove(name)
	}
	return nil
}

// Save writes the lock file.
func (in *Installer) Save() error { return in.Lock.Save() }

// InstalledLiveKitSkills finds LiveKit skills present in dirs: those the lock
// attributes to LiveKit, those upstream publishes, and any SKILL.md whose
// frontmatter names LiveKit as its author.
func (in *Installer) InstalledLiveKitSkills(dirs []SkillsDir) ([]string, error) {
	seen := map[string]bool{}
	for _, n := range in.Lock.LiveKitSkills() {
		seen[n] = true
	}
	if in.Bundle != nil {
		for _, n := range in.Bundle.Names() {
			seen[n] = true
		}
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(d.Path, e.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			if fm, err := ParseFrontmatter(data); err == nil && fm.Metadata["author"] == "livekit" && fm.Name == e.Name() {
				seen[e.Name()] = true
			}
		}
	}
	var names []string
	for n := range seen {
		copies, err := in.Inspect([]string{n}, dirs)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(copies, func(c Copy) bool { return c.State != StateMissing }) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}
