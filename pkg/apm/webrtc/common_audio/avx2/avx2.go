//go:build console && amd64

package avx2

// #cgo CXXFLAGS: -I${SRCDIR}/../.. -I${SRCDIR}/../../third_party/abseil-cpp -std=c++17 -fno-rtti -march=haswell -DWEBRTC_APM_DEBUG_DUMP=0 -DWEBRTC_AUDIO_PROCESSING_ONLY_BUILD -DNDEBUG -Wno-unused-parameter -Wno-missing-field-initializers -Wno-sign-compare -Wno-deprecated-declarations -Wno-nullability-completeness -Wno-shorten-64-to-32
// #cgo linux CXXFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CXXFLAGS: -DWEBRTC_WIN
import "C"
