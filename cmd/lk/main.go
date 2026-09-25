// Copyright 2021-2024 LiveKit, Inc.
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
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

func main() {
	app := &cli.Command{
		Name:  "lk",
		Usage: "The LiveKit command line tool",
		Description: `Build, test, and deploy LiveKit agents, and work with rooms, LiveKit Cloud,
and telephony from the terminal.

Docs: https://docs.livekit.io/intro/basics/cli/`,
		CustomRootCommandHelpTemplate: rootHelpTemplate,
		Version:                       livekitcli.Version,
		EnableShellCompletion:         true,
		Suggest:                       true,
		HideHelpCommand:               true,
		UseShortOptionHandling:        true,
		Flags:                         globalFlags,
		// --experimental-auth and --legacy-auth pick opposite auth modes; you may
		// pass at most one. (These flags are registered via this group, not
		// globalFlags.)
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{
				Flags: [][]cli.Flag{
					{experimentalAuthFlag},
					{legacyAuthFlag},
				},
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "generate-fish-completion",
				Action: generateFishCompletion,
				Hidden: true,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "out",
						Aliases: []string{"o"},
					},
				},
			},
		},
		Before: initLogger,
		After: func(context.Context, *cli.Command) error {
			flushBanner()
			return nil
		},
	}

	categorizeAgentCommands()
	cli.HelpPrinter = func(w io.Writer, templ string, data any) {
		// cli/v3 renders `lk agent --help` with its stock subcommand template
		// regardless of CustomHelpTemplate; swap in the grouped one here.
		if c, ok := data.(*cli.Command); ok && c.Name == "agent" && templ == cli.SubcommandHelpTemplate {
			templ = agentHelpTemplate
		}
		cli.HelpPrinterCustom(w, templ, data, map[string]any{
			"rootSections":  rootHelpSections,
			"agentSections": agentHelpSections,
		})
	}
	app.Commands = append(app.Commands, AppCommands...)
	app.Commands = append(app.Commands, AgentCommands...)
	app.Commands = append(app.Commands, AnalyticsCommands...)
	app.Commands = append(app.Commands, CloudCommands...)
	app.Commands = append(app.Commands, DocsCommands...)
	app.Commands = append(app.Commands, ProjectCommands...)
	app.Commands = append(app.Commands, WorkspaceCommands...)
	app.Commands = append(app.Commands, UserCommands...)
	app.Commands = append(app.Commands, ThemeCommands...)
	app.Commands = append(app.Commands, RoomCommands...)
	app.Commands = append(app.Commands, TokenCommands...)
	app.Commands = append(app.Commands, JoinCommands...)
	app.Commands = append(app.Commands, DispatchCommands...)
	app.Commands = append(app.Commands, EgressCommands...)
	app.Commands = append(app.Commands, IngressCommands...)
	app.Commands = append(app.Commands, SIPCommands...)
	app.Commands = append(app.Commands, PhoneNumberCommands...)
	app.Commands = append(app.Commands, ReplayCommands...)
	app.Commands = append(app.Commands, PerfCommands...)
	app.Commands = append(app.Commands, UpdateCommands...)
	app.CommandNotFound = commandNotFound
	setCommandNotFound(app.Commands)

	// Register cleanup hook for SIGINT, SIGTERM, SIGQUIT
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT,
	)
	defer stop()

	// Cleanup on hooked signals, remembering to flush stdout
	// before exit to prevent line rag in case of SIGINT
	go func() {
		<-ctx.Done()
		stop()
	}()

	checkForLegacyName()

	err := app.Run(ctx, os.Args)
	if err != nil {
		errStyle := lipgloss.NewStyle().Foreground(util.Error())
		// Outside the Printer's reach (it may not be initialized yet), so the
		// color profile has to be applied here too — Lip Gloss v2 emits styles
		// regardless of whether stderr can render them.
		errOut := colorprofile.NewWriter(os.Stderr, os.Environ())
		// Render line by line: a multiline Render pads every line with
		// trailing spaces to match the widest one, which wraps into garbage
		// on terminals narrower than the longest line.
		for line := range strings.SplitSeq(err.Error(), "\n") {
			fmt.Fprintln(errOut, errStyle.Render(line))
		}
		os.Exit(1)
	}
}

