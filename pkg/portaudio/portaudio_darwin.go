//go:build console

package portaudio

/*
#cgo CFLAGS: -I${SRCDIR}/pa_src/include -I${SRCDIR}/pa_src/src/common -I${SRCDIR}/pa_src/src/os/unix -DPA_USE_COREAUDIO -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreAudio -framework AudioToolbox -framework AudioUnit -framework CoreFoundation -framework CoreServices

#include "pa_src/src/os/unix/pa_unix_util.c"
#include "pa_src/src/os/unix/pa_unix_hostapis.c"
#include "pa_src/src/os/unix/pa_pthread_util.c"
#include "pa_src/src/hostapi/coreaudio/pa_mac_core.c"
#include "pa_src/src/hostapi/coreaudio/pa_mac_core_blocking.c"
#include "pa_src/src/hostapi/coreaudio/pa_mac_core_utilities.c"
*/
import "C"
