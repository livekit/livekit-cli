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

package util

import (
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/pkg/browser"
	"github.com/urfave/cli/v3"

	"github.com/google/go-querystring/query"
)

const (
	MeetURL        = "https://meet.livekit.io/custom"
	ConsoleURLPath = "/projects/%s/agents/console/?"
)

type OpenTarget string

const (
	OpenTargetMeet    OpenTarget = "meet"
	OpenTargetConsole OpenTarget = "console"
)

var (
	options  = []string{string(OpenTargetMeet), string(OpenTargetConsole)}
	OpenFlag = &cli.StringFlag{
		Name:  "open",
		Usage: fmt.Sprintf("Open relevant `APP` in browser, supported options: %v", options),
		Validator: func(input string) error {
			if !slices.Contains(options, input) {
				return fmt.Errorf("invalid open target: %s, supported options: %v", input, options)
			}
			return nil
		},
	}
)

func OpenInMeet(livekitURL, token string) error {
	if token == "" {
		return errors.New("token is required to open in Meet")
	}

	meetURL, err := url.Parse(MeetURL)
	if err != nil {
		return fmt.Errorf("failed to parse Meet URL: %w", err)
	}

	query := meetURL.Query()
	query.Set("liveKitUrl", livekitURL)
	query.Set("token", token)
	meetURL.RawQuery = query.Encode()

	if err := browser.OpenURL(meetURL.String()); err != nil {
		return fmt.Errorf("failed to open Meet URL: %w", err)
	}

	return nil
}

type ConsoleURLParams struct {
	AgentName    string            `url:"agentName,omitempty"`
	JobMetadata  string            `url:"jobMetadata,omitempty"`
	RoomName     string            `url:"roomName,omitempty"`
	RoomMetadata string            `url:"roomMetadata,omitempty"`
	Identity     string            `url:"identity,omitempty"`
	Metadata     string            `url:"metadata,omitempty"`
	Attributes   map[string]string `url:"attributes,omitempty"`
	Hidden       bool              `url:"hidden,omitempty"`
	AutoStart    bool              `url:"autoStart,omitempty"`
}

func OpenInConsole(dashboardURL, projectId string, params *ConsoleURLParams) error {
	ps, err := query.Values(params)
	if err != nil {
		return fmt.Errorf("failed to encode console URL parameters: %w", err)
	}

	if projectId == "" {
		projectId = "p_"
	}

	consoleURL := fmt.Sprintf(dashboardURL+ConsoleURLPath, projectId) + ps.Encode()
	if err := browser.OpenURL(consoleURL); err != nil {
		return fmt.Errorf("failed to open Console URL: %w", err)
	}

	return nil
}
