ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

cli: check_lfs
	go build -o bin/livekit-cli ./cmd/livekit-cli

install: cli
	cp bin/livekit-cli $(GOBIN)/

check_lfs:
	@{ \
	if [ ! -f "pkg/provider/resources/neon_720_2000.ivf" ]; then \
		echo "Video resources not found. Ensure Git LFS is installed"; \
		exit 1; \
	fi \
	}
