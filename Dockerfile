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

# Consumed by goreleaser, which drops the pre-built `lk` binary into the build
# context. Distroless gives us glibc (the binary targets glibc 2.28+) plus CA
# certs for TLS, without a shell or package manager.
FROM gcr.io/distroless/base-debian12

COPY lk /lk

ENTRYPOINT ["/lk"]
