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
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type savePhase int

const (
	savePhaseLoading  savePhase = iota
	savePhaseList
	savePhaseNewGroup
	savePhaseSaving
	savePhaseSuccess
	savePhaseError
)

type saveOverlay struct {
	active     bool
	client     *lksdk.AgentSimulationClient
	job        *livekit.SimulationRun_Job
	phase      savePhase
	groups     []*livekit.ScenarioGroup
	cursor     int
	groupInput string
	err        error
	successMsg string
	spinnerIdx int
	width      int
}

// --- Messages ---

type saveGroupsLoadedMsg struct {
	groups []*livekit.ScenarioGroup
	err    error
}

type scenarioSavedMsg struct {
	groupLabel string
	err        error
}

type saveDismissMsg struct{}

type saveSpinnerTickMsg struct{}

func saveSpinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return saveSpinnerTickMsg{}
	})
}

// --- Lifecycle ---

func (s *saveOverlay) start(client *lksdk.AgentSimulationClient, job *livekit.SimulationRun_Job, width int) {
	*s = saveOverlay{
		active: true,
		client: client,
		job:    job,
		phase:  savePhaseLoading,
		width:  width,
	}
}

// --- Async commands ---

func (s *saveOverlay) fetchGroupsCmd() tea.Cmd {
	client := s.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListScenarioGroups(ctx, &livekit.ScenarioGroup_List_Request{})
		if err != nil {
			return saveGroupsLoadedMsg{err: err}
		}
		return saveGroupsLoadedMsg{groups: resp.GetScenarioGroups()}
	}
}

func (s *saveOverlay) saveScenarioCmd(groupID, groupLabel string) tea.Cmd {
	client := s.client
	job := s.job
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.CreateScenario(ctx, &livekit.Scenario_Create_Request{
			GroupId:           groupID,
			Label:             job.Label,
			Instructions:      job.Instructions,
			AgentExpectations: job.AgentExpectations,
		})
		if err != nil {
			return scenarioSavedMsg{err: err}
		}
		return scenarioSavedMsg{groupLabel: groupLabel}
	}
}

func (s *saveOverlay) createGroupAndSaveCmd(groupLabel string) tea.Cmd {
	client := s.client
	job := s.job
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		groupResp, err := client.CreateScenarioGroup(ctx, &livekit.ScenarioGroup_Create_Request{
			Label: groupLabel,
		})
		if err != nil {
			return scenarioSavedMsg{err: fmt.Errorf("create group: %w", err)}
		}
		_, err = client.CreateScenario(ctx, &livekit.Scenario_Create_Request{
			GroupId:           groupResp.GetScenarioGroup().GetId(),
			Label:             job.Label,
			Instructions:      job.Instructions,
			AgentExpectations: job.AgentExpectations,
		})
		if err != nil {
			return scenarioSavedMsg{err: err}
		}
		return scenarioSavedMsg{groupLabel: groupLabel}
	}
}

// --- Message handling ---

func (s *saveOverlay) handleMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case saveGroupsLoadedMsg:
		if msg.err != nil {
			s.phase = savePhaseError
			s.err = msg.err
			return nil
		}
		s.groups = msg.groups
		s.phase = savePhaseList
		s.cursor = 0
		return nil

	case scenarioSavedMsg:
		if msg.err != nil {
			s.phase = savePhaseError
			s.err = msg.err
			return nil
		}
		s.phase = savePhaseSuccess
		s.successMsg = msg.groupLabel
		return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return saveDismissMsg{}
		})

	case saveDismissMsg:
		s.active = false
		return nil

	case saveSpinnerTickMsg:
		if s.active && (s.phase == savePhaseLoading || s.phase == savePhaseSaving) {
			s.spinnerIdx++
			return saveSpinnerTickCmd()
		}
		return nil
	}
	return nil
}

// --- Key handling ---

