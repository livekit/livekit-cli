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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillMD(name, version string) string {
	return "---\nname: " + name + "\ndescription: " + name + " skill\nlicense: MIT\nmetadata:\n  author: livekit\n  version: \"" + version + "\"\n---\n\n# " + name + "\n"
}

type entry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	linkname string
}

// archive builds a GitHub-style source tarball: a pax global header carrying
// the commit, then everything under one top-level directory.
func archive(t *testing.T, commit string, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag:   tar.TypeXGlobalHeader,
		Name:       "pax_global_header",
		PAXRecords: map[string]string{"comment": commit},
	}))
	for _, e := range entries {
		hdr := &tar.Header{Name: "agent-skills-main/" + e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.linkname}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.Typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if hdr.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestParseArchive(t *testing.T) {
	data := archive(t, "abc123",
		entry{name: "README.md", body: "readme"},
		entry{name: "skills/alpha/SKILL.md", body: skillMD("alpha", "1.2.0")},
		entry{name: "skills/alpha/references/guide.md", body: "guide"},
		entry{name: "skills/alpha/scripts/run.py", body: "print()", mode: 0o755},
		entry{name: "skills/alpha/link", typeflag: tar.TypeSymlink, linkname: "../../../etc/passwd"},
		entry{name: "skills/beta/SKILL.md", body: skillMD("not-beta", "1.0.0")},
		entry{name: "skills/notes/README.md", body: "not a skill"},
	)
	b, err := ParseArchive(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "abc123", b.Commit)
	assert.Equal(t, []string{"alpha"}, b.Names())
	require.Len(t, b.Skipped, 1)
	assert.Contains(t, b.Skipped[0], "does not match its directory")

	alpha := b.Find("alpha")
	assert.Equal(t, "1.2.0", alpha.Version)
	assert.Equal(t, "alpha skill", alpha.Description)
	var paths []string
	for _, f := range alpha.Files {
		paths = append(paths, f.Path)
		if f.Path == "scripts/run.py" {
			assert.Equal(t, int64(0o755), f.Mode)
		}
	}
	// The symlink is dropped.
	assert.Equal(t, []string{"SKILL.md", "references/guide.md", "scripts/run.py"}, paths)
}

func TestParseArchiveNoSkills(t *testing.T) {
	_, err := ParseArchive(bytes.NewReader(archive(t, "abc", entry{name: "README.md", body: "x"})))
	require.Error(t, err)
}

func TestSkillEntryRejectsTraversal(t *testing.T) {
	for _, name := range []string{"repo/skills/../../x", "repo/skills/a/../../b", "repo/other/a/SKILL.md", "repo/skills/a"} {
		_, _, ok := skillEntry(name)
		assert.False(t, ok, name)
	}
	skill, rel, ok := skillEntry("repo/skills/a/references/b.md")
	require.True(t, ok)
	assert.Equal(t, "a", skill)
	assert.Equal(t, "references/b.md", rel)
}

func TestParseFrontmatter(t *testing.T) {
	fm, err := ParseFrontmatter([]byte("---\r\nname: x\r\ndescription: 'a: b'\r\nmetadata:\r\n  version: 1.5\r\n---\r\nbody"))
	require.NoError(t, err)
	assert.Equal(t, "x", fm.Name)
	assert.Equal(t, "a: b", fm.Description)
	assert.Equal(t, "1.5", fm.version())

	for _, bad := range []string{"no frontmatter", "---\nname: x\n", "---\ndescription: d\n---\n"} {
		_, err := ParseFrontmatter([]byte(bad))
		assert.Error(t, err, bad)
	}
}

// vectorFiles matches a directory hashed by `npx skills` (Node's
// localeCompare ordering) and by `git write-tree`, to pin both hashes.
var vectorFiles = []File{
	{Path: "SKILL.md", Mode: 0o644, Data: []byte("---\nname: demo\ndescription: d\n---\nbody\n")},
	{Path: "Zeta.md", Mode: 0o644, Data: []byte("z\n")},
	{Path: "_notes.md", Mode: 0o644, Data: []byte("n\n")},
	{Path: "references/A.md", Mode: 0o644, Data: []byte("a\n")},
	{Path: "references/b.md", Mode: 0o644, Data: []byte("b\n")},
	{Path: "scripts/x-y.py", Mode: 0o755, Data: []byte("y\n")},
	{Path: "scripts/x_y.py", Mode: 0o644, Data: []byte("x\n")},
}

func TestContentHashMatchesSkillsCLI(t *testing.T) {
	assert.Equal(t, "c8ccdac8ece3a90a8608cee2935cc3c732d5d899d5d854dcfce49a4f14cbd2cd", ContentHash(vectorFiles))
}

func TestTreeHashMatchesGit(t *testing.T) {
	assert.Equal(t, "22991c4b92e2021a51e57b4ebc50edceb5e704c1", TreeHash(vectorFiles))
}

func TestReadDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, f := range vectorFiles {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, f.Data, os.FileMode(f.Mode)))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("x"), 0o644))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	files, err := readDir(link, vectorFiles)
	require.NoError(t, err)
	assert.Equal(t, ContentHash(vectorFiles), ContentHash(files))
	assert.Equal(t, TreeHash(vectorFiles), TreeHash(files))
}

func testEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{
		Home:     t.TempDir(),
		Root:     t.TempDir(),
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

func TestDetectAgents(t *testing.T) {
	env := testEnv(t)
	assert.Empty(t, DetectAgents(env))

	require.NoError(t, os.MkdirAll(filepath.Join(env.Home, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(env.Home, ".config", "opencode"), 0o755))
	env.LookPath = func(bin string) (string, error) {
		if bin == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	assert.Equal(t, []string{"claude-code", "codex", "opencode"}, agentIDs(DetectAgents(env)))
}

func agentIDs(agents []*Agent) []string {
	var ids []string
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestGroupSkillsDirsSharesAgentsDir(t *testing.T) {
	env := testEnv(t)
	dirs := GroupSkillsDirs(env, ScopeProject, []*Agent{FindAgent("claude-code"), FindAgent("codex"), FindAgent("cursor")})
	require.Len(t, dirs, 2)
	assert.Equal(t, filepath.Join(env.Root, ".claude", "skills"), dirs[0].Path)
	assert.Equal(t, filepath.Join(env.Root, ".agents", "skills"), dirs[1].Path)
	assert.Equal(t, []string{"codex", "cursor"}, agentIDs(dirs[1].Agents))

	env.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/custom/claude"
		}
		return ""
	}
	assert.Equal(t, filepath.Join("/custom/claude", "skills"), FindAgent("claude-code").SkillsDir(env, ScopeGlobal))
}

func bundle(t *testing.T, commit string, skills map[string]string) *Bundle {
	t.Helper()
	var entries []entry
	for name, version := range skills {
		entries = append(entries,
			entry{name: "skills/" + name + "/SKILL.md", body: skillMD(name, version)},
			entry{name: "skills/" + name + "/references/notes.md", body: name + " " + version},
		)
	}
	b, err := ParseArchive(bytes.NewReader(archive(t, commit, entries...)))
	require.NoError(t, err)
	b.Ref = DefaultRef
	return b
}

func state(t *testing.T, in *Installer, name string, dir SkillsDir) State {
	t.Helper()
	copies, err := in.Inspect([]string{name}, []SkillsDir{dir})
	require.NoError(t, err)
	return copies[0].State
}

func install(t *testing.T, in *Installer, names []string, dirs []SkillsDir) {
	t.Helper()
	copies, err := in.Inspect(names, dirs)
	require.NoError(t, err)
	for _, c := range copies {
		require.NoError(t, in.Write(c))
		in.Record(c.Upstream)
	}
	require.NoError(t, in.Save())
}

func TestInstallerLifecycle(t *testing.T) {
	for _, scope := range []Scope{ScopeProject, ScopeGlobal} {
		t.Run(scope.String(), func(t *testing.T) {
			env := testEnv(t)
			dirs := GroupSkillsDirs(env, scope, []*Agent{FindAgent("claude-code"), FindAgent("codex")})
			claude := dirs[0]

			v1 := bundle(t, "c1", map[string]string{"alpha": "1.0.0", "beta": "1.0.0"})
			in, err := NewInstaller(env, scope, v1)
			require.NoError(t, err)
			assert.Equal(t, StateMissing, state(t, in, "alpha", claude))

			install(t, in, v1.Names(), dirs)
			assert.Equal(t, StateCurrent, state(t, in, "alpha", claude))
			assert.FileExists(t, filepath.Join(dirs[1].Path, "beta", "references", "notes.md"))

			// A new upstream version: unedited copies are outdated.
			v2 := bundle(t, "c2", map[string]string{"alpha": "2.0.0", "gamma": "1.0.0"})
			in, err = NewInstaller(env, scope, v2)
			require.NoError(t, err)
			assert.Equal(t, StateOutdated, state(t, in, "alpha", claude))
			orphans, err := in.Orphans(dirs)
			require.NoError(t, err)
			assert.Equal(t, []string{"beta"}, orphans)
			assert.Equal(t, StateRemoved, state(t, in, "beta", claude))

			// Local edits are detected against both upstream and the lock.
			require.NoError(t, os.WriteFile(filepath.Join(dirs[1].Path, "alpha", "SKILL.md"), []byte("edited"), 0o644))
			assert.Equal(t, StateModified, state(t, in, "alpha", dirs[1]))

			// Removing an orphan and forgetting it clears the lock entry once
			// no copy remains.
			copies, err := in.Inspect([]string{"beta"}, dirs)
			require.NoError(t, err)
			for _, c := range copies {
				require.NoError(t, in.Delete(c))
			}
			require.NoError(t, in.Forget("beta"))
			_, ok := in.Lock.Get("beta")
			assert.False(t, ok)

			names, err := in.InstalledLiveKitSkills(GroupSkillsDirs(env, scope, Agents))
			require.NoError(t, err)
			assert.Equal(t, []string{"alpha"}, names)
		})
	}
}

func TestUntrackedSkill(t *testing.T) {
	env := testEnv(t)
	b := bundle(t, "c1", map[string]string{"alpha": "2.0.0"})
	in, err := NewInstaller(env, ScopeProject, b)
	require.NoError(t, err)
	dir := SkillsDir{Path: filepath.Join(env.Root, ".agents", "skills")}

	// A copy committed to a template: LiveKit's, an older version, no lock.
	write := func(name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Join(dir.Path, name), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir.Path, name, "SKILL.md"), []byte(body), 0o644))
	}
	write("alpha", skillMD("alpha", "1.0.0"))
	write("retired", skillMD("retired", "1.0.0"))
	write("mine", "---\nname: mine\ndescription: someone else's\n---\n")
	assert.Equal(t, StateUntracked, state(t, in, "alpha", dir))
	assert.Equal(t, StateUntracked, state(t, in, "retired", dir))
	assert.Equal(t, StateModified, state(t, in, "mine", dir))

	orphans, err := in.Orphans([]SkillsDir{dir})
	require.NoError(t, err)
	assert.Equal(t, []string{"retired"}, orphans)

	// Once installed through lk it's tracked.
	install(t, in, []string{"alpha"}, []SkillsDir{dir})
	assert.Equal(t, StateCurrent, state(t, in, "alpha", dir))
}