func checkForLegacyName() {
	if !strings.HasSuffix(os.Args[0], "lk") && !strings.HasSuffix(os.Args[0], "lk.exe") {
		// Stays on raw os.Stderr: this runs before the cli command parses (so the
		// Printer isn't initialized yet) and is a deprecation warning that should
		// not be suppressed by --quiet.
		fmt.Fprintf(
			os.Stderr,
			"\n~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~ DEPRECATION NOTICE ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n"+
				"The `livekit-cli` binary has been renamed to `lk`, and some of the options and\n"+
				"commands have changed. Though legacy commands my continue to work, they have\n"+
				"been hidden from the USAGE notes and may be removed in future releases."+
				"\n~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n\n",
		)
	}
}

func initLogger(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	logConfig := &logger.Config{
		Level: "info",
		ComponentLevels: map[string]string{
			"pion": "error",
		},
	}
	if cmd.Bool("verbose") {
		logConfig.Level = "debug"
	}
	logger.InitFromConfig(logConfig, "lk")
	lksdk.SetLogger(logger.GetLogger())

	// Bind the human-facing output sink to the root command's writers (cli/v3
	// defaults them to os.Stdout / os.Stderr, but they're overridable in tests).
	out = util.NewPrinter(cmd.Root().Writer, cmd.Root().ErrWriter, cmd.Bool("quiet"))
	// Register the same Printer as the process-wide default so lower-level
	// packages (config, agentfs, …) route through the same streams/gating.
	util.SetDefault(out)

	// Apply the persisted color theme before any output/forms render. An empty value
	// resolves to the default; an invalid stored value is reported and falls back.
	conf, _ := config.LoadOrCreate()
	if conf != nil {
		if err := util.SetTheme(conf.Theme); err != nil {
			out.Warnf("%v; using default theme", err)
		}
	}
	util.DetectBackground()

	deferBanner(ctx)

	// Nudge users still on API-key auth toward the new account-based flow.
	maybeShowUpgradeNotice(cmd, conf)

	return nil, nil
}

// Keep autocomplete/fish_autocomplete in sync with the command tree. CI (test.yaml)
// fails if the committed file drifts; run `go generate ./...` to refresh it.
//
//go:generate go run . generate-fish-completion -o ../../autocomplete/fish_autocomplete
func generateFishCompletion(ctx context.Context, cmd *cli.Command) error {
	// urfave skips a hidden command's own line but still emits its subcommands
	// and flags, so hidden groups (e.g. `lk workspace`) would leak into
	// completion. The process exits after this, so pruning in place is safe.
	pruneHiddenCommands(cmd.Root())
	fishScript, err := cmd.Root().ToFishCompletion()
	if err != nil {
		return err
	}

	outPath := cmd.String("out")
	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(fishScript), 0o644); err != nil {
			return err
		}
	} else {
		out.Result(fishScript)
	}

	return nil
}

// pruneHiddenCommands drops Hidden commands, and everything under them, from
// the tree rooted at cmd.
func pruneHiddenCommands(cmd *cli.Command) {
	visible := cmd.Commands[:0]
	for _, c := range cmd.Commands {
		if c.Hidden {
			continue
		}
		pruneHiddenCommands(c)
		visible = append(visible, c)
	}
	cmd.Commands = visible
}

