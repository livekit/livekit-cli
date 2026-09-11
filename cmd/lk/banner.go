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

// The banner is shown from the copy cached by the previous run and refreshed in
// the background, so it prints before the command without ever delaying it. A
// new notice therefore appears one run after it lands on main.
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

// fetchBanner resolves to the raw banner.json, or nothing when the fetch fails.
// It never delays the command: main caches whatever has arrived by the time the
// command finishes and drops the rest.
func fetchBanner(ctx context.Context) <-chan []byte {
	ch := make(chan []byte, 1)
	go func() {
		defer close(ch)
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, bannerURL, nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err != nil {
			return
		}
		ch <- body
	}()
	return ch
}

// saveBanner caches the fetched banner.json for the next run, if it has arrived.
func saveBanner(ch <-chan []byte) {
	select {
	case raw, ok := <-ch:
		if !ok {
			return
		}
		path, err := bannerCachePath()
		if err != nil {
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return
		}
		_ = os.WriteFile(path, raw, 0600)
	default:
	}
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

// printBanner shows the cached notices for this build on an interactive terminal
// via Status, so they land on stderr and honor --quiet. Non-interactive runs
// (scripts, pipes) never see it.
func printBanner() {
	if !out.Interactive() {
		return
	}
	path, err := bannerCachePath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	msgs := bannerMessages(raw, livekitcli.Version)
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
