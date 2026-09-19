package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentDeployRejectsPrebuiltDeployment(t *testing.T) {
	missingFile := filepath.Join(t.TempDir(), "missing")
	for _, tt := range []struct {
		name       string
		args       []string
		deployment string
	}{
		{"tar", []string{"--image-tar", missingFile, "--deployment", "staging"}, "staging"},
		{"image", []string{"--image", "local:latest", "--deployment", "staging"}, "staging"},
		{"alias before image", []string{"-d", "staging", "--image", "local:latest"}, "staging"},
		{"alias before tar", []string{"-d", "staging", "--image-tar", missingFile}, "staging"},
		{"inline secrets", []string{"--image-tar", missingFile, "--secrets", "KEY=value", "-d", "staging"}, "staging"},
		{"secrets file", []string{"--image-tar", missingFile, "--secrets-file", missingFile, "-d", "staging"}, "staging"},
		{"secret mount", []string{"--image", "local:latest", "--secret-mount", missingFile, "-d", "staging"}, "staging"},
		{"custom name", []string{"--image", "local:latest", "-d", "qa-canary"}, "qa-canary"},
		{"literal production", []string{"--image", "local:latest", "-d", "production"}, "production"},
		{"case preserved", []string{"--image-tar", missingFile, "-d", "Production"}, "Production"},
		{"whitespace deployment", []string{"--image", "local:latest", "-d", " "}, " "},
		{"whitespace image", []string{"--image", " ", "-d", "staging"}, "staging"},
		{"both images", []string{"--image", "local:latest", "--image-tar", missingFile, "-d", "staging"}, "staging"},
		{"quiet", []string{"--quiet", "--image-tar", missingFile, "-d", "staging"}, "staging"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := isolateAgentDeploySetup(t)
			actionCalled := false
			app := agentDeployTestCommand(t, func(context.Context, *cli.Command) error {
				actionCalled = true
				return nil
			})

			err := app.Run(context.Background(), append([]string{"deploy"}, tt.args...))

			require.ErrorContains(t, err, "is not supported with --image or --image-tar")
			require.ErrorContains(t, err, "prebuilt image deployments use the default production deployment")
			require.ErrorContains(t, err, "omit --deployment only if production is intended")
			require.Equal(t, tt.deployment, app.String("deployment"))
			require.False(t, actionCalled, "must reject before secrets, tar/Docker loading, or upload")
			require.Nil(t, agentsClient, "must reject before creating the agent client")
			require.Zero(t, *requests, "must not acquire a push target or make any other HTTP request")
		})
	}
}

func TestAgentDeployRejectsPrebuiltDeploymentBeforeProjectValidation(t *testing.T) {
	requests := isolateAgentDeploySetup(t)
	project.URL = "invalid-project-url"
	actionCalled := false
	app := agentDeployTestCommand(t, func(context.Context, *cli.Command) error {
		actionCalled = true
		return nil
	})

	err := app.Run(context.Background(), []string{"deploy", "--image", "local:latest", "-d", "staging"})

	require.ErrorContains(t, err, "--deployment \"staging\" is not supported")
	require.False(t, actionCalled)
	require.Nil(t, agentsClient)
	require.Zero(t, *requests)
}

func TestAgentDeploySupportedInputsReachAction(t *testing.T) {
	for _, tt := range []struct {
		name       string
		args       []string
		deployment string
	}{
		{"default tar", []string{"--image-tar", "image.tar"}, ""},
		{"default image", []string{"--image", "local:latest"}, ""},
		{"empty deployment tar", []string{"--image-tar", "image.tar", "--deployment="}, ""},
		{"empty deployment image", []string{"--image", "local:latest", "-d", ""}, ""},
		{"source staging", []string{"--deployment", "staging"}, "staging"},
		{"source alias", []string{"-d", "staging"}, "staging"},
		{"source literal production", []string{"--deployment", "production"}, "production"},
		{"empty image values", []string{"--image=", "--image-tar=", "-d", "staging"}, "staging"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := isolateAgentDeploySetup(t)
			actionCalled := false
			app := agentDeployTestCommand(t, func(_ context.Context, cmd *cli.Command) error {
				actionCalled = true
				require.Equal(t, tt.deployment, cmd.String("deployment"))
				return nil
			})

			err := app.Run(context.Background(), append([]string{"deploy"}, tt.args...))

			require.NoError(t, err)
			require.True(t, actionCalled)
			require.NotNil(t, agentsClient, "accepted input must still run normal client setup")
			require.Zero(t, *requests)
		})
	}
}

// Keep the registered command's flags and Before hook. Only replace the action
// so these command-boundary tests cannot update secrets, load images, or deploy.
func agentDeployTestCommand(t *testing.T, action cli.ActionFunc) *cli.Command {
	t.Helper()
	agent := findCommandByName(AgentCommands, "agent")
	require.NotNil(t, agent)
	deploy := findCommandByName(agent.Commands, "deploy")
	require.NotNil(t, deploy)
	cmd := *deploy
	cmd.Action = action
	cmd.Writer, cmd.ErrWriter = io.Discard, io.Discard
	cmd.Flags = nil
	// Flags carry parsing state. Copy each definition to keep cases independent
	// while exercising the production defaults and aliases.
	for _, flag := range append(append([]cli.Flag{}, deploy.Flags...), quietFlag) {
		value := reflect.ValueOf(flag)
		copy := reflect.New(value.Elem().Type())
		copy.Elem().Set(value.Elem())
		cmd.Flags = append(cmd.Flags, copy.Interface().(cli.Flag))
	}
	return &cmd
}

// The CLI uses package globals; these tests must not run in parallel.
func isolateAgentDeploySetup(t *testing.T) *int {
	t.Helper()
	oldProject, oldConfig, oldClient, oldDir, oldOut := project, lkConfig, agentsClient, workingDir, out
	oldTransport, oldHTTPClient := http.DefaultTransport, http.DefaultClient
	t.Cleanup(func() {
		project, lkConfig, agentsClient, workingDir, out = oldProject, oldConfig, oldClient, oldDir, oldOut
		http.DefaultTransport, http.DefaultClient = oldTransport, oldHTTPClient
	})
	project = &config.ProjectConfig{URL: "https://fixture.livekit.cloud", APIKey: "fixture-key", APISecret: "fixture-secret"}
	lkConfig = config.NewLiveKitTOML("fixture").WithDefaultAgent()
	lkConfig.Agent.ID = "test-agent"
	agentsClient = nil
	workingDir = t.TempDir()
	out = util.NewPrinter(io.Discard, io.Discard, false)
	t.Setenv("LK_AGENTS_URL", "http://127.0.0.1:1")
	requests := 0
	transport := agentDeployTestTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected HTTP request during deploy setup test")
	})
	http.DefaultTransport = transport
	http.DefaultClient = &http.Client{Transport: transport}
	return &requests
}

type agentDeployTestTransport func(*http.Request) (*http.Response, error)

func (f agentDeployTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
