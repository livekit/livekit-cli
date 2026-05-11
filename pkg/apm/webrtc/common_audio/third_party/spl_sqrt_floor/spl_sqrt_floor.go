//go:build console

package spl_sqrt_floor


// #cgo CFLAGS: -I${SRCDIR}/../../.. -I${SRCDIR}/../../../third_party/abseil-cpp -DWEBRTC_APM_DEBUG_DUMP=0 -DWEBRTC_AUDIO_PROCESSING_ONLY_BUILD -DNDEBUG -Wno-unused-parameter -Wno-missing-field-initializers -Wno-sign-compare -Wno-deprecated-declarations -Wno-nullability-completeness -Wno-shorten-64-to-32
// #cgo darwin CFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX
// #cgo linux CFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CFLAGS: -DWEBRTC_WIN
// #cgo arm64 CFLAGS: -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64
import "C"
