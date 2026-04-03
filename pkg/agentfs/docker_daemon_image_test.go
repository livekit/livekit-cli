// Copyright 2025 LiveKit, Inc.
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

package agentfs

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func TestDockerImageRefsMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{
			name: "bare tag vs docker hub library form",
			a:    "my-agent:latest",
			b:    "docker.io/library/my-agent:latest",
			want: true,
		},
		{
			name: "bare tag vs index alias",
			a:    "my-agent:latest",
			b:    "index.docker.io/library/my-agent:latest",
			want: true,
		},
		{
			name: "bare tag vs registry-1 alias",
			a:    "my-agent:latest",
			b:    "registry-1.docker.io/library/my-agent:latest",
			want: true,
		},
		{
			name: "same string",
			a:    "docker.io/library/foo:v1",
			b:    "docker.io/library/foo:v1",
			want: true,
		},
		{
			name: "different repos",
			a:    "my-agent:latest",
			b:    "other-agent:latest",
			want: false,
		},
		{
			name: "different tags",
			a:    "my-agent:latest",
			b:    "my-agent:old",
			want: false,
		},
		{
			name: "different namespaced repos",
			a:    "acme/service-a:v1",
			b:    "acme/service-b:v1",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := dockerImageRefsMatch(tt.a, tt.b); got != tt.want {
				t.Fatalf("dockerImageRefsMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestDockerImageRefKeySet_containsExpectedVariants(t *testing.T) {
	t.Parallel()
	ref := "my-agent:latest"
	keys := dockerImageRefKeySet(ref)
	for _, want := range []string{
		"my-agent:latest",
		"library/my-agent:latest",
		"docker.io/library/my-agent:latest",
		"docker.io/my-agent:latest",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("expected key set to contain %q, keys were: %v", want, sortedKeys(keys))
		}
	}
}

func TestDockerImageRefKeySet_ignoresNoneTag(t *testing.T) {
	t.Parallel()
	keys := dockerImageRefKeySet("<none>:<none>")
	if len(keys) != 0 {
		t.Fatalf("expected empty set, got %v", sortedKeys(keys))
	}
}

func TestDockerImageIDRef_nameReference(t *testing.T) {
	t.Parallel()
	const id = "sha256:eb540705f833d454ccb727f23dde5a9465af831e4aad4b76e917d620a9a58624"
	r := dockerImageIDRef(id)
	if r.Name() != id || r.String() != id {
		t.Fatalf("Name/String = %q / %q, want %q", r.Name(), r.String(), id)
	}
	if got := r.Identifier(); got != "eb540705f833d454ccb727f23dde5a9465af831e4aad4b76e917d620a9a58624" {
		t.Fatalf("Identifier() = %q", got)
	}
}

func TestLoadDockerDaemonImage_validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("empty ref", func(t *testing.T) {
		t.Parallel()
		_, closer, err := LoadDockerDaemonImage(ctx, "")
		if closer != nil {
			t.Fatal("expected nil closer")
		}
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("expected empty ref error, got %v", err)
		}
	})

	t.Run("invalid ref", func(t *testing.T) {
		t.Parallel()
		_, closer, err := LoadDockerDaemonImage(ctx, "not a valid ::: reference")
		if closer != nil {
			t.Fatal("expected nil closer")
		}
		if err == nil || !strings.Contains(err.Error(), "invalid image reference") {
			t.Fatalf("expected invalid reference error, got %v", err)
		}
	})
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
