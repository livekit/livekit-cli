package provider

import (
	"github.com/pion/webrtc/v3"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

type VideoLooper interface {
	lksdk.SampleProvider
	Codec() webrtc.RTPCodecCapability
	ToLayer(quality livekit.VideoQuality) *livekit.VideoLayer
}
