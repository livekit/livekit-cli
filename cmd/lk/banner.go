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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/urfave/cli/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// banner.json on main is the list of notices shown to installed CLIs. Editing that
// file is the whole release process: no build or tag involved.
const bannerURL = "https://raw.githubusercontent.com/livekit/livekit-cli/main/banner.json"

// The banner prints before the command runs, so a fetch delays the command.
// To keep that off most runs, the fetched file is cached and reused for bannerTTL.
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
	// Audiences selects who sees the message: a user in any of them, or
	// everyone when empty. An audience this CLI doesn't know matches no one.
	// CLIs older than bannerAudiencesSince ignore the field and would show the
	// message to everyone, so an entry with audiences needs a Versions
	// constraint that excludes them, such as ">= 2.19.2"
	// (TestBannerFileAudiencesExcludeOldCLIs checks banner.json).
	Audiences []string `json:"audiences"`
}

// bannerAudienceUserAuth is the audience of users signed in with --user-auth.
const bannerAudienceUserAuth = "user-auth"

// bannerAudiencesSince is the first CLI version that understands Audiences.
const bannerAudiencesSince = "2.19.2"

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
// version and whose audiences include one of the user's, in file order.
func bannerMessages(raw []byte, version string, audiences []string) []string {
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
		if len(b.Audiences) > 0 && !slices.ContainsFunc(b.Audiences, func(a string) bool {
			return slices.Contains(audiences, a)
		}) {
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

// showBanner prints the notices before the command runs. It blocks so the
// banner owns the terminal first: prompts and spinners write to the terminal
// directly and cannot be held behind it. Runs that a script or tool drives
// (stderr or stdin isn't a terminal, or --yes) skip the fetch entirely: the
// install script and coding agents invoke lk that way, and a notice there is
// noise. A person at a terminal sees it on their next command.
func showBanner(ctx context.Context, cmd *cli.Command) {
	if !out.Interactive() || SkipPrompts(cmd) {
		return
	}
	printBanner(loadBanner(ctx), out.Err, userBannerAudiences())
}

// userBannerAudiences returns the banner audiences this user belongs to.
func userBannerAudiences() []string {
	var audiences []string
	if hasSignedInUser(startupConfig) {
		audiences = append(audiences, bannerAudienceUserAuth)
	}
	return audiences
}

// printBanner writes the notices for this build and user to stderr, honoring
// --quiet.
func printBanner(raw []byte, stderr io.Writer, audiences []string) {
	msgs := bannerMessages(raw, livekitcli.Version, audiences)
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
	fmt.Fprintf(stderr, "%s\n\n", fence.Render(strings.Join(msgs, rule)))
}
