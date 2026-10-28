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
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

const piiCategoryPrefix = "PII_REDACTION_CATEGORY_"

// projectBoolSettings maps each boolean project-settings flag to the field it
// sets. Flags are only sent when given, so `--analytics=false` turns a setting
// off and leaving the flag out keeps the default (create) or current value
// (update).
var projectBoolSettings = []struct {
	name  string
	usage string
	field func(*public.ProjectSettings) **bool
}{
	{"discoverable-by-domain", "Let users with your email domain find the project", func(s *public.ProjectSettings) **bool { return &s.DiscoverableByDomain }},
	{"joinable-by-domain", "Let users with your email domain join the project", func(s *public.ProjectSettings) **bool { return &s.JoinableByDomain }},
	{"analytics", "Collect analytics for the project", func(s *public.ProjectSettings) **bool { return &s.EnableAnalytics }},
	{"auto-create", "Create rooms automatically when a participant joins", func(s *public.ProjectSettings) **bool { return &s.EnableAutoCreate }},
	{"remote-unmute", "Let admins unmute remote participants", func(s *public.ProjectSettings) **bool { return &s.EnableRemoteUnmute }},
	{"egress-auto-retry", "Retry failed egress automatically", func(s *public.ProjectSettings) **bool { return &s.EnableEgressAutoRetry }},
	{"egress-backup-storage", "Use backup storage for egress uploads", func(s *public.ProjectSettings) **bool { return &s.EnableEgressBackupStorage }},
	{"inference-region-restriction", "Restrict LiveKit Inference to the project's region", func(s *public.ProjectSettings) **bool { return &s.EnableInferenceRegionRestriction }},
	{"user-data-recording", "Record traces, transcripts, and audio for agent observability", func(s *public.ProjectSettings) **bool { return &s.EnableUserDataRecording }},
	{"user-data-training", "Allow session data to be used for model training", func(s *public.ProjectSettings) **bool { return &s.EnableUserDataTraining }},
	{"require-explicit-dispatch", "Dispatch agents only when explicitly requested", func(s *public.ProjectSettings) **bool { return &s.RequireExplicitDispatch }},
	{"sfu-allow-pause", "Allow the SFU to pause video tracks", func(s *public.ProjectSettings) **bool { return &s.SfuAllowPause }},
	{"pii-redaction", "Redact PII from transcripts", func(s *public.ProjectSettings) **bool { return &s.EnablePiiRedaction }},
}

// projectSettingsFlags are the flags shared by the project create and update
// commands. Some are only offered on create: the user data region cannot be
// changed later, and making an existing project private removes every member
// but the caller.
//
// The subdomain flag is --project-subdomain because the global --subdomain
// selects the configured project to act on.
func projectSettingsFlags(create bool) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "project-subdomain", Usage: "Project `SUBDOMAIN` (on create, generated from the name when not given)"},
		&cli.StringFlag{Name: "custom-domain", Usage: "Custom `DOMAIN` for the project"},
	}
	if create {
		flags = append(flags, &cli.BoolFlag{Name: "private", Usage: "Make the project private, visible only to you until you add members"})
	}
	for _, b := range projectBoolSettings {
		flags = append(flags, &cli.BoolFlag{Name: b.name, Usage: b.usage})
	}
	flags = append(flags,
		&cli.BoolFlag{Name: "token-endpoint", Usage: "Enable the project's token endpoint"},
		&cli.StringSliceFlag{Name: "pii-redaction-categories", Usage: "PII `CATEGORIES` to redact, such as ssn,credit-card,email-address (with --pii-redaction and none stored, the defaults apply)"},
		&cli.StringSliceFlag{Name: "codecs", Usage: "Enabled `CODECS`, such as video/VP8,audio/opus"},
		&cli.StringSliceFlag{Name: "pinned-regions", Usage: "SFU `REGIONS` to pin the project to, such as us (on update, pass \"\" to clear)"},
	)
	if create {
		flags = append(flags, &cli.StringFlag{Name: "user-data-region", Usage: "`REGION` where session data is stored, such as us; cannot be changed later"})
	}
	return flags
}

// projectSettingsFromFlags builds the settings to send from the flags that
// were given.
func projectSettingsFromFlags(cmd *cli.Command) (public.ProjectSettings, error) {
	var s public.ProjectSettings
	for name, field := range map[string]**string{
		"name":              &s.Name,
		"project-subdomain": &s.Subdomain,
		"custom-domain":     &s.CustomDomain,
		"user-data-region":  &s.UserDataRegion,
	} {
		if cmd.IsSet(name) {
			*field = ptrTo(cmd.String(name))
		}
	}
	for _, b := range projectBoolSettings {
		if cmd.IsSet(b.name) {
			*b.field(&s) = ptrTo(cmd.Bool(b.name))
		}
	}
	if cmd.IsSet("private") {
		s.IsPrivate = ptrTo(cmd.Bool("private"))
	}
	if cmd.IsSet("token-endpoint") {
		s.DisableTokenEndpoint = ptrTo(!cmd.Bool("token-endpoint"))
	}
	// The server ignores an empty codec or PII category list, so passing ""
	// would look like it worked while changing nothing. Only the pinned
	// regions can be cleared.
	if cmd.IsSet("codecs") {
		codecs := nonEmpty(cmd.StringSlice("codecs"))
		if len(codecs) == 0 {
			return s, errors.New("--codecs needs at least one codec")
		}
		s.EnabledCodecs = &codecs
	}
	if cmd.IsSet("pinned-regions") {
		s.PinnedRegions = ptrTo(nonEmpty(cmd.StringSlice("pinned-regions")))
	}
	if cmd.IsSet("pii-redaction-categories") {
		values := nonEmpty(cmd.StringSlice("pii-redaction-categories"))
		if len(values) == 0 {
			return s, errors.New("--pii-redaction-categories needs at least one category; to stop redacting, pass --pii-redaction=false")
		}
		categories, err := parsePIICategories(values)
		if err != nil {
			return s, err
		}
		s.PiiRedactionCategories = &categories
	}
	return s, nil
}

// projectUpdateFromFlags is projectSettingsFromFlags for an update, which
// must change something.
func projectUpdateFromFlags(cmd *cli.Command) (public.ProjectSettings, error) {
	s, err := projectSettingsFromFlags(cmd)
	if err == nil && s.IsZero() {
		err = errors.New("nothing to update: pass --name or a setting to change")
	}
	return s, err
}

// parsePIICategories accepts categories by their short names (credit-card,
// CREDIT_CARD) or in full (PII_REDACTION_CATEGORY_CREDIT_CARD).
func parsePIICategories(values []string) ([]oapi.CloudProtocolPIIRedactionCategory, error) {
	categories := make([]oapi.CloudProtocolPIIRedactionCategory, 0, len(values))
	for _, v := range values {
		name := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), "-", "_"))
		c := oapi.CloudProtocolPIIRedactionCategory(piiCategoryPrefix + strings.TrimPrefix(name, piiCategoryPrefix))
		if !c.Valid() || c == oapi.PIIREDACTIONCATEGORYUNSPECIFIED {
			return nil, fmt.Errorf("unknown PII redaction category %q", v)
		}
		categories = append(categories, c)
	}
	return categories, nil
}

// nonEmpty drops blank entries, so `--pinned-regions ""` is an empty list.
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func ptrTo[T any](v T) *T { return &v }
