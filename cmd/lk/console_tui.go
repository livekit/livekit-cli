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
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

var (
	consoleTitleStyle  = lipgloss.NewStyle().Background(lipgloss.Color("#1fd5f9")).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1)
	consoleGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	consoleRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	consoleDimStyle    = lipgloss.NewStyle().Faint(true)
	consoleBoldStyle   = lipgloss.NewStyle().Bold(true)
	consoleCyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	consoleYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// Unicode block characters for frequency visualizer
var blocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

type consoleTickMsg struct{}

type consoleModel struct {
	pipeline  *console.AudioPipeline
	addr      string
	inputDev  *portaudio.DeviceInfo
	outputDev *portaudio.DeviceInfo

	width  int
	height int
}

func newConsoleModel(pipeline *console.AudioPipeline, addr string, inputDev, outputDev *portaudio.DeviceInfo) consoleModel {
	return consoleModel{
		pipeline:  pipeline,
		addr:      addr,
		inputDev:  inputDev,
		outputDev: outputDev,
	}
}

func (m consoleModel) Init() tea.Cmd {
	return tea.Batch(
		consoleTickCmd(),
	)
}

func consoleTickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return consoleTickMsg{}
	})
}

func (m consoleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "m":
			m.pipeline.SetMuted(!m.pipeline.Muted())
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case consoleTickMsg:
		return m, consoleTickCmd()
	}

	return m, nil
}

func (m consoleModel) View() string {
	var b strings.Builder

	b.WriteString(consoleTitleStyle.Render(" lk console "))
	b.WriteString("\n\n")

	b.WriteString(consoleBoldStyle.Render("Status: "))
	b.WriteString(consoleGreenStyle.Render("● Connected"))
	b.WriteString("  ")
	b.WriteString(consoleDimStyle.Render(m.addr))
	b.WriteString("\n")

	b.WriteString(consoleBoldStyle.Render("Input:  "))
	b.WriteString(m.inputDev.Name)
	b.WriteString("\n")
	b.WriteString(consoleBoldStyle.Render("Output: "))
	b.WriteString(m.outputDev.Name)
	b.WriteString("\n\n")

	bands := m.pipeline.FFTBands()
	b.WriteString(consoleBoldStyle.Render("Audio "))
	for _, band := range bands {
		idx := int(band * float64(len(blocks)-1))
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		if idx < 0 {
			idx = 0
		}
		if band > 0.5 {
			b.WriteString(consoleCyanStyle.Render(string(blocks[idx])))
		} else if band > 0.2 {
			b.WriteString(consoleGreenStyle.Render(string(blocks[idx])))
		} else {
			b.WriteString(consoleDimStyle.Render(string(blocks[idx])))
		}
	}
	b.WriteString("\n\n")

	level := m.pipeline.Level()
	b.WriteString(consoleBoldStyle.Render("Mic: "))

	if m.pipeline.Muted() {
		b.WriteString(consoleRedStyle.Render("MUTED"))
	} else {
		// Level bar: -60dB to 0dB
		normalized := (level + 60) / 60
		if normalized < 0 {
			normalized = 0
		}
		if normalized > 1 {
			normalized = 1
		}
		barWidth := 30
		filled := int(normalized * float64(barWidth))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		if normalized > 0.8 {
			b.WriteString(consoleRedStyle.Render(bar))
		} else if normalized > 0.5 {
			b.WriteString(consoleYellowStyle.Render(bar))
		} else {
			b.WriteString(consoleGreenStyle.Render(bar))
		}
		b.WriteString(fmt.Sprintf(" %.0f dB", level))
	}
	b.WriteString("\n\n")

	b.WriteString(consoleDimStyle.Render("m: mute/unmute  q: quit"))
	b.WriteString("\n")

	return b.String()
}
