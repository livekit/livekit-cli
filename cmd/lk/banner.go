// Copyright 2021-2026 LiveKit, Inc.
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

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// banner.json on main is the list of notices shown to installed CLIs. Editing that
// file is the whole release process: no build or tag involved.
const bannerURL = "https://raw.githubusercontent.com/livekit/livekit-cli/main/banner.json"

// The banner prints before the command, so the fetch is awaited. To keep that off
// most runs, the fetched file is cached and reused for bannerTTL.
const (
	bannerTimeout = time.Second
	bannerTTL     = time.Hour
)

func bannerCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".livekit", "banner.json"), nil
}

type banner struct {
	Message string `json:"message"`
	// Versions is a semver constraint (e.g. "< 3.0.0") selecting which CLI versions
	// see the message. Empty matches every version.
	Versions string `json:"versions"`
}

// bannerCache is the on-disk shape of ~/.livekit/banner.json: the fetched file and
// when it was fetched, so freshness does not depend on the file's mtime.
type bannerCache struct {
	Data         json.RawMessage `json:"data"`
	DownloadedAt time.Time       `json:"downloadedAt"`
}

// loadBanner returns banner.json from the cache when it was downloaded within
// bannerTTL, otherwise from the network, falling back to a stale cache when the
// fetch fails.
func loadBanner(ctx context.Context) []byte {
	path, err := bannerCachePath()
	if err != nil {
		return nil
	}
	var cached bannerCache
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &cached)
	}
	if time.Since(cached.DownloadedAt) < bannerTTL {
		return cached.Data
	}
	raw := fetchBanner(ctx)
	if raw == nil {
		return cached.Data
	}
	if os.MkdirAll(filepath.Dir(path), 0700) == nil {
		if enc, err := json.Marshal(bannerCache{Data: raw, DownloadedAt: time.Now()}); err == nil {
			_ = os.WriteFile(path, enc, 0600)
		}
	}
	return raw
}

// fetchBanner returns the raw banner.json, or nil when the fetch fails or exceeds
// bannerTimeout.
func fetchBanner(ctx context.Context) []byte {
	ctx, cancel := context.WithTimeout(ctx, bannerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bannerURL, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil
	}
	return body
}

// bannerMessages returns the message of every entry whose constraint matches
// version, in file order.
func bannerMessages(raw []byte, version string) []string {
	var banners []banner
	if json.Unmarshal(raw, &banners) != nil {
		return nil
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return nil
	}
	var msgs []string
	for _, b := range banners {
		if b.Message == "" {
			continue
		}
		if b.Versions != "" {
			c, err := semver.NewConstraint(b.Versions)
			if err != nil || !c.Check(v) {
				continue
			}
		}
		msgs = append(msgs, b.Message)
	}
	return msgs
}

// printBanner shows the notices for this build on an interactive terminal via
// Status, so they land on stderr and honor --quiet. Non-interactive runs (scripts,
// pipes) skip the fetch entirely.
func printBanner(ctx context.Context) {
	if !out.Interactive() {
		return
	}
	msgs := bannerMessages(loadBanner(ctx), livekitcli.Version)
	if len(msgs) == 0 {
		return
	}
	// The fence sets the notices apart from the command's own output. The fixed
	// width wraps long messages instead of letting the border break on narrow
	// terminals.
	fence := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(util.Warning()).
		Padding(0, 1).
		Width(76)
	out.Statusf("%s\n", fence.Render(strings.Join(msgs, "\n\n")))
}