// Root help is laid out like a product CLI (compare `modal --help`): a short
// description, then every command grouped into named sections with a one-line
// summary each. Agent subcommands are listed inline as "agent <name>" so the
// agent workflow is visible from `lk --help` rather than hidden behind one row.
const rootHelpTemplate = `NAME:
   {{template "helpNameTemplate" .}}

USAGE:
   {{.FullName}} [global options] command [command options] [arguments...]

VERSION:
   {{.Version}}

DESCRIPTION:
   {{template "descriptionTemplate" .}}
{{range rootSections .}}
{{.Title}}:{{range .Rows}}
   {{.Name}}{{"\t"}}{{.Usage}}{{end}}
{{end}}
Run "lk <command> --help" for details; "lk a" is short for "lk agent".
{{if .VisibleFlagCategories}}
GLOBAL OPTIONS:{{template "visibleFlagCategoryTemplate" .}}{{else if .VisibleFlags}}
GLOBAL OPTIONS:{{template "visibleFlagTemplate" .}}{{end}}
`

type helpRow struct{ Name, Usage string }

type helpSection struct {
	Title string
	Rows  []helpRow
}

// rootHelpGroups orders the non-agent commands into sections. Anything not
// listed lands in a trailing OTHER section, so a new command never disappears.
var rootHelpGroups = []struct {
	title string
	names []string
}{
	{"PROJECTS", []string{"project", "cloud", "app"}},
	{"ROOMS AND MEDIA", []string{"room", "token", "dispatch", "egress", "ingress"}},
	{"TELEPHONY", []string{"sip", "number"}},
	{"TOOLS", []string{"docs", "perf", "update", "can-update"}},
}

// agentHelpSections groups a command's visible subcommands by Category, in
// first-seen order, for `lk agent --help`. cli/v3 only populates its own
// category list once the subcommand runs, which is after help is rendered.
func agentHelpSections(cmd *cli.Command) []helpSection {
	var sections []helpSection
	index := map[string]int{}
	for _, sub := range cmd.VisibleCommands() {
		i, ok := index[sub.Category]
		if !ok {
			i = len(sections)
			index[sub.Category] = i
			sections = append(sections, helpSection{Title: sub.Category})
		}
		sections[i].Rows = append(sections[i].Rows, helpRow{Name: strings.Join(sub.Names(), ", "), Usage: sub.Usage})
	}
	return sections
}

// rootHelpSections builds the sections rendered by rootHelpTemplate from the
// live command tree, so summaries stay in sync with each command's Usage.
func rootHelpSections(root *cli.Command) []helpSection {
	remaining := map[string]*cli.Command{}
	for _, c := range root.VisibleCommands() {
		remaining[c.Name] = c
	}

	var sections []helpSection
	if agentCmd, ok := remaining["agent"]; ok {
		local := helpSection{Title: "AGENTS"}
		cloud := helpSection{Title: "AGENT DEPLOYMENT (LIVEKIT CLOUD)"}
		for _, sub := range agentCmd.VisibleCommands() {
			row := helpRow{Name: "agent " + sub.Name, Usage: sub.Usage}
			if sub.Category == agentCategoryCloud {
				cloud.Rows = append(cloud.Rows, row)
			} else {
				local.Rows = append(local.Rows, row)
			}
		}
		sections = append(sections, local, cloud)
		delete(remaining, "agent")
	}
	for _, g := range rootHelpGroups {
		sec := helpSection{Title: g.title}
		for _, name := range g.names {
			if c, ok := remaining[name]; ok {
				sec.Rows = append(sec.Rows, helpRow{Name: strings.Join(c.Names(), ", "), Usage: c.Usage})
				delete(remaining, name)
			}
		}
		if len(sec.Rows) > 0 {
			sections = append(sections, sec)
		}
	}
	other := helpSection{Title: "OTHER"}
	for _, c := range root.VisibleCommands() {
		if _, ok := remaining[c.Name]; ok {
			other.Rows = append(other.Rows, helpRow{Name: strings.Join(c.Names(), ", "), Usage: c.Usage})
		}
	}
	if len(other.Rows) > 0 {
		sections = append(sections, other)
	}
	return sections
}
