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
