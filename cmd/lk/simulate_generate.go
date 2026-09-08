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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// defaultScenariosFile is where `simulate` looks when --scenarios is omitted,
// and where derived scenarios are saved.
const defaultScenariosFile = "scenarios.yaml"

const recentSessionsLimit = 20

var simulateGenerateCommand = &cli.Command{
	Name:            "generate",
	Usage:           "Turn recent agent sessions into scenarios and save them. Nothing is run",
	ArgsUsage:       "[SESSION_ID...]",
	Description:     "Without SESSION_IDs, pick from the project's recent sessions. Scenarios are appended to the --scenarios file (default scenarios.yaml).",
	HideHelpCommand: true,
	Action: func(ctx context.Context, cmd *cli.Command) error {
		pc := simulateProjectConfig
		group, err := deriveScenarios(ctx, pc, cmd.Args().Slice())
		if err != nil {
			return err
		}
		path := cmd.String("scenarios")
		if path == "" {
			path = defaultScenariosFile
		}
		return saveScenarioGroup(path, group)
	},
}

// scenariosPathOrDefault resolves --scenarios, falling back to scenarios.yaml
// in the working directory when it exists; "" means no file.
func scenariosPathOrDefault(cmd *cli.Command) string {
	if path := cmd.String("scenarios"); path != "" {
		return path
	}
	if _, err := os.Stat(defaultScenariosFile); err == nil {
		return defaultScenariosFile
	}
	return ""
}

// deriveScenarios has the cloud derive one scenario per recorded session.
// Without IDs the user picks from the project's recent sessions, which needs a
// terminal. A session the cloud can't derive from is skipped with a warning.
func deriveScenarios(ctx context.Context, pc *config.ProjectConfig, sessionIDs []string) (*livekit.ScenarioGroup, error) {
	if len(sessionIDs) == 0 {
		if !isInteractive() {
			return nil, errors.New("pass one or more session IDs (from the dashboard's Sessions page) to generate scenarios non-interactively")
		}
		sessions, err := listRecentSessions(ctx, pc)
		if err != nil {
			return nil, err
		}
		if len(sessions) == 0 {
			return nil, errors.New("no finished sessions in this project yet; talk to your agent first, then re-run")
		}
		sessionIDs, err = pickSessions(sessions)
		if err != nil {
			return nil, err
		}
	}

	client := lksdk.NewAgentSimulationClient(serverURL, pc.APIKey, pc.APISecret)
	group := &livekit.ScenarioGroup{Name: scenarioGroupName()}
	for _, id := range sessionIDs {
		var scenario *livekit.Scenario
		err := out.Await("Deriving a scenario from session "+id, ctx, func(ctx context.Context) error {
			resp, err := client.CreateScenarioFromSession(ctx, &livekit.Scenario_CreateFromSession_Request{
				ProjectId: pc.ProjectId,
				RoomId:    id,
			})
			if err != nil {
				return err
			}
			scenario = resp.GetScenario()
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			out.Warnf("Warning: skipping session %s: %v", id, err)
			continue
		}
		group.Scenarios = append(group.Scenarios, scenario)
	}
	if len(group.Scenarios) == 0 {
		return nil, errors.New("no scenarios could be derived from the selected sessions")
	}

	preview, err := scenarioGroupToYAML(group)
	if err != nil {
		return nil, err
	}
	out.Result(string(preview))
	return group, nil
}

// scenarioGroupName names a new group after the project directory, which is
// what the dashboard's runs list shows.
func scenarioGroupName() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Base(wd)
}

// listRecentSessions returns the project's newest finished sessions; a live
// session has no chat history to derive from yet.
func listRecentSessions(ctx context.Context, pc *config.ProjectConfig) ([]*analyticsSession, error) {
	query := url.Values{}
	query.Set("limit", fmt.Sprint(recentSessionsLimit))
	query.Set("status", "closed")
	body, err := analyticsGET(ctx, pc, "sessions", query)
	if err != nil {
		return nil, fmt.Errorf("failed to list recent sessions: %w", err)
	}
	var res analyticsListResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to parse sessions response: %w", err)
	}
	return res.Sessions, nil
}

func pickSessions(sessions []*analyticsSession) ([]string, error) {
	var options []huh.Option[string]
	for _, s := range sessions {
		label := fmt.Sprintf("%s  %s  %d participants", emptyDash(s.RoomName), emptyDash(s.CreatedAt), s.NumParticipants)
		options = append(options, huh.NewOption(label, s.SessionID))
	}
	var picked []string
	err := huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().
		Title("Which sessions should become scenarios?").
		Description("Each session is turned into one scenario: what the user did, and what the agent is expected to do.").
		Options(options...).
		Height(len(options) + 2).
		Value(&picked))).
		WithTheme(util.FormTheme()).
		Run()
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, errors.New("no sessions selected")
	}
	return picked, nil
}

// saveScenarioGroup appends the group's scenarios to the scenarios file at
// path, creating it when missing. An existing file keeps its name.
func saveScenarioGroup(path string, group *livekit.ScenarioGroup) error {
	merged := group
	if existing, err := loadScenarioGroup(path); err == nil {
		existing.Scenarios = append(existing.Scenarios, group.Scenarios...)
		merged = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := scenarioGroupToYAML(merged)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	noun := "scenarios"
	if len(group.Scenarios) == 1 {
		noun = "scenario"
	}
	out.Statusf("Saved %d %s to %s", len(group.Scenarios), noun, path)
	return nil
}
