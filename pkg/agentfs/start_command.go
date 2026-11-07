package agentfs

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// StartCommandWarning explains why the Dockerfile's start command keeps SIGTERM from letting
// the agent drain, or returns "" when it does not.
//
// The container runtime signals only PID 1, and the container exits as soon as PID 1 does.
// npm, pnpm and yarn exit on SIGTERM without waiting for the script they started, and
// /bin/sh -c (dash on Debian images) neither execs nor forwards the signal. uv run and
// bash -c are fine: uv forwards the signal and waits, and bash execs the final command.
func StartCommandWarning(dockerfile []byte) string {
	argv := startCommand(dockerfile)
	if len(argv) == 0 {
		return ""
	}
	cmdline := strings.Join(argv, " ")
	switch path.Base(argv[0]) {
	case "npm", "pnpm", "yarn":
		return fmt.Sprintf("Dockerfile starts the agent with `%s`. %s exits on SIGTERM without waiting for your agent, so active sessions drop on every deploy and scale-down. Run the agent directly instead, e.g. CMD [\"node\", \"dist/agent.js\", \"start\"].", cmdline, path.Base(argv[0]))
	case "sh", "dash":
		if len(argv) > 2 && argv[1] == "-c" && !strings.HasPrefix(strings.TrimSpace(argv[2]), "exec ") {
			return fmt.Sprintf("Dockerfile starts the agent with `%s`. The shell does not pass SIGTERM to your agent, so active sessions drop on every deploy and scale-down. Use the exec form, e.g. CMD [\"python\", \"agent.py\", \"start\"], or prefix the command with `exec`.", cmdline)
		}
	}
	return ""
}

// startCommand returns the argv the final stage runs: ENTRYPOINT followed by CMD, each
// inherited through stages built FROM another stage. Unparsable Dockerfiles yield nil.
func startCommand(dockerfile []byte) []string {
	res, err := parser.Parse(bytes.NewReader(dockerfile))
	if err != nil {
		return nil
	}
	stages, _, err := instructions.Parse(res.AST, nil)
	if err != nil || len(stages) == 0 {
		return nil
	}

	type runConfig struct{ shell, entrypoint, cmd []string }
	byName := map[string]runConfig{}
	var rc runConfig
	for _, stage := range stages {
		rc = byName[strings.ToLower(stage.BaseName)]
		if rc.shell == nil {
			rc.shell = []string{"/bin/sh", "-c"}
		}
		for _, c := range stage.Commands {
			switch c := c.(type) {
			case *instructions.ShellCommand:
				rc.shell = c.Shell
			case *instructions.EntrypointCommand:
				rc.entrypoint = withShell(rc.shell, c.ShellDependantCmdLine)
				rc.cmd = nil
			case *instructions.CmdCommand:
				rc.cmd = withShell(rc.shell, c.ShellDependantCmdLine)
			}
		}
		if stage.Name != "" {
			byName[strings.ToLower(stage.Name)] = rc
		}
	}
	return append(append([]string{}, rc.entrypoint...), rc.cmd...)
}

func withShell(shell []string, line instructions.ShellDependantCmdLine) []string {
	if !line.PrependShell {
		return line.CmdLine
	}
	return append(append([]string{}, shell...), strings.Join(line.CmdLine, " "))
}
