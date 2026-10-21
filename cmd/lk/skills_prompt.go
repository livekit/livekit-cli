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
	"errors"
	"os"
	"slices"

	"charm.land/huh/v2"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/skills"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// skillsPromptOptOutEnv hides the first-run skills offer for good, for
// machines that never want it (shared build hosts, images).
const skillsPromptOptOutEnv = "LK_NO_SKILLS_PROMPT"

// skillsOfferSkipCommands are top-level commands that never offer skills:
// the skills commands themselves, and ones whose output a tool reads.
var skillsOfferSkipCommands = []string{"skills", "help", "h", "completion", "generate-fish-completion"}

// skillsOfferSkipLeaves are commands with their own skills offer: lk agent init
// asks to install skills into the project it creates.
var skillsOfferSkipLeaves = []string{"lk agent init"}

type skillsOffer int

const (
	skillsOfferNone   skillsOffer = iota
	skillsOfferHint               // one status line; for runs that can't prompt
	skillsOfferPrompt             // ask once, and install on yes
)

// skillsOfferInput is everything skillsOfferFor decides on, gathered by
// maybeOfferSkills so the decision can be tested without a terminal.
type skillsOfferInput struct {
	command        string // top-level command name; empty for bare `lk`
	leaf           string // full name of the command that runs, such as "lk agent init"
	optedOut       bool   // LK_NO_SKILLS_PROMPT is set
	ci             bool   // CI is set
	alreadyAsked   bool   // the user answered the prompt before
	installed      bool   // LiveKit skills are installed globally or in this project
	agentsDetected bool   // at least one coding agent is on this machine
	interactive    bool   // stdin, stdout and stderr are terminals, and no --yes
	quiet          bool
	json           bool
	helpOrVersion  bool
}

// skillsOfferFor decides whether a run offers to install LiveKit's skills.
// It only asks with a terminal, and never blocks a script, CI, or a coding
// agent running lk: those get a one-line hint instead, so an agent can act on
// it. The hint repeats until skills are installed, the prompt is answered, or
// the opt-out is set; an answer of no is final.
func skillsOfferFor(in skillsOfferInput) skillsOffer {
	switch {
	case in.optedOut, in.ci, in.alreadyAsked, in.installed, !in.agentsDetected:
		return skillsOfferNone
	case in.command == "", in.helpOrVersion, slices.Contains(skillsOfferSkipCommands, in.command),
		slices.Contains(skillsOfferSkipLeaves, in.leaf):
		return skillsOfferNone
	case in.json, in.quiet:
		return skillsOfferNone
	case in.interactive:
		return skillsOfferPrompt
	}
	return skillsOfferHint
}

// maybeOfferSkills offers, on the first interactive run, to install
// LiveKit's skills and Docs MCP server into the coding agents on this machine
// (lk skills install --global). It runs before every command, ahead of any
// sign-in, so API-key and self-hosted users see it too. Its only error is the
// user cancelling the prompt (Ctrl+C), which stops the command it runs
// before; any other failure is reported and the command carries on.
func maybeOfferSkills(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig) error {
	env, err := skills.DefaultEnv()
	if err != nil {
		return nil
	}
	agents := skills.DetectAgents(env)
	leaf := leafCommand(cmd)
	in := skillsOfferInput{
		command:        cmd.Args().First(),
		leaf:           leaf.FullName(),
		optedOut:       os.Getenv(skillsPromptOptOutEnv) != "",
		ci:             os.Getenv("CI") != "",
		alreadyAsked:   conf != nil && conf.SkillsPrompted,
		agentsDetected: len(agents) > 0,
		interactive:    !SkipPrompts(cmd) && out.Interactive() && isatty.IsTerminal(os.Stdout.Fd()),
		quiet:          cmd.Bool("quiet"),
		json:           leaf.Bool("json"),
		helpOrVersion:  cmd.Bool("help") || leaf.Bool("help") || cmd.Bool("version"),
	}
	// Reading the lock files is cheap but not free; skip it when nothing
	// else would show the offer.
	if skillsOfferFor(in) == skillsOfferNone {
		return nil
	}
	in.installed = liveKitSkillsInstalled(env)

	switch skillsOfferFor(in) {
	case skillsOfferHint:
		out.Statusf("Tip: run %s to teach your coding agents (%s) to build LiveKit agents. Set %s=1 to hide this.",
			util.Accented("lk skills install --global --yes"), skills.AgentNames(agents), skillsPromptOptOutEnv)
	case skillsOfferPrompt:
		return promptSkillsInstall(ctx, cmd, conf, env, agents)
	}
	return nil
}

