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
	"time"

	"github.com/Masterminds/semver/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// banner.json on main is the list of notices shown to installed CLIs. Editing that
// file is the whole release process: no build or tag involved.
const bannerURL = "https://raw.githubusercontent.com/livekit/livekit-cli/main/banner.json"

type banner struct {
	Message string `json:"message"`
	// Versions is a semver constraint (e.g. "< 3.0.0") selecting which CLI versions
	// see the message. Empty matches every version.
	Versions string `json:"versions"`
}

// fetchBanner resolves to the notices for this build, or nothing when there are
// none or the fetch fails. It never delays the command: main prints whatever has
// arrived by the time the command finishes and drops the rest.
func fetchBanner(ctx context.Context) <-chan []string {
	ch := make(chan []string, 1)
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
		ch <- bannerMessages(body, livekitcli.Version)
	}()
	return ch
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

// printBanner shows the fetched notices on an interactive terminal. Non-interactive
// runs (scripts, pipes), --json runs, and runs that finished before the fetch never
// see it.
func printBanner(ch <-chan []string) {
	if !out.Interactive() || jsonOutput {
		return
	}
	select {
	case msgs := <-ch:
		for _, msg := range msgs {
			out.Warnf("\n%s", util.Warn(msg))
		}
	default:
	}
}
