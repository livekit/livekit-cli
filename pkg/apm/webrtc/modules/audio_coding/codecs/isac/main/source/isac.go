//go:build console

package source

// #cgo CFLAGS: -I${SRCDIR}/../../../../../.. -std=c11 -Wno-unused-parameter -Wno-sign-compare -Wno-deprecated-declarations
// #cgo darwin CFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX
// #cgo linux CFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX
// #cgo windows CFLAGS: -DWEBRTC_WIN
import "C"
