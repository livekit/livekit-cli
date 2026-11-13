package main

import (
	"context"
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/urfave/cli/v3"
)

func runMergeSIPMedia(t *testing.T, args []string, dst **livekit.SIPMediaConfig) error {
	t.Helper()
	var mergeErr error
	cmd := &cli.Command{
		Name:  "test",
		Flags: appendSIPMediaFlags(nil),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			mergeErr = mergeSIPMediaConfig(cmd, dst)
			return nil
		},
	}
	if err := cmd.Run(context.Background(), append([]string{"test"}, args...)); err != nil {
		t.Fatal(err)
	}
	return mergeErr
}

func TestMergeSIPMediaConfigKeepsJSONValuesWithoutFlags(t *testing.T) {
	enc := livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_REQUIRE
	fromJSON := &livekit.SIPMediaConfig{Encryption: &enc}
	media := fromJSON
	if err := runMergeSIPMedia(t, nil, &media); err != nil {
		t.Fatal(err)
	}
	if media != fromJSON {
		t.Fatalf("media from JSON was discarded: got %v", media)
	}
}

func TestMergeSIPMediaConfigFlagsOverride(t *testing.T) {
	media := &livekit.SIPMediaConfig{}
	if err := runMergeSIPMedia(t, []string{"--media-enc", "SIP_MEDIA_ENCRYPT_REQUIRE"}, &media); err != nil {
		t.Fatal(err)
	}
	if media == nil || media.Encryption == nil || *media.Encryption != livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_REQUIRE {
		t.Fatalf("flag not applied: got %v", media)
	}
}
