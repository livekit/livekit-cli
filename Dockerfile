FROM golang:1.17-alpine as builder

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY version.go version.go

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o livekit-load-tester ./cmd/livekit-load-tester

FROM alpine

COPY --from=builder /workspace/livekit-load-tester /livekit-load-tester

# Run the binary.
ENTRYPOINT ["/livekit-load-tester"]
