//go:build portaudio_system

package portaudio

// Link against a system libportaudio (e.g. Homebrew's `portaudio`) instead of
// compiling the vendored sources. Selected with -tags portaudio_system. The
// system library already bundles its host-API backends, so no per-OS backend
// link flags are needed here. pkg-config supplies the include and link flags.

/*
#cgo pkg-config: portaudio-2.0
*/
import "C"
