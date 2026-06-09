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

# Consumed by the docker job in release.yaml via `docker buildx`. The release
# job (goreleaser, on macOS) cross-builds the cgo Linux binaries with zig and
# stages them as lk_linux_<arch> in the build context; buildx sets TARGETARCH
# per platform. Distroless gives glibc (the binaries target glibc 2.28+) plus CA
# certs, without a shell or package manager.
FROM gcr.io/distroless/base-debian12

ARG TARGETARCH
COPY lk_linux_${TARGETARCH} /lk

ENTRYPOINT ["/lk"]
