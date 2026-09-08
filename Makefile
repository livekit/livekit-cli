# `make` builds the lk binary — the same cgo artifact as `go build ./cmd/lk`
# (see the README), with a submodule init so it also works from a fresh clone.
# It doubles as the build system CodeQL's C/C++ autobuild detects and traces to
# extract the vendored C/C++ (PortAudio + WebRTC APM).
#
# `make install` puts it on $GOBIN with a `livekit-cli` alias for the legacy
# binary name. `make update-vrt` re-records the TUI reference frames. Releases
# use .goreleaser.yaml, not this file.

.PHONY: all build install update-vrt clean

ifeq (,$(shell go env GOBIN))
GOBIN := $(shell go env GOPATH)/bin
else
GOBIN := $(shell go env GOBIN)
endif

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

all: build

# pa_src holds the PortAudio C source the cgo build links against; the submodule
# init makes this work from a fresh clone (and under CodeQL, whose checkout may
# skip submodules). ALSA headers (libasound2-dev) come from CodeQL's automatic
# dependency installation on Linux. Keyed on a file the submodule brings with it
# so an already-populated checkout doesn't pay for a git round trip every build.
pkg/portaudio/pa_src/CMakeLists.txt:
	git submodule update --init --recursive

# Phony, rather than a rule for ./bin/lk: as a file target the binary counted as
# up to date the moment it existed, so every source change after the first was
# silently skipped and `make install` shipped a stale binary. go build tracks
# staleness itself — over the Go sources, the vendored C/C++ and the build flags
# alike — and exits immediately when there is nothing to do, which no
# prerequisite list written here could match.
build: pkg/portaudio/pa_src/CMakeLists.txt
	CGO_ENABLED=1 go build -o ./bin/lk$(EXE) ./cmd/lk

install: build
	cp ./bin/lk$(EXE) "$(GOBIN)/lk$(EXE)"
	ln -sf "$(GOBIN)/lk$(EXE)" "$(GOBIN)/livekit-cli$(EXE)"

# Re-records the TUI visual regression frames in cmd/lk/testdata/vrt from the
# current renderers. Run it after an intentional UI change and commit the diff,
# which is the review artifact: it shows exactly what moved on screen.
#
# The existing frames are cleared first so a fixture that was renamed or removed
# takes its frame with it, instead of leaving one behind that nothing renders any
# more. Nothing is lost if the run then fails: git restore cmd/lk/testdata/vrt.
update-vrt:
	rm -f cmd/lk/testdata/vrt/*.txt
	UPDATE_TUI_VRT=1 go test ./cmd/lk -run TestVRT -count=1

clean:
	rm -rf ./bin
