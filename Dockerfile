# Copyright 2023 LiveKit, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.22-alpine as builder

ARG TARGETPLATFORM
ARG TARGETARCH
RUN echo building for "$TARGETPLATFORM"

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

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -a -o lk ./cmd/lk
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -a -o livekit-cli ./cmd/livekit-cli

FROM alpine:3.20

COPY --from=builder /workspace/lk /lk
COPY --from=builder /workspace/livekit-cli /livekit-cli

# Run the binary.
ENTRYPOINT ["/lk"]
