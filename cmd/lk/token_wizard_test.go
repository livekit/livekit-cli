//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/creack/pty"
	"github.com/livekit/protocol/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

func TestTokenWizardIngressPermissions(t *testing.T) {
	// Use the accessible wizard with terminal stdin so SkipPrompts stays false.
	t.Setenv("TERM", "dumb")
	previousProject := project
	project = &config.ProjectConfig{APIKey: "test-key", APISecret: "test-secret"}
	t.Cleanup(func() { project = previousProject })

	for _, tc := range []struct {
		name  string
		input string
		grant auth.VideoGrant
	}{
		{"ingress", "6\n0\n", auth.VideoGrant{IngressAdmin: true}},
		{"egress", "5\n0\n", auth.VideoGrant{RoomRecord: true}},
		{"join, egress and ingress", "3\n5\n6\n0\n", auth.VideoGrant{RoomJoin: true, RoomRecord: true, IngressAdmin: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminal, stdin, err := pty.Open()
			require.NoError(t, err)
			previousStdin := os.Stdin
			os.Stdin = stdin
			t.Cleanup(func() {
				os.Stdin = previousStdin
				_ = stdin.Close()
				_ = terminal.Close()
			})
			_, err = terminal.WriteString(tc.input)
			require.NoError(t, err)

			var stdout bytes.Buffer
			cmd := &cli.Command{
				Name:      "lk",
				Action:    createToken,
				Writer:    &stdout,
				ErrWriter: io.Discard,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "room"},
					&cli.StringFlag{Name: "identity"},
					&cli.BoolFlag{Name: "token-only"},
					&cli.StringFlag{Name: "valid-for", Value: "5m"},
				},
			}
			require.NoError(t, cmd.Run(context.Background(), []string{
				"lk", "--room", "test-room", "--identity", "test-id", "--token-only",
			}))
			verifier, err := auth.ParseAPIToken(stdout.String())
			require.NoError(t, err)
			_, grants, err := verifier.Verify("test-secret")
			require.NoError(t, err)
			tc.grant.Room = "test-room"
			tc.grant.SetCanUpdateOwnMetadata(false)
			assert.Equal(t, &tc.grant, grants.Video)
		})
	}
}
