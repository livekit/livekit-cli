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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/skills"
)

func TestSkillsOfferFor(t *testing.T) {
	// base is an interactive first run of an ordinary command on a machine with
	// a coding agent and no LiveKit skills.
	base := skillsOfferInput{command: "room", leaf: "lk room list", agentsDetected: true, interactive: true}
	cases := []struct {
		name   string
		modify func(*skillsOfferInput)
		want   skillsOffer
	}{
		{"asks on the first interactive run", func(*skillsOfferInput) {}, skillsOfferPrompt},
		{"asks once", func(in *skillsOfferInput) { in.alreadyAsked = true }, skillsOfferNone},
		{"hints without a terminal or with --yes", func(in *skillsOfferInput) { in.interactive = false }, skillsOfferHint},
		{"stops hinting once the prompt was answered", func(in *skillsOfferInput) { in.interactive, in.alreadyAsked = false, true }, skillsOfferNone},
		{"stays quiet once installed", func(in *skillsOfferInput) { in.installed = true }, skillsOfferNone},
		{"stays quiet with no coding agents", func(in *skillsOfferInput) { in.agentsDetected = false }, skillsOfferNone},
		{"honors the opt-out", func(in *skillsOfferInput) { in.optedOut, in.interactive = true, false }, skillsOfferNone},
		{"stays quiet in CI", func(in *skillsOfferInput) { in.ci, in.interactive = true, false }, skillsOfferNone},
		{"stays quiet with --quiet", func(in *skillsOfferInput) { in.quiet = true }, skillsOfferNone},
		{"stays quiet with --json", func(in *skillsOfferInput) { in.json, in.interactive = true, false }, skillsOfferNone},
		{"skips bare lk", func(in *skillsOfferInput) { in.command, in.leaf = "", "lk" }, skillsOfferNone},
		{"skips help and version", func(in *skillsOfferInput) { in.helpOrVersion = true }, skillsOfferNone},
		{"skips the skills commands", func(in *skillsOfferInput) { in.command, in.leaf = "skills", "lk skills install" }, skillsOfferNone},
		{"skips completion", func(in *skillsOfferInput) { in.command = "generate-fish-completion" }, skillsOfferNone},
		{"leaves agent init to its own offer", func(in *skillsOfferInput) { in.command, in.leaf = "agent", "lk agent init" }, skillsOfferNone},
		{"offers before other agent commands", func(in *skillsOfferInput) { in.command, in.leaf = "agent", "lk agent deploy" }, skillsOfferPrompt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.modify(&in)
			assert.Equal(t, c.want, skillsOfferFor(in))
		})
	}
}

func TestLiveKitSkillsInstalled(t *testing.T) {
	writeSkill := func(t *testing.T, dir, name, author string) {
		t.Helper()
		p := filepath.Join(dir, name, "SKILL.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		body := "---\nname: " + name + "\ndescription: x\nmetadata:\n  author: " + author + "\n---\n"
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	newEnv := func(t *testing.T) *skills.Env {
		return &skills.Env{Home: t.TempDir(), Root: t.TempDir(), Getenv: func(string) string { return "" }}
	}

	t.Run("none", func(t *testing.T) {
		assert.False(t, liveKitSkillsInstalled(newEnv(t)))
	})
	t.Run("someone else's skill", func(t *testing.T) {
		env := newEnv(t)
		writeSkill(t, filepath.Join(env.Home, ".claude", "skills"), "other-skill", "someone")
		assert.False(t, liveKitSkillsInstalled(env))
	})
	t.Run("global", func(t *testing.T) {
		env := newEnv(t)
		writeSkill(t, filepath.Join(env.Home, ".claude", "skills"), "building-livekit-agents", "livekit")
		assert.True(t, liveKitSkillsInstalled(env))
	})
	t.Run("project", func(t *testing.T) {
		env := newEnv(t)
		writeSkill(t, filepath.Join(env.Root, ".agents", "skills"), "building-livekit-agents", "livekit")
		assert.True(t, liveKitSkillsInstalled(env))
	})
}

func TestLeafCommand(t *testing.T) {
	var got string
	app := &cli.Command{
		Name: "lk",
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			got = leafCommand(cmd).FullName()
			return ctx, nil
		},
		Commands: []*cli.Command{{
			Name:     "agent",
			Aliases:  []string{"a"},
			Commands: []*cli.Command{{Name: "init", Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}}, Action: func(context.Context, *cli.Command) error { return nil }}},
		}},
	}
	require.NoError(t, app.Run(context.Background(), []string{"lk", "a", "init", "--json", "my-agent"}))
	assert.Equal(t, "lk agent init", got)
}
