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
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/twitchtv/twirp"
)

// Client is a wrapper around the lksdk.AgentClient that provides a simpler interface for creating and deploying agents.
type Client struct {
	*lksdk.AgentClient
	projectURL string
	apiKey     string
	apiSecret  string
	agentsURL  string
	httpClient *http.Client
	logger     logger.Logger
}

// New returns a new Client with the given project URL, API key, and API secret.
func New(opts ...ClientOption) (*Client, error) {
	client := &Client{
		logger: logger.GetLogger(),
	}
	for _, opt := range opts {
		opt(client)
	}
	if client.projectURL == "" {
		return nil, fmt.Errorf("project credentials are required")
	}
	agentClient, err := lksdk.NewAgentClient(client.projectURL, client.apiKey, client.apiSecret, twirp.WithClientHooks(&twirp.ClientHooks{
		RequestPrepared: func(ctx context.Context, req *http.Request) (context.Context, error) {
			setLivekitVersionHeader(req)
			return ctx, nil
		},
	}))
	if err != nil {
		return nil, err
	}
	client.AgentClient = agentClient
	client.agentsURL = client.getAgentsURL()
	if client.httpClient == nil {
		client.httpClient = &http.Client{}
	}
	return client, nil
}

// ClientOption provides a way to configure the Client.
type ClientOption func(*Client)

// WithLogger sets the logger for the Client.
func WithLogger(logger logger.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithProject sets the livekit project credentials for the Client.
func WithProject(projectURL, apiKey, apiSecret string) ClientOption {
	return func(c *Client) {
		c.projectURL = projectURL
		c.apiKey = apiKey
		c.apiSecret = apiSecret
	}
}

// WithHTTPClient sets the http client for the Client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// CreateAgent creates a new agent by building from source.
func (c *Client) CreateAgent(
	ctx context.Context,
	workingDir string,
	secrets []*lkproto.AgentSecret,
	regions []string,
	excludeFiles []string,
) (*lkproto.CreateAgentResponse, error) {
	resp, err := c.AgentClient.CreateAgent(ctx, &lkproto.CreateAgentRequest{
		Secrets: secrets,
		Regions: regions,
	})
	if err != nil {
		return nil, err
	}
	if err := c.uploadAndBuild(ctx, resp.AgentId, resp.PresignedUrl, workingDir, excludeFiles); err != nil {
		return nil, err
	}
	return resp, nil
}

// DeployAgent deploys new agent by building from source.
func (c *Client) DeployAgent(
	ctx context.Context,
	agentID string,
	workingDir string,
	secrets []*lkproto.AgentSecret,
	excludeFiles []string,
) error {
	resp, err := c.AgentClient.DeployAgent(ctx, &lkproto.DeployAgentRequest{
		AgentId: agentID,
		Secrets: secrets,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("failed to deploy agent: %s", resp.Message)
	}
	return c.uploadAndBuild(ctx, agentID, resp.PresignedUrl, workingDir, excludeFiles)
}

// uploadAndBuild uploads the source and triggers remote build
func (c *Client) uploadAndBuild(
	ctx context.Context,
	agentID string,
	presignedUrl string,
	workingDir string,
	excludeFiles []string,
) error {
	projectType, err := DetectProjectType(workingDir)
	if err != nil {
		return err
	}
	if err := UploadTarball(
		workingDir,
		presignedUrl,
		excludeFiles,
		projectType,
	); err != nil {
		return err
	}
	if err := c.Build(ctx, agentID); err != nil {
		return err
	}
	return nil
}

func (c *Client) getAgentsURL() string {
	agentsURL := c.projectURL
	if strings.HasPrefix(agentsURL, "ws") {
		agentsURL = strings.Replace(agentsURL, "ws", "http", 1)
	}
	if os.Getenv("LK_AGENTS_URL") != "" {
		agentsURL = os.Getenv("LK_AGENTS_URL")
	} else if !strings.Contains(agentsURL, "localhost") && !strings.Contains(agentsURL, "127.0.0.1") {
		pattern := `^https://[a-zA-Z0-9\-]+\.`
		re := regexp.MustCompile(pattern)
		agentsURL = re.ReplaceAllString(agentsURL, "https://agents.")
	}
	return agentsURL
}

func (c *Client) newRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if err := c.setAuthToken(req); err != nil {
		return nil, err
	}
	setLivekitVersionHeader(req)
	return req, nil
}

func (c *Client) setAuthToken(req *http.Request) error {
	at := auth.NewAccessToken(c.apiKey, c.apiSecret)
	at.SetAgentGrant(&auth.AgentGrant{Admin: true})
	token, err := at.ToJWT()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	return nil
}

func setLivekitVersionHeader(req *http.Request) {
	req.Header.Set("X-LIVEKIT-CLI-VERSION", livekitcli.Version)
}