// skillsPromptAnswer is what a run does with the result of the skills prompt.
type skillsPromptAnswer struct {
	remember bool  // save that the prompt was answered, so it isn't asked again
	install  bool  // install skills now
	stop     error // cancel the command lk was running
}

// answerSkillsPrompt interprets the prompt's result. Ctrl+C cancels the
// command, as it does at any other lk prompt, and isn't an answer: the prompt
// comes back next time. A prompt that failed to show is retried next time
// too. Only a real yes or no is remembered.
func answerSkillsPrompt(err error, yes bool) skillsPromptAnswer {
	switch {
	case errors.Is(err, huh.ErrUserAborted):
		return skillsPromptAnswer{stop: err}
	case err != nil:
		return skillsPromptAnswer{}
	}
	return skillsPromptAnswer{remember: true, install: yes}
}

// promptSkillsInstall asks whether to install skills globally, remembers a
// yes or no, and installs on yes. Ctrl+C stops the command; any other failure
// is reported and the command carries on.
func promptSkillsInstall(ctx context.Context, cmd *cli.Command, conf *config.CLIConfig, env *skills.Env, agents []*skills.Agent) error {
	yes := true
	err := huh.NewForm(huh.NewGroup(util.Confirm().
		Title("Install LiveKit skills for your coding agents?").
		Description("Teaches " + skills.AgentNames(agents) + " to build, test, and debug LiveKit agents,\n" +
			"and adds the LiveKit Docs MCP server. Installs to your user-level agent\n" +
			"directories. lk asks once; run lk skills install any time.").
		Value(&yes).
		WithTheme(util.FormTheme()))).
		Run()
	answer := answerSkillsPrompt(err, yes)
	if answer.stop != nil {
		return answer.stop
	}
	if err != nil {
		out.Warnf("Couldn't show the LiveKit skills prompt: %v", err)
		return nil
	}
	if answer.remember && conf != nil {
		conf.SkillsPrompted = true
		if perr := conf.PersistQuietly(); perr != nil {
			out.Warnf("Couldn't save your answer to the CLI config: %v", perr)
		}
	}
	if !answer.install {
		out.Statusf("Skipped. Run %s any time to add them.", util.Accented("lk skills install --global"))
		return nil
	}
	s := &skillsSession{env: env, scope: skills.ScopeGlobal}
	o, err := s.install(ctx, installRequest{
		agents:  agents,
		ref:     skills.DefaultRef,
		confirm: func(copies []skills.Copy) (bool, error) { return confirmUntracked(cmd, s, copies) },
	})
	if err != nil {
		out.Warnf("Couldn't install LiveKit skills: %v. Run %s to try again.", err, util.Accented("lk skills install --global"))
		return nil
	}
	_ = s.printChange(o)
	return nil
}

// liveKitSkillsInstalled reports whether any LiveKit skill is installed for
// any agent, globally or in the current project. It reads only lock files
// and skills directories; nothing is downloaded.
func liveKitSkillsInstalled(env *skills.Env) bool {
	for _, scope := range []skills.Scope{skills.ScopeGlobal, skills.ScopeProject} {
		in, err := skills.NewInstaller(env, scope, nil)
		if err != nil {
			continue
		}
		names, err := in.InstalledLiveKitSkills(skills.GroupSkillsDirs(env, scope, skills.Agents))
		if err == nil && len(names) > 0 {
			return true
		}
	}
	return false
}

// leafCommand is the command a run resolves to, found by following cmd's
// arguments down the command tree. Before hooks get the root, but flags such
// as --json belong to the leaf.
func leafCommand(cmd *cli.Command) *cli.Command {
	leaf := cmd
	for _, arg := range cmd.Args().Slice() {
		sub := leaf.Command(arg)
		if sub == nil {
			break
		}
		leaf = sub
	}
	return leaf
}
