ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

cli:
	go build -o bin/livekit-cli ./cmd/livekit-cli
	go build -o bin/livekit-load-tester ./cmd/livekit-load-tester
	GOOS=linux GOARCH=amd64 go build -o bin/livekit-load-tester-linux ./cmd/livekit-load-tester

install: cli
	cp bin/livekit-cli $(GOBIN)/
	cp bin/livekit-load-tester $(GOBIN)/