func (s *saveOverlay) handleKey(key string) tea.Cmd {
	switch s.phase {
	case savePhaseLoading, savePhaseSaving:
		if key == "esc" {
			s.active = false
		}
		return nil

	case savePhaseSuccess:
		s.active = false
		return nil

	case savePhaseError:
		switch key {
		case "esc", "q":
			s.active = false
		case "r":
			s.phase = savePhaseLoading
			s.err = nil
			return tea.Batch(s.fetchGroupsCmd(), saveSpinnerTickCmd())
		}
		return nil

	case savePhaseNewGroup:
		switch key {
		case "esc":
			s.phase = savePhaseList
			s.groupInput = ""
		case "enter":
			name := strings.TrimSpace(s.groupInput)
			if name != "" {
				s.phase = savePhaseSaving
				return tea.Batch(s.createGroupAndSaveCmd(name), saveSpinnerTickCmd())
			}
		case "backspace":
			if len(s.groupInput) > 0 {
				s.groupInput = s.groupInput[:len(s.groupInput)-1]
			}
		default:
			if len(key) == 1 && key[0] >= 32 {
				s.groupInput += key
			}
		}
		return nil

	case savePhaseList:
		maxIdx := len(s.groups) // extra slot for "+ New Group..."
		switch key {
		case "esc", "q":
			s.active = false
		case "up", "shift+tab":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "tab":
			if s.cursor < maxIdx {
				s.cursor++
			}
		case "enter":
			if s.cursor < len(s.groups) {
				group := s.groups[s.cursor]
				s.phase = savePhaseSaving
				return tea.Batch(s.saveScenarioCmd(group.Id, group.Label), saveSpinnerTickCmd())
			}
			// "+ New Group..." selected
			s.phase = savePhaseNewGroup
			s.groupInput = ""
		}
		return nil
	}
	return nil
}

// --- Rendering ---

var (
	saveBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(0, 1)
	saveTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)
	saveSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")).
				Bold(true)
	saveNewGroupStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))
)

func (s *saveOverlay) render() string {
	boxWidth := min(max(s.width-8, 40), 60)

	var b strings.Builder
	title := saveTitleStyle.Render("Save to Scenario Group")
	b.WriteString(title + "\n")

	switch s.phase {
	case savePhaseLoading:
		spinner := simSpinnerFrames[s.spinnerIdx%len(simSpinnerFrames)]
		b.WriteString(fmt.Sprintf("\n  %s Loading scenario groups...\n", cyanStyle.Render(spinner)))
		b.WriteString(dimStyle.Render("\n  ESC cancel"))

	case savePhaseError:
		b.WriteString(fmt.Sprintf("\n  %s %s\n", redStyle.Render("✗"), redStyle.Render(s.err.Error())))
		b.WriteString(dimStyle.Render("\n  r retry · ESC back"))

	case savePhaseList:
		b.WriteString("\n")
		for i, g := range s.groups {
			label := g.Label
			if i == s.cursor {
				b.WriteString(fmt.Sprintf("  %s %s\n", saveSelectedStyle.Render(">"), saveSelectedStyle.Render(label)))
			} else {
				b.WriteString(fmt.Sprintf("    %s\n", label))
			}
		}
		// "+ New Group..." option
		newLabel := "+ New Group..."
		if s.cursor == len(s.groups) {
			b.WriteString(fmt.Sprintf("  %s %s\n", saveNewGroupStyle.Render(">"), saveNewGroupStyle.Render(newLabel)))
		} else {
			b.WriteString(fmt.Sprintf("    %s\n", saveNewGroupStyle.Render(newLabel)))
		}
		b.WriteString(dimStyle.Render("\n  ↑↓ navigate · ENTER select · ESC back"))

	case savePhaseNewGroup:
		b.WriteString(fmt.Sprintf("\n  Group name: %s%s\n", s.groupInput, cyanStyle.Render("│")))
		b.WriteString(dimStyle.Render("\n  ENTER create · ESC cancel"))

	case savePhaseSaving:
		spinner := simSpinnerFrames[s.spinnerIdx%len(simSpinnerFrames)]
		b.WriteString(fmt.Sprintf("\n  %s Saving scenario...\n", cyanStyle.Render(spinner)))

	case savePhaseSuccess:
		b.WriteString(fmt.Sprintf("\n  %s Saved to %s\n", greenStyle.Render("✓"), boldStyle.Render("\""+s.successMsg+"\"")))
	}

	return "\n" + saveBoxStyle.Width(boxWidth).Render(b.String()) + "\n"
}
