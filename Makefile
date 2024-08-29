ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
else
    DETECTED_OS := $(shell uname -s)
endif

cli: check_lfs
	GOOS=darwin GOARCH=arm64 go build -ldflags "-w -s" -o bin/lk ./cmd/lk
	GOOS=linux GOARCH=amd64 go build -ldflags "-w -s" -o bin/lk-linux ./cmd/lk
	GOOS=windows GOARCH=amd64 go build -ldflags "-w -s" -o bin/lk.exe ./cmd/lk


install: cli
ifeq ($(DETECTED_OS),Windows)
	cp bin/lk.exe $(GOBIN)/lk.exe
	ln -sf $(GOBIN)/lk.exe $(GOBIN)/livekit-cli.exe
else ifeq ($(DETECTED_OS),Darwin)
	cp bin/lk $(GOBIN)/lk
	ln -sf $(GOBIN)/lk $(GOBIN)/livekit-cli
else
	cp bin/lk-linux $(GOBIN)/lk
	ln -sf $(GOBIN)/lk $(GOBIN)/livekit-cli
endif

check_lfs:
	@{ \
	if [ ! -n $(find pkg/provider/resources -name neon_720_2000.ivf -size +100) ]; then \
		echo "Video resources not found. Ensure Git LFS is installed"; \
		exit 1; \
	fi \
	}

fish_autocomplete: cli
	./bin/lk generate-fish-completion -o autocomplete/fish_autocomplete
