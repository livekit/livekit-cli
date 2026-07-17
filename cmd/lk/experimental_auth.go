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

package main

import (
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
)

// experimentalAuthEnabled reports whether the user opted into user-based
// (session) auth via the global --experimental-auth flag.
func experimentalAuthEnabled(cmd *cli.Command) bool {
	return cmd.Bool("experimental-auth")
}

// experimentalAuthGate refuses a command that only supports API-key auth when
// the user requested experimental user-based auth. User auth routes through the
// Public API, which does not yet implement most operations; rather than
// silently fall back to API-key auth — a different security model than the user
// asked for — we fail clearly. Command paths that DO support user auth branch on
// experimentalAuthEnabled before reaching this gate.
func experimentalAuthGate(cmd *cli.Command) error {
	if experimentalAuthEnabled(cmd) {
		return errors.New("this command is not yet available under --experimental-auth (user-based auth); re-run without it to use API-key authentication")
	}
	return nil
}

// requireUserSession loads the CLI config and resolves the default user with a
// valid (unexpired) session, for commands running under --experimental-auth.
// The returned *CLIConfig is the same instance the user was read from, so
// callers may cache data on it (e.g. via SetUserProjects) and persist.
func requireUserSession(cmd *cli.Command) (*config.CLIConfig, *config.UserConfig, error) {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return nil, nil, err
	}
	if conf.DefaultUser == "" {
		return nil, nil, errors.New("no user is signed in (run `lk cloud auth` to sign in)")
	}
	user := conf.GetUser(conf.DefaultUser)
	if user == nil {
		return nil, nil, fmt.Errorf("default user %q not found in config", conf.DefaultUser)
	}
	if !user.SessionValid() {
		return nil, nil, fmt.Errorf("session for %s has expired (run `lk cloud auth` to sign in again)", userLabel(user))
	}
	return conf, user, nil
}

// newCloudAPIClient builds a Public API client authenticated as the default
// user, honoring --experimental-api-url.
func newCloudAPIClient(cmd *cli.Command) (*public.Client, *config.CLIConfig, *config.UserConfig, error) {
	conf, user, err := requireUserSession(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := public.New(experimentalAPIURL, user.SessionToken)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, conf, user, nil
}

// userLabel is a human-friendly identifier for a user, preferring email.
func userLabel(u *config.UserConfig) string {
	switch {
	case u.Email != "":
		return u.Email
	case u.Name != "":
		return u.Name
	default:
		return u.Id
	}
}
