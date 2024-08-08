// Copyright 2024 LiveKit, Inc.
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
	"net/http"
	"net/url"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/pkg/config"
	"github.com/pkg/browser"
)

const (
	cloudAPIServerURL    = "https://cloud-api.livekit.io"
	cloudDashboardURL    = "https://cloud.livekit.io"
	createTokenEndpoint  = "/cli/auth"
	confirmAuthEndpoint  = "/cli/confirm-auth"
	claimSessionEndpoint = "/cli/claim"
)

var (
	disconnect   bool
	timeout      int64
	interval     int64
	serverURL    string
	dashboardURL string
	authClient   AuthClient
	AuthCommands = []*cli.Command{
		{
			Name:     "cloud",
			Usage:    "Interacting with LiveKit Cloud",
			Category: "Core",
			Commands: []*cli.Command{
				{
					Name:   "auth",
					Usage:  "Authenticate the CLI via the browser to permit advanced actions",
					Before: initAuth,
					Action: handleAuth,
					Flags: []cli.Flag{
						&cli.BoolFlag{
							Name:        "R",
							Aliases:     []string{"revoke"},
							Destination: &disconnect,
						},
						&cli.IntFlag{
							Name:        "t",
							Aliases:     []string{"timeout"},
							Usage:       "Number of `SECONDS` to attempt authentication before giving up",
							Destination: &timeout,
							Value:       60 * 15,
						},
						&cli.IntFlag{
							Name:        "i",
							Aliases:     []string{"poll-interval"},
							Usage:       "Number of `SECONDS` between poll requests to verify authentication",
							Destination: &interval,
							Value:       4,
						},
						&cli.StringFlag{
							// Use "http://cloud-api.livekit.run" in local dev
							Name:        "server-url",
							Value:       cloudAPIServerURL,
							Destination: &serverURL,
							Hidden:      true,
						},
						&cli.StringFlag{
							// Use "https://cloud.livekit.run" in local dev
							Name:        "dashboard-url",
							Value:       cloudDashboardURL,
							Destination: &dashboardURL,
							Hidden:      true,
						},
					},
				},
			},
		},
	}
)

type VerificationToken struct {
	Identifier string
	Token      string
	Expires    int64
	DeviceName string
}

type AuthClient struct {
	client            *http.Client
	baseURL           string
	verificationToken VerificationToken
}

func (a *AuthClient) GetVerificationToken(deviceName string) (*VerificationToken, error) {
	reqURL, err := url.Parse(a.baseURL + createTokenEndpoint)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("device_name", deviceName)
	reqURL.RawQuery = params.Encode()

	resp, err := a.client.Post(reqURL.String(), "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&a.verificationToken)
	if err != nil {
		return nil, err
	}

	return &a.verificationToken, nil
}

func (a *AuthClient) ClaimCliKey(ctx context.Context) (*config.AccessKey, error) {
	if a.verificationToken.Token == "" || time.Now().Unix() > a.verificationToken.Expires {
		return nil, errors.New("session expired")
	}

	reqURL, err := url.Parse(a.baseURL + claimSessionEndpoint)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("t", a.verificationToken.Token)
	reqURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if resp != nil && resp.StatusCode == 404 {
		return nil, errors.New("access denied")
	}
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		// Not yet approved
		return nil, nil
	}

	ak := &config.AccessKey{}
	err = json.NewDecoder(resp.Body).Decode(&ak)
	if err != nil {
		return nil, err
	}

	return ak, nil
}

func (a *AuthClient) Deauthenticate() error {
	// TODO: revoke any session token
	return nil
}

func NewAuthClient(client *http.Client, baseURL string) *AuthClient {
	a := &AuthClient{
		client:  client,
		baseURL: baseURL,
	}
	return a
}

func initAuth(ctx context.Context, cmd *cli.Command) error {
	if err := loadProjectConfig(ctx, cmd); err != nil {
		return err
	}
	authClient = *NewAuthClient(&http.Client{}, serverURL)
	return nil
}

func handleAuth(ctx context.Context, cmd *cli.Command) error {
	if disconnect {
		return authClient.Deauthenticate()
	}
	return tryAuthIfNeeded(ctx, cmd)
}

func tryAuthIfNeeded(ctx context.Context, cmd *cli.Command) error {
	_, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	// name
	var deviceName string
	if err := huh.NewInput().
		Title("What is the name of this device?").
		Value(&deviceName).
		WithTheme(theme).
		Run(); err != nil {
		return err
	}
	fmt.Println("Device:", deviceName)

	// request token
	fmt.Println("Requesting verification token...")
	token, err := authClient.GetVerificationToken(deviceName)
	if err != nil {
		return err
	}

	authURL, err := generateConfirmURL(token.Token)
	if err != nil {
		return err
	}

	// poll for keys
	fmt.Printf("Please confirm access by visiting:\n\n   %s\n\n", authURL.String())

	if err := browser.OpenURL(authURL.String()); err != nil {
		return err
	}

	var key *config.AccessKey
	var pollErr error
	if err := spinner.New().
		Title("Awaiting confirmation...").
		Action(func() {
			key, pollErr = pollClaim(ctx, cmd)
		}).
		Context(ctx).
		Run(); err != nil {
		return err
	}

	if pollErr != nil {
		return pollErr
	}

	var isDefault bool
	if err := huh.NewConfirm().
		Title("Make this project default?").
		Value(&isDefault).
		WithTheme(theme).
		Run(); err != nil {
		return err
	}

	// persist to config file
	cliConfig.Projects = append(cliConfig.Projects, config.ProjectConfig{
		Name:      key.Description,
		APIKey:    key.Key,
		APISecret: key.Secret,
		URL:       "ws://" + key.Project.Subdomain + ".livekit:7800",
	})
	if isDefault {
		cliConfig.DefaultProject = key.Description
	}
	if err = cliConfig.PersistIfNeeded(); err != nil {
		return err
	}

	return err
}

func generateConfirmURL(token string) (*url.URL, error) {
	base, err := url.Parse(dashboardURL + confirmAuthEndpoint)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("t", token)
	base.RawQuery = params.Encode()
	return base, nil
}

func pollClaim(ctx context.Context, _ *cli.Command) (*config.AccessKey, error) {
	claim := make(chan *config.AccessKey)
	cancel := make(chan error)

	// every <interval> seconds, poll
	go func() {
		for {
			time.Sleep(time.Duration(interval) * time.Second)
			session, err := authClient.ClaimCliKey(ctx)
			if err != nil {
				cancel <- err
				return
			}
			if session != nil {
				claim <- session
			}
		}
	}()

	select {
	case <-time.After(time.Duration(timeout) * time.Second):
		return nil, errors.New("session claim timed out")
	case err := <-cancel:
		return nil, err
	case accessKey := <-claim:
		return accessKey, nil
	}
}
