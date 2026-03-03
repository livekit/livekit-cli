//go:build console

package audio_processing

// #cgo CXXFLAGS: -I${SRCDIR}/../.. -I${SRCDIR}/../../third_party/abseil-cpp -std=c++17 -fno-rtti -DWEBRTC_APM_DEBUG_DUMP=0 -DWEBRTC_AUDIO_PROCESSING_ONLY_BUILD -DNDEBUG -Wno-unused-parameter -Wno-missing-field-initializers -Wno-sign-compare -Wno-deprecated-declarations -Wno-nullability-completeness -Wno-shorten-64-to-32
// #cgo darwin CXXFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX
// #cgo linux CXXFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CXXFLAGS: -DWEBRTC_WIN
// #cgo arm64 CXXFLAGS: -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64
import "C"

import (
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/aec3"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/aecm"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/agc"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/agc2"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/capture_levels_adjuster"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/echo_detector"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/include"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/logging"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/ns"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/utility"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/modules/audio_processing/vad"
)
