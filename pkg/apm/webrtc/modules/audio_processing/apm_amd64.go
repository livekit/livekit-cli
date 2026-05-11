//go:build console && amd64

package audio_processing

import (
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/aec3/avx2"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/agc2/rnn_vad/avx2"
)
