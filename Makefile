ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

cli: check_lfs
	go build -ldflags "-w -s" -o bin/lk ./cmd/lk
	GOOS=linux GOARCH=amd64 go build -o bin/lk-linux ./cmd/lk

install: cli
	cp bin/lk $(GOBIN)/
	ln -sf $(GOBIN)/lk $(GOBIN)/livekit-cli

check_lfs:
	@{ \
	if [ ! -n $(find pkg/provider/resources -name neon_720_2000.ivf -size +100) ]; then \
		echo "Video resources not found. Ensure Git LFS is installed"; \
		exit 1; \
	fi \
	}

fish_autocomplete: cli
	./bin/lk generate-fish-completion -o autocomplete/fish_autocomplete
