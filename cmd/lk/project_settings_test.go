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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// parseProjectSettings runs args through a command carrying the project
// settings flags and returns what projectSettingsFromFlags builds.
func parseProjectSettings(t *testing.T, create bool, args ...string) (public.ProjectSettings, error) {
	t.Helper()
	var got public.ProjectSettings
	var gotErr error
	cmd := &cli.Command{
		Name:  "test",
		Flags: append([]cli.Flag{&cli.StringFlag{Name: "name"}}, projectSettingsFlags(create)...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			got, gotErr = projectSettingsFromFlags(cmd)
			return nil
		},
	}
	require.NoError(t, cmd.Run(context.Background(), append([]string{"test"}, args...)))
	return got, gotErr
}

func TestProjectSettingsFromFlags(t *testing.T) {
	s, err := parseProjectSettings(t, true,
		"--private",
		"--analytics=false",
		"--token-endpoint",
		"--project-subdomain", "demo",
		"--user-data-region", "eu",
		"--pinned-regions", "us-east,us-west",
		"--pii-redaction-categories", "ssn,credit-card,PII_REDACTION_CATEGORY_EMAIL_ADDRESS",
	)
	require.NoError(t, err)
	assert.Equal(t, public.ProjectSettings{
		Subdomain:            ptrTo("demo"),
		IsPrivate:            ptrTo(true),
		EnableAnalytics:      ptrTo(false),
		DisableTokenEndpoint: ptrTo(false),
		UserDataRegion:       ptrTo("eu"),
		PinnedRegions:        ptrTo([]string{"us-east", "us-west"}),
		PiiRedactionCategories: ptrTo([]oapi.CloudProtocolPIIRedactionCategory{
			oapi.PIIREDACTIONCATEGORYSSN,
			oapi.PIIREDACTIONCATEGORYCREDITCARD,
			oapi.PIIREDACTIONCATEGORYEMAILADDRESS,
		}),
	}, s)
}

func TestProjectSettingsFromFlagsLeavesUnsetFlagsOut(t *testing.T) {
	s, err := parseProjectSettings(t, false)
	require.NoError(t, err)
	assert.True(t, s.IsZero())
}

func TestProjectSettingsClearPinnedRegions(t *testing.T) {
	s, err := parseProjectSettings(t, false, "--pinned-regions", "")
	require.NoError(t, err)
	assert.Equal(t, ptrTo([]string{}), s.PinnedRegions)
}

func TestProjectSettingsUnknownPIICategory(t *testing.T) {
	_, err := parseProjectSettings(t, true, "--pii-redaction-categories", "ssn,shoe-size")
	assert.EqualError(t, err, `unknown PII redaction category "shoe-size"`)
}

func TestProjectUpdateNeedsAChange(t *testing.T) {
	cmd := &cli.Command{
		Name:  "test",
		Flags: append([]cli.Flag{&cli.StringFlag{Name: "name"}}, projectSettingsFlags(false)...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			_, err := projectUpdateFromFlags(cmd)
			return err
		},
	}
	assert.ErrorContains(t, cmd.Run(context.Background(), []string{"test"}), "nothing to update")
	assert.NoError(t, cmd.Run(context.Background(), []string{"test", "--name", "renamed"}))
}

// The global --subdomain picks the project to act on; it must not become a
// subdomain change.
func TestProjectSettingsIgnoreGlobalSubdomain(t *testing.T) {
	var got public.ProjectSettings
	root := &cli.Command{
		Name:  "lk",
		Flags: []cli.Flag{&cli.StringFlag{Name: "subdomain"}},
		Commands: []*cli.Command{{
			Name:  "update",
			Flags: append([]cli.Flag{&cli.StringFlag{Name: "name"}}, projectSettingsFlags(false)...),
			Action: func(_ context.Context, cmd *cli.Command) error {
				var err error
				got, err = projectSettingsFromFlags(cmd)
				return err
			},
		}},
	}
	require.NoError(t, root.Run(context.Background(), []string{"lk", "--subdomain", "other", "update", "--name", "renamed"}))
	assert.Equal(t, public.ProjectSettings{Name: ptrTo("renamed")}, got)
}
