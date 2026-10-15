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
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// docs.livekit.io renders this command's output inline in its CLI reference
// (livekit/web docs-cli-help workflow), so the reference always matches the
// released help text. The output sits under a hand-written heading and
// description, so it carries only the usage line, subcommands, and options.
func generateDocs(ctx context.Context, cmd *cli.Command) error {
	pruneHiddenCommands(cmd.Root())
	path := cmd.Args().Slice()
	target := cmd.Root()
	for _, name := range path {
		if target = target.Command(name); target == nil {
			return fmt.Errorf("unknown command: lk %s", strings.Join(path, " "))
		}
	}
	out.Result(commandDocs(target, path))
	return nil
}

func commandDocs(cmd *cli.Command, path []string) string {
	var b strings.Builder
	usage := cmd.UsageText
	if usage == "" {
		var lines []string
		for _, name := range append([]string{cmd.Name}, cmd.Aliases...) {
			lines = append(lines, usageLine(cmd, append(path[:len(path)-1:len(path)-1], name)))
		}
		usage = strings.Join(lines, "\n# or\n")
	}
	fmt.Fprintf(&b, "```shell\n%s\n```\n", strings.TrimSpace(usage))

	if subs := cmd.VisibleCommands(); len(subs) > 0 {
		b.WriteString("\nSubcommands:\n\n")
		for _, sub := range subs {
			name := sub.Name
			if sub.ArgsUsage != "" {
				name += " " + sub.ArgsUsage
			}
			fmt.Fprintf(&b, "- `%s`: %s\n", name, sentence(sub.Usage))
		}
	}

	if flags := docFlags(cmd); len(flags) > 0 {
		// Each docs page covers one top-level command group, so options are
		// labelled by the path below it: "Options for `deploy`".
		// Aliases are joined into one label: "Options for `logs`/`tail`".
		parent := strings.Join(path[:len(path)-1], " ")
		if len(path) > 1 {
			parent = strings.Join(path[1:len(path)-1], " ")
		}
		var labels []string
		for _, name := range append([]string{cmd.Name}, cmd.Aliases...) {
			labels = append(labels, "`"+strings.TrimSpace(parent+" "+name)+"`")
		}
		fmt.Fprintf(&b, "\nOptions for %s:\n\n", strings.Join(labels, "/"))
		for _, f := range flags {
			b.WriteString(flagDocs(f))
		}
	}
	return b.String()
}

func usageLine(cmd *cli.Command, path []string) string {
	usage := strings.Join(append([]string{"lk"}, path...), " ")
	if len(cmd.VisibleCommands()) > 0 {
		usage += " <command>"
	}
	if len(docFlags(cmd)) > 0 {
		usage += " [options]"
	}
	if cmd.ArgsUsage != "" {
		usage += " " + cmd.ArgsUsage
	}
	return usage
}

// docFlags is the command's own visible flags, without the --help flag urfave
// copies into every command.
func docFlags(cmd *cli.Command) []cli.Flag {
	var flags []cli.Flag
	for _, f := range cmd.VisibleFlags() {
		if f.Names()[0] != cli.HelpFlag.Names()[0] {
			flags = append(flags, f)
		}
	}
	return flags
}

func flagDocs(f cli.Flag) string {
	doc, ok := f.(cli.DocGenerationFlag)
	if !ok {
		return ""
	}
	placeholder, usage := unquoteUsage(doc.GetUsage())
	if doc.TakesValue() && placeholder == "" {
		placeholder = "VALUE"
	}

	names := f.Names()
	name := "--" + names[0]
	if doc.TakesValue() {
		name += " " + placeholder
	}
	line := fmt.Sprintf("- `%s`", name)
	if len(names) > 1 {
		var aliases []string
		for _, a := range names[1:] {
			aliases = append(aliases, "`"+flagPrefix(a)+a+"`")
		}
		line += fmt.Sprintf(" (alias %s)", strings.Join(aliases, ", "))
	}
	line += ": " + sentence(usage)
	if def := defaultText(doc); def != "" && !strings.Contains(usage, "Default") {
		line += fmt.Sprintf(" Default: `%s`.", def)
	}
	return line + "\n"
}

func defaultText(f cli.DocGenerationFlag) string {
	if t := f.GetDefaultText(); t != "" {
		return t
	}
	switch v := strings.Trim(f.GetValue(), `"`); v {
	case "", "false", "0", "[]", "0s":
		return ""
	default:
		return v
	}
}

func flagPrefix(name string) string {
	if len(name) == 1 {
		return "-"
	}
	return "--"
}

// unquoteUsage matches urfave/cli's help: the first `quoted` word in a flag's
// usage is its value placeholder, and prints without the backticks.
func unquoteUsage(usage string) (string, string) {
	i := strings.Index(usage, "`")
	if i < 0 {
		return "", usage
	}
	j := strings.Index(usage[i+1:], "`")
	if j < 0 {
		return "", usage
	}
	name := usage[i+1 : i+1+j]
	return name, usage[:i] + name + usage[i+2+j:]
}

func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, ")") && strings.HasSuffix(strings.TrimSuffix(s, ")"), ".") {
		return s
	}
	return s + "."
}
