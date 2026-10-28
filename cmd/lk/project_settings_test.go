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
	"reflect"
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

// The server ignores empty codec and PII category lists, so they are refused
// rather than silently changing nothing.
func TestProjectSettingsRefuseEmptyLists(t *testing.T) {
	for _, create := range []bool{true, false} {
		_, err := parseProjectSettings(t, create, "--codecs", "")
		assert.ErrorContains(t, err, "--codecs needs at least one codec")
		_, err = parseProjectSettings(t, create, "--pii-redaction-categories", "")
		assert.ErrorContains(t, err, "--pii-redaction-categories needs at least one category")
	}
}

// Every project setting must be settable by a flag, so a field added to
// public.ProjectSettings without one fails here.
func TestProjectSettingsFlagsCoverEverySetting(t *testing.T) {
	args := []string{"--name", "demo"}
	for _, f := range projectSettingsFlags(true) {
		name := f.Names()[0]
		switch f.(type) {
		case *cli.BoolFlag:
			args = append(args, "--"+name)
		case *cli.StringSliceFlag:
			value := "x"
			if name == "pii-redaction-categories" {
				value = "ssn"
			}
			args = append(args, "--"+name, value)
		default:
			args = append(args, "--"+name, "x")
		}
	}
	s, err := parseProjectSettings(t, true, args...)
	require.NoError(t, err)
	v := reflect.ValueOf(s)
	for i := range v.NumField() {
		assert.False(t, v.Field(i).IsNil(), "no flag sets ProjectSettings.%s", v.Type().Field(i).Name)
	}
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
