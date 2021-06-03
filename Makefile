ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

cli:
	go build -o bin/livekit-cli ./cmd/livekit-cli
	go build -o bin/livekit-load-tester ./cmd/livekit-load-tester
	mv bin/livekit-cli $(GOBIN)/
	mv bin/livekit-load-tester $(GOBIN)/
