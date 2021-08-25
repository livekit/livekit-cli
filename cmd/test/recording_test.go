package test

import (
	"io/ioutil"
	"testing"

	livekit "github.com/livekit/server-sdk-go/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestUnmarshal(t *testing.T) {
	config, err := ioutil.ReadFile("config.json")
	require.NoError(t, err)

	req := &livekit.StartRecordingRequest{}
	require.NoError(t, protojson.Unmarshal(config, req))

	require.Equal(t, "speaker-dark", req.Input.Template.Layout)
	require.Equal(t, "bucket", req.Output.S3.Bucket)
}