func TestInstallReplacesSymlink(t *testing.T) {
	env := testEnv(t)
	b := bundle(t, "c1", map[string]string{"alpha": "1.0.0"})
	in, err := NewInstaller(env, ScopeProject, b)
	require.NoError(t, err)

	// `npx skills` links .claude/skills/<name> to .agents/skills/<name>.
	shared := filepath.Join(env.Root, ".agents", "skills")
	install(t, in, []string{"alpha"}, []SkillsDir{{Path: shared}})
	claude := SkillsDir{Path: filepath.Join(env.Root, ".claude", "skills")}
	require.NoError(t, os.MkdirAll(claude.Path, 0o755))
	require.NoError(t, os.Symlink(filepath.Join("..", "..", ".agents", "skills", "alpha"), filepath.Join(claude.Path, "alpha")))
	assert.Equal(t, StateCurrent, state(t, in, "alpha", claude))

	copies, err := in.Inspect([]string{"alpha"}, []SkillsDir{claude})
	require.NoError(t, err)
	require.NoError(t, in.Write(copies[0]))
	info, err := os.Lstat(filepath.Join(claude.Path, "alpha"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	// The link's target is untouched.
	assert.FileExists(t, filepath.Join(shared, "alpha", "SKILL.md"))
}

func TestLockPreservesOtherEntries(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.Root, "skills-lock.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"skills":{"other":{"source":"acme/skills","sourceType":"github","computedHash":"x","custom":true}}}`), 0o644))

	lock, err := LoadLock(env, ScopeProject)
	require.NoError(t, err)
	b := bundle(t, "c1", map[string]string{"alpha": "1.0.0"})
	lock.Record(b.Find("alpha"), DefaultRef, timeNow())
	require.NoError(t, lock.Save())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got struct {
		Version int                        `json:"version"`
		Skills  map[string]json.RawMessage `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, 1, got.Version)
	assert.JSONEq(t, `{"source":"acme/skills","sourceType":"github","computedHash":"x","custom":true}`, string(got.Skills["other"]))
	assert.JSONEq(t, `{"source":"livekit/agent-skills","sourceType":"github","skillPath":"skills/alpha/SKILL.md","computedHash":"`+ContentHash(b.Find("alpha").Files)+`"}`, string(got.Skills["alpha"]))
	assert.True(t, bytes.HasPrefix(data, []byte("{\n  \"version\": 1,\n  \"skills\": {")), string(data))

	// Removing lk's entry keeps a lock that still has other skills.
	lock.Remove("alpha")
	require.NoError(t, lock.Save())
	assert.FileExists(t, path)
}

func TestLockDeletedWhenEmpty(t *testing.T) {
	env := testEnv(t)
	lock, err := LoadLock(env, ScopeProject)
	require.NoError(t, err)
	lock.Record(bundle(t, "c1", map[string]string{"alpha": "1.0.0"}).Find("alpha"), DefaultRef, timeNow())
	require.NoError(t, lock.Save())
	lock.Remove("alpha")
	require.NoError(t, lock.Save())
	assert.NoFileExists(t, lock.Path)
}

func TestLoadLockRejectsInvalidJSON(t *testing.T) {
	env := testEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(env.Root, "skills-lock.json"), []byte("<<<<<<< HEAD"), 0o644))
	_, err := LoadLock(env, ScopeProject)
	require.Error(t, err)
}

func TestFetch(t *testing.T) {
	data := archive(t, "deadbeef", entry{name: "skills/alpha/SKILL.md", body: skillMD("alpha", "1.0.0")})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/main" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	orig := ArchiveURL
	ArchiveURL = func(ref string) string { return srv.URL + "/" + ref }
	defer func() { ArchiveURL = orig }()

	b, err := Fetch(context.Background(), srv.Client(), "main")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", b.Commit)
	assert.Equal(t, "main", b.Ref)

	_, err = Fetch(context.Background(), srv.Client(), "nope")
	require.ErrorContains(t, err, `ref "nope" not found`)
}
