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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// banner.json on main is the list of notices shown to installed CLIs. Editing that
// file is the whole release process: no build or tag involved.
const bannerURL = "https://raw.githubusercontent.com/livekit/livekit-cli/main/banner.json"

// The banner prints at the top, so output written while the fetch is in flight
// is held back and flushed behind it: the command keeps working, only its output
// is delayed. To keep that off most runs, the fetched file is cached and reused
// for bannerTTL.
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

// loadBanner returns banner.json from the cache when it is fresh, otherwise from
// the network, falling back to a stale cache when the fetch fails.
func loadBanner(ctx context.Context) []byte {
	path, err := bannerCachePath()
	if err != nil {
		return nil
	}
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < bannerTTL {
		raw, _ := os.ReadFile(path)
		return raw
	}
	raw := fetchBanner(ctx)
	if raw == nil {
		raw, _ = os.ReadFile(path)
		return raw
	}
	if os.MkdirAll(filepath.Dir(path), 0700) == nil {
		_ = os.WriteFile(path, raw, 0600)
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

// flushBanner blocks until the banner has been printed and held output released.
// deferBanner installs it; the root command's After hook calls it so a command
// cannot exit with output still held.
var flushBanner = func() {}

// bannerGate holds writes to the Printer until release, preserving their order
// across stdout and stderr.
type bannerGate struct {
	mu      sync.Mutex
	open    bool
	pending []pendingWrite
}

type pendingWrite struct {
	w io.Writer
	b []byte
}

type gatedWriter struct {
	g *bannerGate
	w io.Writer
}

func (gw gatedWriter) Write(b []byte) (int, error) {
	gw.g.mu.Lock()
	defer gw.g.mu.Unlock()
	if gw.g.open {
		return gw.w.Write(b)
	}
	gw.g.pending = append(gw.g.pending, pendingWrite{gw.w, bytes.Clone(b)})
	return len(b), nil
}

// release prints the banner to stderr, then the held writes, and lets later
// writes straight through.
func (g *bannerGate) release(raw []byte, stderr io.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open = true
	printBanner(raw, stderr)
	for _, p := range g.pending {
		_, _ = p.w.Write(p.b)
	}
	g.pending = nil
}

// deferBanner starts the fetch and gates the Printer until it completes.
// Non-interactive runs (scripts, pipes) skip the fetch entirely.
func deferBanner(ctx context.Context) {
	if !out.Interactive() {
		return
	}
	g := &bannerGate{}
	stdout, stderr := out.Out, out.Err
	out.Out, out.Err = gatedWriter{g, stdout}, gatedWriter{g, stderr}
	done := make(chan struct{})
	go func() {
		g.release(loadBanner(ctx), stderr)
		close(done)
	}()
	flushBanner = func() { <-done }
}

// printBanner writes the notices for this build to stderr, honoring --quiet.
func printBanner(raw []byte, stderr io.Writer) {
	msgs := bannerMessages(raw, livekitcli.Version)
	if len(msgs) == 0 || out.Quiet {
		return
	}
	// The fence sets the notices apart from the command's own output. The fixed
	// width wraps long messages instead of letting the border break on narrow
	// terminals.
	const width, inner = 76, 72
	fence := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(util.Warning()).
		Padding(0, 1).
		Width(width)
	// A rule between notices keeps two messages from reading as one paragraph.
	rule := "\n" + util.Dimmed(strings.Repeat("─", inner)) + "\n"
	fmt.Fprintf(stderr, "%s\n", fence.Render(strings.Join(msgs, rule)))
}
