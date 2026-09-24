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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/skills"
)

// setHome points os.UserHomeDir at dir: $HOME, or %USERPROFILE% on Windows.
func setHome(t *testing.T, dir string) {
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// skillsArchive builds a GitHub-style tarball of the named skills.
func skillsArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := "---\nname: " + name + "\ndescription: " + name + "\nmetadata:\n  author: livekit\n  version: \"1.0.0\"\n---\n"
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: "agent-skills-main/skills/" + name + "/SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// TestSkillsCommands runs install, update and remove end to end against a fake
// skills repo, in a scratch project and home directory.
func TestSkillsCommands(t *testing.T) {
	archive := skillsArchive(t, "old-skill", "kept-skill")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	orig := skills.ArchiveURL
	skills.ArchiveURL = func(ref string) string { return srv.URL + "/" + ref }
	defer func() { skills.ArchiveURL = orig }()

	root := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Chdir(root)

	run := func(args ...string) {
		t.Helper()
		app := &cli.Command{Name: "lk", Flags: globalFlags, Commands: SkillsCommands, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
		require.NoError(t, app.Run(context.Background(), append([]string{"lk"}, args...)))
	}

	run("skills", "install", "-y", "--agent", "claude-code", "--agent", "codex")
	for _, p := range []string{
		".claude/skills/old-skill/SKILL.md",
		".agents/skills/kept-skill/SKILL.md",
		"skills-lock.json",
		".mcp.json",
		".codex/config.toml",
	} {
		assert.FileExists(t, filepath.Join(root, p))
	}

	// Upstream renames old-skill to new-skill: update swaps them wherever
	// LiveKit skills are installed.
	archive = skillsArchive(t, "kept-skill", "new-skill")
	run("skills", "update")
	assert.NoDirExists(t, filepath.Join(root, ".claude/skills/old-skill"))
	assert.NoDirExists(t, filepath.Join(root, ".agents/skills/old-skill"))
	assert.FileExists(t, filepath.Join(root, ".claude/skills/new-skill/SKILL.md"))
	assert.FileExists(t, filepath.Join(root, ".agents/skills/new-skill/SKILL.md"))
	lock, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(lock), "old-skill")
	assert.Contains(t, string(lock), "new-skill")

	run("skills", "remove", "-y")
	assert.NoDirExists(t, filepath.Join(root, ".claude/skills/kept-skill"))
	assert.NoDirExists(t, filepath.Join(root, ".agents/skills/new-skill"))
	assert.NoFileExists(t, filepath.Join(root, "skills-lock.json"))
	// MCP config stays.
	assert.FileExists(t, filepath.Join(root, ".mcp.json"))
}

func TestSkillsInstallUnknownAgent(t *testing.T) {
	app := &cli.Command{Name: "lk", Flags: globalFlags, Commands: SkillsCommands, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	err := app.Run(context.Background(), []string{"lk", "skills", "install", "--agent", "vim"})
	require.ErrorContains(t, err, `unknown agent "vim"`)
}

// fakeSkillsRepo serves archive as livekit/agent-skills for the test.
func fakeSkillsRepo(t *testing.T, archive []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	orig := skills.ArchiveURL
	skills.ArchiveURL = func(ref string) string { return srv.URL + "/" + ref }
	t.Cleanup(func() { skills.ArchiveURL = orig })
}

// writeTemplateSkill commits a LiveKit skill into dir the way starter
// templates used to: copied in, with no lock file.
func writeTemplateSkill(t *testing.T, dir, name string) {
	t.Helper()
	for _, sub := range []string{".agents/skills", ".claude/skills"} {
		p := filepath.Join(dir, sub, name)
		require.NoError(t, os.MkdirAll(p, 0o755))
		body := "---\nname: " + name + "\ndescription: old\nmetadata:\n  author: livekit\n  version: \"0.3.0\"\n---\n"
		require.NoError(t, os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(body), 0o644))
	}
}

// Projects created from the old starters carry untracked copies of skills
// LiveKit has since renamed; update replaces them.
func TestSkillsUpdateReplacesTemplateCopies(t *testing.T) {
	fakeSkillsRepo(t, skillsArchive(t, "new-skill"))
	root := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Chdir(root)
	writeTemplateSkill(t, root, "livekit-agents")

	app := &cli.Command{Name: "lk", Flags: globalFlags, Commands: SkillsCommands, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	require.NoError(t, app.Run(context.Background(), []string{"lk", "skills", "update"}))
	assert.NoDirExists(t, filepath.Join(root, ".agents/skills/livekit-agents"))
	assert.NoDirExists(t, filepath.Join(root, ".claude/skills/livekit-agents"))
	assert.FileExists(t, filepath.Join(root, ".agents/skills/new-skill/SKILL.md"))
	assert.FileExists(t, filepath.Join(root, ".claude/skills/new-skill/SKILL.md"))
	assert.FileExists(t, filepath.Join(root, "skills-lock.json"))
}

func TestSetupProjectSkills(t *testing.T) {
	fakeSkillsRepo(t, skillsArchive(t, "new-skill"))
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	setHome(t, home)
	t.Setenv("PATH", t.TempDir())
	t.Chdir(t.TempDir())

	newProject := func(t *testing.T, deps string) string {
		require.NoError(t, os.MkdirAll("app", 0o755))
		require.NoError(t, os.WriteFile(filepath.Join("app", "pyproject.toml"), []byte(deps), 0o644))
		return "app"
	}
	run := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		app := &cli.Command{
			Name:  "lk",
			Flags: slices.Concat(globalFlags, []cli.Flag{skillsSetupFlag}),
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return setupProjectSkills(ctx, cmd, dir)
			},
			Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{},
		}
		require.NoError(t, app.Run(context.Background(), append([]string{"lk"}, args...)))
	}

	t.Run("agent project", func(t *testing.T) {
		dir := newProject(t, `dependencies = ["livekit-agents[silero]~=1.3"]`)
		defer os.RemoveAll(dir)
		writeTemplateSkill(t, dir, "livekit-agents")
		run(t, dir)
		assert.FileExists(t, filepath.Join(dir, ".claude/skills/new-skill/SKILL.md"))
		assert.NoDirExists(t, filepath.Join(dir, ".claude/skills/livekit-agents"))
		assert.FileExists(t, filepath.Join(dir, ".mcp.json"))
	})
	t.Run("opted out", func(t *testing.T) {
		dir := newProject(t, `dependencies = ["livekit-agents"]`)
		defer os.RemoveAll(dir)
		run(t, dir, "--skills=false")
		assert.NoDirExists(t, filepath.Join(dir, ".claude"))
	})
	t.Run("not an agent project", func(t *testing.T) {
		dir := newProject(t, `dependencies = ["fastapi"]`)
		defer os.RemoveAll(dir)
		run(t, dir)
		assert.NoDirExists(t, filepath.Join(dir, ".claude"))
	})
}

