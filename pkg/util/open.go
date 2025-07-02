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

package util

import (
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/pkg/browser"
	"github.com/urfave/cli/v3"
)

const (
	MeetURL = "https://meet.livekit.io/custom"
)

type OpenTarget string

const (
	OpenTargetMeet OpenTarget = "meet"
)

var (
	options  = []string{string(OpenTargetMeet)}
	OpenFlag = &cli.StringFlag{
		Name:  "open",
		Usage: fmt.Sprintf("Open relevant `APP` in browser, supported options: %v", options),
		Validator: func(input string) error {
			if !slices.Contains(options, input) {
				return fmt.Errorf("invalid open target: %s, supported options: %v", input, options)
			}
			return nil
		},
	}
)

func OpenInMeet(livekitURL, token string) error {
	if token == "" {
		return errors.New("token is required to open in Meet")
	}

	meetURL, err := url.Parse(MeetURL)
	if err != nil {
		return fmt.Errorf("failed to parse Meet URL: %w", err)
	}

	query := meetURL.Query()
	query.Set("liveKitUrl", livekitURL)
	query.Set("token", token)
	meetURL.RawQuery = query.Encode()

	if err := browser.OpenURL(meetURL.String()); err != nil {
		return fmt.Errorf("failed to open Meet URL: %w", err)
	}

	return nil
}
