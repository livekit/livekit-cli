//go:build console && amd64

package common_audio

import (
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/common_audio/avx2"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/common_audio/resampler/avx2"
)
