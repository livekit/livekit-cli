package agentfs

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
)

func LogHelper(ctx context.Context, id string, name string, logType string, projectConfig *config.ProjectConfig) error {
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
	if id != "" {
		params.Add("agent_id", id)
	} else {
		params.Add("agent_name", name)
	}
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
			fmt.Println(line)
		}
	}
}
