package main

import (
	"context"
	"io"
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentDeployRejectsImageDeployment(t *testing.T) {
	cmd := agentDeployTestCommand(t)

	err := cmd.Run(context.Background(), []string{"deploy", "--image", "local:latest", "--deployment", "staging"})

	require.ErrorContains(t, err, "--deployment \"staging\" is not supported with --image or --image-tar")
}

func TestAgentDeployRejectsImageTarDeployment(t *testing.T) {
	cmd := agentDeployTestCommand(t)

	err := cmd.Run(context.Background(), []string{"deploy", "--image-tar", "image.tar", "--deployment", "staging"})

	require.ErrorContains(t, err, "--deployment \"staging\" is not supported with --image or --image-tar")
	require.Nil(t, agentsClient, "rejection must happen before client creation")
}

func TestAgentDeployAllowsPrebuiltWithoutDeployment(t *testing.T) {
	cmd := agentDeployTestCommand(t)

	err := cmd.Run(context.Background(), []string{"deploy", "--image-tar", "image.tar"})

	require.NoError(t, err)
}

func TestAgentDeployAllowsNamedSourceDeployment(t *testing.T) {
	cmd := agentDeployTestCommand(t)

	err := cmd.Run(context.Background(), []string{"deploy", "--deployment", "staging"})

	require.NoError(t, err)
}

func agentDeployTestCommand(t *testing.T) *cli.Command {
	t.Helper()
	// Client setup uses package globals, so these tests must not run in parallel.
	oldProject, oldConfig, oldClient, oldDir := project, lkConfig, agentsClient, workingDir
	t.Cleanup(func() {
		project, lkConfig, agentsClient, workingDir = oldProject, oldConfig, oldClient, oldDir
	})
	project = &config.ProjectConfig{URL: "https://fixture.livekit.cloud", APIKey: "fixture-key", APISecret: "fixture-secret"}
	lkConfig, agentsClient = nil, nil
	workingDir = t.TempDir()

	agent := findCommandByName(AgentCommands, "agent")
	deploy := findCommandByName(agent.Commands, "deploy")
	return &cli.Command{
		Name:      "deploy",
		Before:    deploy.Before,
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "image"},
			&cli.StringFlag{Name: "image-tar"},
			&cli.StringFlag{Name: "deployment"},
		},
		// Exercise the registered Before hook without loading or deploying an image.
		Action: func(context.Context, *cli.Command) error { return nil },
	}
}
