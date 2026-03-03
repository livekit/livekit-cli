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
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

var ConsoleCommands = []*cli.Command{
	{
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
		},
		Action: runConsole,
	},
}

func runConsole(ctx context.Context, cmd *cli.Command) error {
	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize PortAudio: %w", err)
	}
	defer portaudio.Terminate()

	if cmd.Bool("list-devices") {
		return listDevices()
	}

	var inputDev *portaudio.DeviceInfo
	var err error
	if q := cmd.String("input-device"); q != "" {
		inputDev, err = portaudio.FindDevice(q, true)
	} else {
		inputDev, err = portaudio.DefaultInputDevice()
	}
	if err != nil {
		return fmt.Errorf("input device: %w", err)
	}

	var outputDev *portaudio.DeviceInfo
	if q := cmd.String("output-device"); q != "" {
		outputDev, err = portaudio.FindDevice(q, false)
	} else {
		outputDev, err = portaudio.DefaultOutputDevice()
	}
	if err != nil {
		return fmt.Errorf("output device: %w", err)
	}

	port := cmd.Int("port")
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	server, err := console.NewTCPServer(addr)
	if err != nil {
		return err
	}
	defer server.Close()

	actualAddr := server.Addr().String()
	fmt.Fprintf(os.Stderr, "Listening on %s\n", actualAddr)
	fmt.Fprintf(os.Stderr, "Input:  %s\n", inputDev.Name)
	fmt.Fprintf(os.Stderr, "Output: %s\n", outputDev.Name)
	fmt.Fprintf(os.Stderr, "Waiting for agent connection...\n")

	conn, err := server.Accept()
	if err != nil {
		return fmt.Errorf("agent connection: %w", err)
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "Agent connected from %s\n", conn.RemoteAddr())

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

	model := newConsoleModel(pipeline, actualAddr, inputDev, outputDev)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return err
	}

	pipelineCancel()
	pipeline.Stop()

	return nil
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
