//go:build console

package common_audio

// #cgo CXXFLAGS: -I${SRCDIR}/.. -I${SRCDIR}/../third_party/abseil-cpp -std=c++17 -fno-rtti -DWEBRTC_APM_DEBUG_DUMP=0 -DWEBRTC_AUDIO_PROCESSING_ONLY_BUILD -DNDEBUG -Wno-unused-parameter -Wno-missing-field-initializers -Wno-sign-compare -Wno-deprecated-declarations -Wno-nullability-completeness -Wno-shorten-64-to-32
// #cgo darwin CXXFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX
// #cgo linux CXXFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CXXFLAGS: -DWEBRTC_WIN
// #cgo arm64 CXXFLAGS: -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64
// #cgo CFLAGS: -I${SRCDIR}/.. -I${SRCDIR}/../third_party/abseil-cpp -DWEBRTC_APM_DEBUG_DUMP=0 -DWEBRTC_AUDIO_PROCESSING_ONLY_BUILD -DNDEBUG -Wno-unused-parameter -Wno-missing-field-initializers -Wno-sign-compare -Wno-deprecated-declarations -Wno-nullability-completeness -Wno-shorten-64-to-32
// #cgo darwin CFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX
// #cgo linux CFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CFLAGS: -DWEBRTC_WIN
// #cgo arm64 CFLAGS: -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64
import "C"

import (
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/common_audio/resampler"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/common_audio/signal_processing"
	_ "github.com/livekit/livekit-cli/v2/pkg/apm/webrtc/common_audio/vad"
)
