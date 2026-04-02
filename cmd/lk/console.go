//go:build console

// Copyright 2025 LiveKit, Inc.
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
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

func init() {
	AgentCommands[0].Commands = append(AgentCommands[0].Commands, consoleCommand)
}

var consoleCommand = &cli.Command{
	Name:     "console",
	Usage:    "Voice chat with an agent via mic/speakers",
	Category: "Core",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "port",
			Aliases: []string{"p"},
			Usage:   "TCP port for agent communication",
			Value:   0,
		},
		&cli.StringFlag{
			Name:  "input-device",
			Usage: "Input device index or name substring",
		},
		&cli.StringFlag{
			Name:  "output-device",
			Usage: "Output device index or name substring",
		},
		&cli.BoolFlag{
			Name:  "list-devices",
			Usage: "List available audio devices and exit",
		},
		&cli.BoolFlag{
			Name:  "no-aec",
			Usage: "Disable acoustic echo cancellation",
		},
		&cli.BoolFlag{
			Name:    "text",
			Aliases: []string{"t"},
			Usage:   "Start in text mode instead of audio mode",
		},
		&cli.BoolFlag{
			Name:  "record",
			Usage: "Record audio and session report to console-recordings/",
		},
		&cli.StringFlag{
			Name:  "entrypoint",
			Usage: "Agent entrypoint `FILE` (default: auto-detect)",
		},
	},
	Action: runConsole,
}

func runConsole(ctx context.Context, cmd *cli.Command) error {
	textMode := cmd.Bool("text")

	var inputDev, outputDev *portaudio.DeviceInfo
	if !textMode {
		if err := portaudio.Initialize(); err != nil {
			return fmt.Errorf("failed to initialize PortAudio: %w", err)
		}
		defer portaudio.Terminate()

		if cmd.Bool("list-devices") {
			return listDevices()
		}

		var err error
		if q := cmd.String("input-device"); q != "" {
			inputDev, err = portaudio.FindDevice(q, true)
		} else {
			inputDev, err = portaudio.DefaultInputDevice()
		}
		if err != nil {
			return fmt.Errorf("input device: %w", err)
		}

		if q := cmd.String("output-device"); q != "" {
			outputDev, err = portaudio.FindDevice(q, false)
		} else {
			outputDev, err = portaudio.DefaultOutputDevice()
		}
		if err != nil {
			return fmt.Errorf("output device: %w", err)
		}
	}

	port := cmd.Int("port")
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var err error
	server, err := console.NewTCPServer(addr)
	if err != nil {
		return err
	}
	defer server.Close()

	actualAddr := server.Addr().String()
	if inputDev != nil {
		fmt.Fprintf(os.Stderr, "Input:  %s\n", inputDev.Name)
		fmt.Fprintf(os.Stderr, "Output: %s\n", outputDev.Name)
	}

	projectDir, projectType, entrypoint, err := detectProject(cmd)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Detected %s agent (%s in %s)\n", projectType.Lang(), entrypoint, projectDir)
	agentProc, err := startAgent(AgentStartConfig{
		Dir:         projectDir,
		Entrypoint:  entrypoint,
		ProjectType: projectType,
		CLIArgs:     buildConsoleArgs(actualAddr, cmd.Bool("record")),
	})
	if err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}
	defer agentProc.Kill()

	// Stream agent logs to the TUI
	agentProc.LogStream = make(chan string, 128)

	// Wait for TCP connection, agent crash, timeout, or cancellation
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := server.Accept()
		acceptCh <- acceptResult{conn, err}
	}()

	var conn net.Conn
	select {
	case res := <-acceptCh:
		if res.err != nil {
			return fmt.Errorf("agent connection: %w", res.err)
		}
		conn = res.conn
	case err := <-agentProc.Done():
		logs := agentProc.RecentLogs(20)
		for _, l := range logs {
			fmt.Fprintln(os.Stderr, l)
		}
		if err != nil {
			return fmt.Errorf("agent exited before connecting: %w", err)
		}
		return fmt.Errorf("agent exited before connecting")
	case <-time.After(60 * time.Second):
		logs := agentProc.RecentLogs(20)
		for _, l := range logs {
			fmt.Fprintln(os.Stderr, l)
		}
		return fmt.Errorf("timed out waiting for agent to connect")
	case <-ctx.Done():
		return ctx.Err()
	}
	pipeline, err := console.NewPipeline(console.PipelineConfig{
		InputDevice:  inputDev,
		OutputDevice: outputDev,
		NoAEC:        cmd.Bool("no-aec"),
		Conn:         conn,
	})
	if err != nil {
		return fmt.Errorf("pipeline: %w", err)
	}

	pipelineCtx, pipelineCancel := context.WithCancel(ctx)
	defer pipelineCancel()

	go func() {
		pipeline.Start(pipelineCtx)
	}()

	// Redirect Go's default logger to discard so it doesn't corrupt the TUI
	log.SetOutput(io.Discard)

	// Remove the global SIGINT handler (from signal.NotifyContext in main.go)
	// so that ctrl+C in raw mode reaches Bubble Tea as a key event, and after
	// the TUI exits, a ctrl+C during cleanup uses the default handler (terminate).
	signal.Reset(syscall.SIGINT)

	var inputDevName, outputDevName string
	if inputDev != nil {
		inputDevName = inputDev.Name
	}
	if outputDev != nil {
		outputDevName = outputDev.Name
	}
	model := newConsoleModel(pipeline, pipelineCancel, agentProc, inputDevName, outputDevName, textMode)
	p := tea.NewProgram(model, tea.WithoutSignalHandler())

	if _, err := p.Run(); err != nil {
		return err
	}

	return nil
}

func buildConsoleArgs(addr string, record bool) []string {
	args := []string{"console", "--connect-addr", addr}
	if record {
		args = append(args, "--record")
	}
	return args
}

func listDevices() error {
	devices, err := portaudio.ListDevices()
	if err != nil {
		return err
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	defaultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	fmt.Println(headerStyle.Render(fmt.Sprintf("  %-4s %-8s %-45s %s", "#", "Type", "Name", "Default")))
	fmt.Println(strings.Repeat("─", 70))

	for _, d := range devices {
		devType := ""
		if d.MaxInputChannels > 0 && d.MaxOutputChannels > 0 {
			devType = "Both"
		} else if d.MaxInputChannels > 0 {
			devType = "Input"
		} else {
			devType = "Output"
		}

		defStr := ""
		if d.IsDefaultInput {
			defStr += defaultStyle.Render("✓ input")
		}
		if d.IsDefaultOutput {
			if defStr != "" {
				defStr += " "
			}
			defStr += defaultStyle.Render("✓ output")
		}

		fmt.Printf("  %-4d %-8s %-45s %s\n", d.Index, devType, d.Name, defStr)
	}

	return nil
}

