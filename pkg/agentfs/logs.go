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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
)

type APIError struct {
	Message string             `json:"msg"`
	Meta    *map[string]string `json:"meta,omitempty"`
}

func LogHelper(ctx context.Context, id string, logType string, projectConfig *config.ProjectConfig) error {
	if logType == "" {
		logType = "deploy"
	}

	baseUrl := projectConfig.URL
	if strings.HasPrefix(projectConfig.URL, "ws") {
		baseUrl = strings.Replace(projectConfig.URL, "ws", "http", 1)
	}

	var agentsUrl string

	if os.Getenv("LK_AGENTS_URL") != "" {
		agentsUrl = os.Getenv("LK_AGENTS_URL")
	} else if !strings.Contains(baseUrl, "localhost") && !strings.Contains(baseUrl, "127.0.0.1") {
		pattern := `^https://[a-zA-Z0-9\-]+\.`
		re := regexp.MustCompile(pattern)
		agentsUrl = re.ReplaceAllString(baseUrl, "https://agents.")
	} else {
		agentsUrl = baseUrl
	}

	logger.Debugw("Connecting to LK hosted agents on", "url", agentsUrl)

	params := url.Values{}
	params.Add("agent_id", id)
	params.Add("log_type", logType)
	fullUrl := fmt.Sprintf("%s/logs?%s", agentsUrl, params.Encode())

	at := auth.NewAccessToken(projectConfig.APIKey, projectConfig.APISecret)
	at.SetAgentGrant(&auth.AgentGrant{
		Admin: true,
	})
	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("GET", fullUrl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debugw("failed to get logs", "status", resp.Status)

		var errorResponse APIError
		if err := json.NewDecoder(resp.Body).Decode(&errorResponse); err != nil {
			return fmt.Errorf("failed to parse error response: %w", err)
		} else {
			return fmt.Errorf("failed to get logs: %s", errorResponse.Message)
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("scanner error: %w", err)
				}
				return nil
			}

			line := scanner.Text()
			if strings.HasPrefix(line, "ERROR:") {
				return fmt.Errorf("%s", strings.TrimPrefix(line, "ERROR: "))
			}
			fmt.Println(util.Dimmed(line))
		}
	}
}
