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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Lock files are shared with the `skills` CLI and `gh skill`:
//
//   - project: ./skills-lock.json (version 1), meant to be committed. Entries
//     carry computedHash (ContentHash) and no timestamps, to merge cleanly.
//   - global: ~/.agents/.skill-lock.json (version 3), or
//     $XDG_STATE_HOME/skills/.skill-lock.json. Entries carry skillFolderHash
//     (TreeHash) and timestamps.
//
// Only entries for LiveKit's skills are ever rewritten; everything else in the
// file, including other tools' top-level fields, is preserved as-is.
const (
	projectLockName    = "skills-lock.json"
	projectLockVersion = 1
	globalLockName     = ".skill-lock.json"
	globalLockVersion  = 3
)

// LockEntry is one skill's record. Fields are the union of both lock formats.
type LockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl,omitempty"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	ComputedHash    string `json:"computedHash,omitempty"`
	SkillFolderHash string `json:"skillFolderHash,omitempty"`
	InstalledAt     string `json:"installedAt,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

// FromLiveKit reports whether the entry was installed from LiveKit's repo, by
// any tool.
func (e *LockEntry) FromLiveKit() bool { return e.Source == SourceRepo }

// Lock is a lock file loaded for editing.
type Lock struct {
	Path  string
	scope Scope
	// top holds every top-level field; skills holds each skill entry verbatim.
	top    map[string]json.RawMessage
	skills map[string]json.RawMessage
}

func lockPath(env *Env, scope Scope) string {
	if scope == ScopeGlobal {
		if state := env.getenv("XDG_STATE_HOME"); state != "" {
			return filepath.Join(state, "skills", globalLockName)
		}
		return filepath.Join(env.Home, ".agents", globalLockName)
	}
	return filepath.Join(env.Root, projectLockName)
}

// LoadLock reads the lock file for scope. A missing file is an empty lock; a
// file that isn't valid JSON is an error rather than something to overwrite.
func LoadLock(env *Env, scope Scope) (*Lock, error) {
	l := &Lock{Path: lockPath(env, scope), scope: scope, top: map[string]json.RawMessage{}, skills: map[string]json.RawMessage{}}
	data, err := os.ReadFile(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &l.top); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", l.Path, err)
	}
	if raw, ok := l.top["skills"]; ok {
		if err := json.Unmarshal(raw, &l.skills); err != nil {
			return nil, fmt.Errorf("%s: invalid skills: %w", l.Path, err)
		}
	}
	return l, nil
}

// Get returns the entry for name, if any.
func (l *Lock) Get(name string) (*LockEntry, bool) {
	raw, ok := l.skills[name]
	if !ok {
		return nil, false
	}
	var e LockEntry
	if json.Unmarshal(raw, &e) != nil {
		return nil, false
	}
	return &e, true
}

// LiveKitSkills lists the names of entries installed from LiveKit's repo.
func (l *Lock) LiveKitSkills() []string {
	var names []string
	for name := range l.skills {
		if e, ok := l.Get(name); ok && e.FromLiveKit() {
			names = append(names, name)
		}
	}
	return names
}

// Hash is the recorded hash for name in this lock's format.
func (l *Lock) Hash(name string) string {
	e, ok := l.Get(name)
	if !ok {
		return ""
	}
	if l.scope == ScopeGlobal {
		return e.SkillFolderHash
	}
	return e.ComputedHash
}

// Record writes the entry for a skill installed from bundle.
func (l *Lock) Record(s *Skill, ref string, now time.Time) {
	e := LockEntry{
		Source:     SourceRepo,
		SourceType: "github",
		SkillPath:  s.SkillPath(),
	}
	if ref != DefaultRef {
		e.Ref = ref
	}
	if l.scope == ScopeGlobal {
		// The global format requires sourceUrl; the project one omits it for
		// GitHub sources.
		e.SourceURL = sourceURL()
		e.SkillFolderHash = TreeHash(s.Files)
		ts := now.UTC().Format(time.RFC3339Nano)
		e.InstalledAt, e.UpdatedAt = ts, ts
		if prev, ok := l.Get(s.Name); ok && prev.InstalledAt != "" {
			e.InstalledAt = prev.InstalledAt
		}
	} else {
		e.ComputedHash = ContentHash(s.Files)
	}
	raw, _ := json.Marshal(e)
	l.skills[s.Name] = raw
}

// Remove drops the entry for name.
func (l *Lock) Remove(name string) { delete(l.skills, name) }

// Save writes the lock file, or deletes a project lock that has become empty
// and was only ever lk's.
func (l *Lock) Save() error {
	version := projectLockVersion
	if l.scope == ScopeGlobal {
		version = globalLockVersion
	}
	var existing int
	if raw, ok := l.top["version"]; ok && json.Unmarshal(raw, &existing) == nil && existing > version {
		version = existing
	}
	if l.scope == ScopeProject && len(l.skills) == 0 && len(l.top) <= 2 {
		if err := os.Remove(l.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	l.top["version"], _ = json.Marshal(version)
	l.top["skills"], _ = json.Marshal(l.skills)
	data, err := marshalLock(l.top)
	if err != nil {
		return err
	}
	return writeFileAtomic(l.Path, data, 0o644)
}

// marshalLock writes version and skills first, as the `skills` CLI does, then
// any other fields; keys within are sorted, keeping the file deterministic.
func marshalLock(top map[string]json.RawMessage) ([]byte, error) {
	keys := []string{"version", "skills"}
	var rest []string
	for k := range top {
		if k != "version" && k != "skills" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	var b bytes.Buffer
	b.WriteString("{")
	for i, k := range append(keys, rest...) {
		if i > 0 {
			b.WriteString(",")
		}
		var v bytes.Buffer
		if err := json.Indent(&v, top[k], "  ", "  "); err != nil {
			return nil, err
		}
		name, _ := json.Marshal(k)
		fmt.Fprintf(&b, "\n  %s: %s", name, v.Bytes())
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

// writeFileAtomic replaces path via a temp file in the same directory, so a
// crash never leaves a half-written file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
