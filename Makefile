# `make` builds the lk binary — the same cgo artifact as `go build ./cmd/lk`
# (see the README), with a submodule init so it also works from a fresh clone.
# It doubles as the build system CodeQL's C/C++ autobuild detects and traces to
# extract the vendored C/C++ (PortAudio + WebRTC APM).
#
# `make install` puts it on $GOBIN with a `livekit-cli` alias for the legacy
# binary name. Releases use .goreleaser.yaml, not this file.

ifeq (,$(shell go env GOBIN))
GOBIN := $(shell go env GOPATH)/bin
else
GOBIN := $(shell go env GOBIN)
endif

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

# pa_src holds the PortAudio C source the cgo build links against; the submodule
# init makes this work from a fresh clone (and under CodeQL, whose checkout may
# skip submodules). ALSA headers (libasound2-dev) come from CodeQL's automatic
# dependency installation on Linux.
lk$(EXE):
	git submodule update --init --recursive
	CGO_ENABLED=1 go build -o lk$(EXE) ./cmd/lk

install: lk$(EXE)
	cp lk$(EXE) "$(GOBIN)/lk$(EXE)"
	ln -sf "$(GOBIN)/lk$(EXE)" "$(GOBIN)/livekit-cli$(EXE)"
