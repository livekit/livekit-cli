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

package render

import (
	"fmt"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// ProjectLogs prints a page of a project's log records, one line per record
// with its time, level, source, session, logger and message; --json prints
// the records as the API sent them. empty says why a page has no records, on
// stderr in both modes so --json output stays parseable.
func ProjectLogs(p *util.Printer, asJSON bool, page public.ProjectLogPage, empty string) error {
	jp := newJSONPage(page.Records, page.NextCursor, 0, nil)
	if err := renderPage(p, asJSON, jp, empty, projectLogLines); err != nil || asJSON {
		return err
	}
	moreAvailable(p, "records", page.NextCursor)
	return nil
}

// projectLogLines renders log records one per line, their sources, sessions
// and loggers padded so the messages line up.
func projectLogLines(records []oapi.LivekitPublicapiObservabilityV1LogRecord) []string {
	sourceWidth, sessionWidth, loggerWidth := 1, 1, 1
	for _, r := range records {
		sourceWidth = max(sourceWidth, len(logSourceName(r.Source)))
		sessionWidth = max(sessionWidth, len([]rune(dashText(r.SessionId))))
		loggerWidth = max(loggerWidth, len([]rune(dashText(r.Logger))))
	}
	lines := make([]string, 0, len(records))
	for _, r := range records {
		at := "-"
		if r.Timestamp != nil && !r.Timestamp.IsZero() {
			at = r.Timestamp.Local().Format("2006-01-02 15:04:05.000")
		}
		lines = append(lines, fmt.Sprintf("%-23s  %-6s  %-*s  %-*s  %-*s  %s", at, logLevelName(r),
			sourceWidth, logSourceName(r.Source), sessionWidth, dashText(r.SessionId),
			loggerWidth, dashText(r.Logger), oneLine(util.Deref(r.Message))))
	}
	return lines
}

// logSourceName names a record's source in lower case, as --source takes it,
// or "-" when the API didn't say.
func logSourceName(s *oapi.LivekitPublicapiObservabilityV1LogSource) string {
	if s == nil || *s == oapi.LOGSOURCEUNSPECIFIED {
		return "-"
	}
	return strings.ToLower(strings.TrimPrefix(string(*s), "LOG_SOURCE_"))
}

var logPatternHeaders = []string{"Events", "Level", "Source", "First Seen", "Last Seen", "Template"}

// logPatternTemplateWidth is how much of a pattern's template a row shows;
// --json has all of it.
const logPatternTemplateWidth = 80

func logPatternRow(lp oapi.LivekitPublicapiObservabilityV1LogPattern) []string {
	level := "-"
	if lp.Level != nil && *lp.Level != oapi.LOGLEVELUNSPECIFIED {
		level = strings.TrimPrefix(string(*lp.Level), "LOG_LEVEL_")
	}
	return []string{
		util.DashString(lp.Events), level, logSourceName(lp.Source),
		util.FormatTime(lp.FirstSeen), util.FormatTime(lp.LastSeen),
		clip(oneLine(util.Deref(lp.Template)), logPatternTemplateWidth),
	}
}

// LogPatterns prints a project's ranked log patterns, one row each with its
// count, level, source, first and last record, and template, then on stderr
// how many records the range holds and whether the counts are estimates;
// --json prints the API's response. empty says why there are no patterns, on
// stderr in both modes so --json output stays parseable.
func LogPatterns(p *util.Printer, asJSON bool, resp oapi.LivekitPublicapiObservabilityV1LogPatternsListResponse, empty string) error {
	patterns := util.Deref(resp.Items)
	if asJSON {
		if err := util.PrintJSONTo(p.ResultWriter(), resp); err != nil {
			return err
		}
	} else if len(patterns) > 0 {
		t := util.CreateTable().Headers(logPatternHeaders...)
		for _, lp := range patterns {
			t.Row(logPatternRow(lp)...)
		}
		p.Result(t)
	}
	if len(patterns) == 0 {
		if empty != "" {
			p.Status(empty)
		}
		return nil
	}
	if asJSON {
		return nil
	}
	shown := "1 pattern shown"
	if len(patterns) != 1 {
		shown = fmt.Sprintf("%d patterns shown", len(patterns))
	}
	p.Statusf("%s; the range holds %s records in all patterns", shown, util.Dash(util.Deref(resp.TotalEvents)))
	if util.Deref(resp.Estimated) {
		p.Statusf("The counts are estimates: the API read the records of %s of the sessions, and scaled the counts",
			formatPercent(util.Deref(resp.SampleShare)))
	}
	return nil
}
