package provider

import (
	"github.com/pion/webrtc/v3"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

type Looper interface {
	lksdk.SampleProvider
	Codec() webrtc.RTPCodecCapability
}

type VideoLooper interface {
	Looper
	ToLayer(quality livekit.VideoQuality) *livekit.VideoLayer
}
