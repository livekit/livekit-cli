//go:build !portaudio_system

package portaudio

// Vendored PortAudio: the cross-platform core sources are compiled directly into
// this package (a cgo unity build). The per-OS host APIs and their backend link
// flags live in portaudio_<os>.go. Build with -tags portaudio_system to link a
// system libportaudio instead, in which case none of these sources compile.

/*
#cgo CFLAGS: -I${SRCDIR}/pa_src/include -I${SRCDIR}/pa_src/src/common -DPA_LITTLE_ENDIAN -Wno-unused-parameter -Wno-deprecated-declarations

#if !__has_include("pa_src/include/portaudio.h")
#error "PortAudio submodule not found. Run: git submodule update --init --recursive (or build with -tags portaudio_system to use a system libportaudio)"
#else

#include "pa_src/src/common/pa_allocation.c"
#include "pa_src/src/common/pa_converters.c"
#include "pa_src/src/common/pa_cpuload.c"
#include "pa_src/src/common/pa_debugprint.c"
#include "pa_src/src/common/pa_dither.c"
#include "pa_src/src/common/pa_front.c"
#include "pa_src/src/common/pa_process.c"
#include "pa_src/src/common/pa_ringbuffer.c"
#include "pa_src/src/common/pa_stream.c"
#include "pa_src/src/common/pa_trace.c"
#endif
*/
import "C"
