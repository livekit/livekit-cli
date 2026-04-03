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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
)

// LoadDockerDaemonImage loads a v1.Image from the local Docker daemon by tag or equivalent
// RepoTag. The caller must Close() the returned closer after finishing with the image
// (e.g. after pushing layers).
func LoadDockerDaemonImage(ctx context.Context, ref string) (v1.Image, io.Closer, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil, errors.New("empty image reference")
	}
	if _, err := name.ParseReference(ref); err != nil {
		return nil, nil, fmt.Errorf("invalid image reference: %w", err)
	}

	cli, id, err := dockerClientForImage(ctx, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve image in Docker daemon: %w", err)
	}

	img, err := daemon.Image(dockerImageIDRef(id), daemon.WithContext(ctx), daemon.WithClient(cli))
	if err != nil {
		cli.Close()
		return nil, nil, fmt.Errorf("failed to load image from Docker daemon: %w", err)
	}
	return img, cli, nil
}

// dockerImageIDRef implements name.Reference for a Docker Engine image ID (e.g. "sha256:...").
// pkg/name cannot parse that form; daemon.Image only needs Name/String for the API.
type dockerImageIDRef string

func (r dockerImageIDRef) Context() name.Repository { return name.Repository{} }

func (r dockerImageIDRef) Identifier() string {
	s := string(r)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (r dockerImageIDRef) Name() string { return string(r) }

func (r dockerImageIDRef) String() string { return string(r) }

func (r dockerImageIDRef) Scope(string) string { return "" }

// dockerImageRefKeySet returns normalized spellings of an image reference so two sets
// intersect iff the daemon would treat them as the same tag (e.g. my-app:latest vs
// docker.io/library/my-app:latest).
func dockerImageRefKeySet(ref string) map[string]struct{} {
	m := map[string]struct{}{}
	var add func(string)
	add = func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == "<none>:<none>" {
			return
		}
		if _, ok := m[s]; ok {
			return
		}
		m[s] = struct{}{}

		if rest, ok := strings.CutPrefix(s, "docker.io/"); ok {
			add(rest)
		}
		if rest, ok := strings.CutPrefix(s, "index.docker.io/"); ok {
			add(rest)
		}
		if rest, ok := strings.CutPrefix(s, "registry-1.docker.io/"); ok {
			add(rest)
		}
		if rest, ok := strings.CutPrefix(s, "library/"); ok {
			add(rest)
		}

		if !strings.Contains(s, "@") && strings.Count(s, ":") == 1 {
			i := strings.Index(s, ":")
			repo, tag := s[:i], s[i+1:]
			if repo != "" && tag != "" && !strings.Contains(repo, "/") {
				add("library/" + repo + ":" + tag)
				add("docker.io/library/" + repo + ":" + tag)
				add("docker.io/" + repo + ":" + tag)
			}
		}
	}
	add(ref)
	return m
}

func dockerImageRefsMatch(a, b string) bool {
	if a == b {
		return true
	}
	ka, kb := dockerImageRefKeySet(a), dockerImageRefKeySet(b)
	for k := range ka {
		if _, ok := kb[k]; ok {
			return true
		}
	}
	return false
}

func resolveLocalDockerImageID(ctx context.Context, c *client.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty image reference")
	}

	if insp, err := c.ImageInspect(ctx, ref); err == nil && insp.ID != "" {
		return insp.ID, nil
	}

	for _, candidate := range dockerImageRefKeyList(ref) {
		f := filters.NewArgs(filters.Arg("reference", candidate))
		imgs, err := c.ImageList(ctx, image.ListOptions{Filters: f})
		if err != nil {
			continue
		}
		for _, im := range imgs {
			if im.ID != "" {
				return im.ID, nil
			}
		}
	}

	imgs, err := c.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("docker image list: %w", err)
	}

	for _, im := range imgs {
		for _, tag := range im.RepoTags {
			if dockerImageRefsMatch(ref, tag) {
				return im.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no local Docker image matches %q", ref)
}

func dockerImageRefKeyList(ref string) []string {
	var out []string
	seen := map[string]struct{}{}
	for k := range dockerImageRefKeySet(ref) {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// dockerClientForImage tries successive API endpoints so we match the Docker CLI: default
// is FromEnv; when DOCKER_HOST is unset, Docker Desktop often uses ~/.docker/run/docker.sock
// while the Go client defaults to /var/run/docker.sock (they may not be the same node).
func dockerClientForImage(ctx context.Context, ref string) (*client.Client, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, "", errors.New("empty image reference")
	}

	try := func(opts ...client.Opt) (*client.Client, error) {
		return client.NewClientWithOpts(opts...)
	}

	var hostOrder []string
	if os.Getenv("DOCKER_HOST") != "" {
		hostOrder = append(hostOrder, "") // FromEnv only
	} else {
		hostOrder = append(hostOrder, "")
		if home, err := os.UserHomeDir(); err == nil {
			sock := filepath.Join(home, ".docker", "run", "docker.sock")
			if fi, err := os.Stat(sock); err == nil && !fi.IsDir() {
				u := "unix://" + filepath.ToSlash(sock)
				hostOrder = append(hostOrder, u)
			}
		}
	}

	var lastErr error
	seenHost := map[string]struct{}{}
	for _, explicitHost := range hostOrder {
		if _, dup := seenHost[explicitHost]; dup {
			continue
		}
		seenHost[explicitHost] = struct{}{}

		var cli *client.Client
		var err error
		if explicitHost == "" {
			cli, err = try(client.FromEnv, client.WithAPIVersionNegotiation())
		} else {
			cli, err = try(client.WithHost(explicitHost), client.WithAPIVersionNegotiation())
		}
		if err != nil {
			lastErr = err
			continue
		}

		id, err := resolveLocalDockerImageID(ctx, cli, ref)
		if err == nil {
			return cli, id, nil
		}
		lastErr = err
		cli.Close()
	}
	if lastErr == nil {
		lastErr = errors.New("could not connect to Docker")
	}
	return nil, "", lastErr
}
