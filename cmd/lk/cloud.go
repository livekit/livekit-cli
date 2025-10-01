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
	"github.com/pkg/browser"
	"github.com/urfave/cli/v3"

	authutil "github.com/livekit/livekit-cli/v2/pkg/auth"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
)

type ClaimAccessKeyResponse struct {
	Key         string
	Secret      string
	ProjectId   string
	ProjectName string
	OwnerId     string
	Description string
	URL         string
}

const (
	createTokenEndpoint = "/cli/auth"
	claimKeyEndpoint    = "/cli/claim"
	confirmAuthEndpoint = "/cli/confirm-auth"
	revokeKeyEndpoint   = "/cli/revoke"
)

var (
	revoke        bool
	timeout       int = 60 * 15
	interval      int = 4
	authClient    AuthClient
	CloudCommands = []*cli.Command{
		{
			Name:  "cloud",
			Usage: "Interact with LiveKit Cloud services",
			Commands: []*cli.Command{
				{
					Name:   "auth",
					Usage:  "Authenticate LiveKit Cloud account to link your projects",
					Before: initAuth,
					Action: handleAuth,
					Flags: []cli.Flag{
						&cli.BoolFlag{
							Name:        "revoke",
							Aliases:     []string{"R"},
							Destination: &revoke,
						},
						&cli.IntFlag{
							Name:        "timeout",
							Aliases:     []string{"t"},
							Usage:       "Number of `SECONDS` to attempt authentication before giving up",
							Destination: &timeout,
							Value:       60 * 15,
						},
						&cli.IntFlag{
							Name:        "poll-interval",
							Aliases:     []string{"i"},
							Usage:       "Number of `SECONDS` between poll requests to verify authentication",
							Destination: &interval,
							Value:       4,
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

func (a *AuthClient) ClaimCliKey(ctx context.Context) (*ClaimAccessKeyResponse, error) {
	if a.verificationToken.Token == "" || time.Now().Unix() > a.verificationToken.Expires {
		return nil, errors.New("session expired")
	}

	reqURL, err := url.Parse(a.baseURL + claimKeyEndpoint)
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

	ak := &ClaimAccessKeyResponse{}
	err = json.NewDecoder(resp.Body).Decode(&ak)
	if err != nil {
		return nil, err
	}

	return ak, nil
}

func (a *AuthClient) Deauthenticate(ctx context.Context, projectName, token string) error {
	reqURL, err := url.Parse(a.baseURL + revokeKeyEndpoint)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL.String(), nil)
	req.Header = authutil.NewHeaderWithToken(token)
	if err != nil {
		return err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return errors.New("access denied")
	}
	return cliConfig.RemoveProject(projectName)
}

func NewAuthClient(client *http.Client, baseURL string) *AuthClient {
	a := &AuthClient{
		client:  client,
		baseURL: baseURL,
	}
	return a
}

func initAuth(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	authClient = *NewAuthClient(&http.Client{}, serverURL)
	return nil, nil
}

func handleAuth(ctx context.Context, cmd *cli.Command) error {
	if revoke {
		if _, err := loadProjectConfig(ctx, cmd); err != nil {
			return err
		}
		token, err := requireToken(ctx, cmd)
		if err != nil {
			return err
		}
		return authClient.Deauthenticate(ctx, project.Name, token)
	}
	return tryAuthIfNeeded(ctx, cmd)
}

func requireToken(_ context.Context, cmd *cli.Command) (string, error) {
	if project == nil {
		var err error
		project, err = loadProjectDetails(cmd)
		if err != nil {
			return "", err
		}
	}

	// construct a token from the chosen project, using the hashed secret as the identity
	// as a means of preventing any old token generated with this key/secret pair from
	// deleting it
	hash, err := util.HashString(project.APISecret)
	if err != nil {
		return "", err
	}
	at := auth.NewAccessToken(project.APIKey, project.APISecret).SetIdentity(hash)
	token, err := at.ToJWT()
	if err != nil {
		return "", err
	}

	return token, nil
}

func tryAuthIfNeeded(ctx context.Context, cmd *cli.Command) error {
	if _, err := loadProjectConfig(ctx, cmd); err != nil {
		return err
	}

	// get devicename
	if err := huh.NewForm(huh.NewGroup(huh.NewInput().
		Title("What is the name of this device?").
		Value(&cliConfig.DeviceName).
		WithTheme(util.Theme))).
		Run(); err != nil {
		return err
	}

	// remember device name for next time
	if err := cliConfig.PersistIfNeeded(); err != nil {
		return err
	}
	fmt.Printf("Device [%s]\n", util.Accented(cliConfig.DeviceName))

	// request token
	fmt.Println("Requesting verification token...")
	token, err := authClient.GetVerificationToken(cliConfig.DeviceName)
	if err != nil {
		return err
	}

	authURL, err := generateConfirmURL(token.Token)
	if err != nil {
		return err
	}

	// poll for keys
	fmt.Printf("Please confirm access by visiting:\n\n   %s\n\n", authURL.String())
	_ = browser.OpenURL(authURL.String()) // discard result; this will fail in headless environments

	var ak *ClaimAccessKeyResponse
	err = util.Await(
		"Awaiting confirmation...",
		ctx,
		func(ctx context.Context) error {
			var pollErr error
			ak, pollErr = pollClaim(ctx, cmd)
			return pollErr
		},
	)
	if err != nil {
		return err
	}

	if ak == nil {
		return errors.New("operation cancelled")
	}

	fmt.Printf("Authenticated project [%s]\n", util.Accented(ak.ProjectName))

	// if other authed projects, ask if this should be the default project
	isDefault := len(cliConfig.Projects) == 0
	if !isDefault {
		if err := huh.NewConfirm().
			Title("Make this project default?").
			Value(&isDefault).
			Inline(true).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}
	}

	// make sure name is unique
	name, err := util.URLSafeName(ak.URL)
	if err != nil {
		return err
	}
	if cliConfig.ProjectExists(name) {
		if err := huh.NewInput().
			Title("Choose a different alias").
			Description(fmt.Sprintf("You've already authenticated a project with the alias %q.", name)).
			Value(&name).
			Validate(func(s string) error {
				if cliConfig.ProjectExists(s) {
					return errors.New("project name already exists")
				}
				return nil
			}).
			WithTheme(util.Theme).
			Run(); err != nil {
			return err
		}
	}

	// persist to config file
	cliConfig.Projects = append(cliConfig.Projects, config.ProjectConfig{
		Name:      name,
		ProjectId: ak.ProjectId,
		APIKey:    ak.Key,
		APISecret: ak.Secret,
		URL:       ak.URL,
	})

	if isDefault {
		cliConfig.DefaultProject = name
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

func pollClaim(ctx context.Context, _ *cli.Command) (*ClaimAccessKeyResponse, error) {
	claim := make(chan *ClaimAccessKeyResponse)
	cancel := make(chan error)

	// every <interval> seconds, poll
	go func() {
		for {
			time.Sleep(time.Duration(interval) * time.Second)
			ak, err := authClient.ClaimCliKey(ctx)
			if err != nil {
				cancel <- err
				return
			}
			if ak != nil {
				claim <- ak
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
