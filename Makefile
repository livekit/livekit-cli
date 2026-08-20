# Build system shim.
#
# The build system is Mage (see ./magefile.go); run targets directly with
# `go tool mage <target>` (mage is pinned as a go.mod tool dependency).
#
# This Makefile is retained ONLY so GitHub's default CodeQL setup keeps working:
# its C/C++ autobuild detects a Makefile and runs `make`, which is what traces
# the cgo compilation of the vendored PortAudio + WebRTC APM for extraction.
# Each target delegates to the corresponding Mage target so there is a single
# source of truth. `mage build` runs the same CGO_ENABLED=1 `go build`.

MAGE := go tool mage

.PHONY: all build install clean generate test

all: build

build:
	$(MAGE) build

install:
	$(MAGE) install

clean:
	$(MAGE) clean

generate:
	$(MAGE) generate

test:
	$(MAGE) test
