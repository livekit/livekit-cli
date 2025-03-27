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

package agentfs

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242")).
			Padding(0, 1)

	titleText = lipgloss.NewStyle().
			Foreground(lipgloss.Color("36")).
			Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("36"))

	checkboxStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("36"))

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("36")).
				Bold(true)

	regularItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))
)

type secretModel struct {
	secrets  map[string]string
	cursor   int
	selected map[string]bool
	quitting bool
}

func (m secretModel) Init() tea.Cmd {
	return nil
}

func (m secretModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.secrets)-1 {
				m.cursor++
			}
		case " ":
			keys := make([]string, 0, len(m.secrets))
			for k := range m.secrets {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			key := keys[m.cursor]
			m.selected[key] = !m.selected[key]
		case "enter":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m secretModel) View() string {
	var b strings.Builder

	b.WriteString(titleText.Render("Select secrets to include (space to toggle, enter to confirm)") + "\n")

	keys := make([]string, 0, len(m.secrets))
	for k := range m.secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, key := range keys {
		cursor := " "
		if m.cursor == i {
			cursor = cursorStyle.Render("→")
		}

		checkbox := "[" + checkboxStyle.Render(" ") + "]"
		if m.selected[key] {
			checkbox = "[" + checkboxStyle.Render("×") + "]"
		}

		item := regularItemStyle.Render(key + ": ***")
		if m.cursor == i {
			item = selectedItemStyle.Render(key + ": ***")
		}

		b.WriteString(fmt.Sprintf("%s %s %s\n", cursor, checkbox, item))
	}

	b.WriteString("\n" + footerStyle.Render("Press q to quit"))

	return b.String()
}

func ParseEnvFile(file string) (map[string]string, error) {
	env := make(map[string]string)
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			continue
		}

		parts[0] = strings.TrimSpace(parts[0])
		parts[1] = strings.TrimSpace(parts[1])
		parts[1] = strings.Trim(parts[1], "\"'")
		parts[1] = strings.Split(parts[1], "#")[0]
		env[parts[0]] = parts[1]
	}

	selected := make(map[string]bool)
	for k := range env {
		selected[k] = true
	}

	m := secretModel{
		secrets:  env,
		selected: selected,
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("error running TUI: %w", err)
	}

	finalSecretModel := finalModel.(secretModel)
	if finalSecretModel.quitting {
		return nil, fmt.Errorf("selection cancelled")
	}

	filteredEnv := make(map[string]string)
	for k, v := range env {
		if finalSecretModel.selected[k] {
			filteredEnv[k] = v
		}
	}

	return filteredEnv, nil
}