func TestSkillsRemoveKeepsEditedSkills(t *testing.T) {
	fakeSkillsRepo(t, skillsArchive(t, "alpha", "beta"))
	root := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Chdir(root)
	run := func(args ...string) {
		t.Helper()
		app := &cli.Command{Name: "lk", Flags: globalFlags, Commands: SkillsCommands, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
		require.NoError(t, app.Run(context.Background(), append([]string{"lk"}, args...)))
	}

	run("skills", "install", "-y", "--skip-mcp", "--agent", "claude-code")
	edited := filepath.Join(root, ".claude/skills/alpha/SKILL.md")
	require.NoError(t, os.WriteFile(edited, []byte("---\nname: alpha\ndescription: mine now\nmetadata:\n  author: livekit\n---\n"), 0o644))

	run("skills", "remove", "-y")
	assert.FileExists(t, edited)
	assert.NoDirExists(t, filepath.Join(root, ".claude/skills/beta"))
	lock, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
	require.NoError(t, err)
	assert.Contains(t, string(lock), `"alpha"`)
	assert.NotContains(t, string(lock), `"beta"`)

	run("skills", "remove", "-y", "--force")
	assert.NoDirExists(t, filepath.Join(root, ".claude/skills/alpha"))
	assert.NoFileExists(t, filepath.Join(root, "skills-lock.json"))
}
