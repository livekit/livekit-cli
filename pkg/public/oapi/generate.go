// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build oapigen

package oapi

// This file carries only the code-generation directive for package oapi. It is
// gated behind the `oapigen` build tag so a plain `go generate ./...` never
// reaches out to fetch the spec; regenerate deliberately with:
//
//	go generate -tags oapigen ./pkg/public/...
//
// See ../gen.go for the source of truth and generate.sh for the fetch itself.
//go:generate sh generate.sh
