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

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
)

func Build(ctx context.Context, id string, name string, action string, projectConfig *config.ProjectConfig) error {
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
	params.Add("action", action)
	fullUrl := fmt.Sprintf("%s/build?%s", agentsUrl, params.Encode())

	at := auth.NewAccessToken(projectConfig.APIKey, projectConfig.APISecret)
	at.SetAgentGrant(&auth.AgentGrant{
		Admin: true,
	})
	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", fullUrl, nil)
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
		return fmt.Errorf("failed to build agent: %s", resp.Status)
	}

	ch := make(chan *bkclient.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		display, err := progressui.NewDisplay(os.Stderr, "auto")
		if err != nil {
			return err
		}
		_, err = display.UpdateFrom(context.Background(), ch)
		return err
	})

	eg.Go(func() error {
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "BUILD ERROR:") {
				return fmt.Errorf("%s", strings.TrimPrefix(line, "BUILD ERROR: "))
			}

			var status bkclient.SolveStatus
			if err := json.Unmarshal(scanner.Bytes(), &status); err != nil {
				return fmt.Errorf("decode error: %w", err)
			}
			select {
			case ch <- &status:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return scanner.Err()
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}
