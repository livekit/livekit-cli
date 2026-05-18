//go:build console

package portaudio

/*
#cgo CFLAGS: -I${SRCDIR}/pa_src/include -I${SRCDIR}/pa_src/src/common -I${SRCDIR}/pa_src/src/os/unix -DPA_USE_ALSA -Wno-unused-parameter
#cgo LDFLAGS: -lasound -lm -lpthread

#include "pa_src/src/os/unix/pa_unix_util.c"
#include "pa_src/src/os/unix/pa_unix_hostapis.c"
#include "pa_src/src/os/unix/pa_pthread_util.c"
#include "pa_src/src/hostapi/alsa/pa_linux_alsa.c"
*/
import "C"
