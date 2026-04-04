//go:build console

package portaudio

/*
#cgo CFLAGS: -I${SRCDIR}/pa_src/include -I${SRCDIR}/pa_src/src/common -I${SRCDIR}/pa_src/src/os/win -DPA_USE_WASAPI -Wno-unused-parameter
#cgo LDFLAGS: -lole32 -lwinmm -luuid

#include "pa_src/src/os/win/pa_win_util.c"
#include "pa_src/src/os/win/pa_win_version.c"
#include "pa_src/src/os/win/pa_win_hostapis.c"
#include "pa_src/src/os/win/pa_win_coinitialize.c"
#include "pa_src/src/os/win/pa_win_waveformat.c"
#include "pa_src/src/os/win/pa_x86_plain_converters.c"
#include "pa_src/src/hostapi/wasapi/pa_win_wasapi.c"
*/
import "C"
