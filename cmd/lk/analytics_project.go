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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// The commands in this file read a whole project's data over a time range,
// rather than one session's: its logs, which only the Public API serves.

// logQueryHelp describes --query, the dashboard's log search syntax.
const logQueryHelp = "--query takes a search in the syntax of the logs search box on the dashboard: " +
	"a field such as agent_name:NAME, room_id:RM_ID or severity_text:ERROR, " +
	"an attribute with an @ before its name, such as @lk.cloud_agent_id:CA_ID, " +
	"or text that the message contains, such as \"timed out\". " +
	"Clauses combine with AND (or a space), OR and NOT; * matches any characters."

// analyticsLogsCommand reads a project's logs.
var analyticsLogsCommand = &cli.Command{
	Name:  "logs",
	Usage: "Print a project's logs from its agents, client SDKs and LiveKit (requires --user-auth)",
	UsageText: "lk analytics logs [--since DURATION | --start TIME [--end TIME]] [--log-level LEVEL ...] " +
		"[--source SOURCE ...] [--session-id RM_ID] [--query QUERY]",
	Description: "Prints the log records that the logs page on the dashboard lists, newest first, one line per record: " +
		"its time, level, source, session, logger and message. --json prints the records as the API sends them, " +
		"attributes included.\n\n" +
		"It reads the last 24 hours unless --since or --start says otherwise, up to and including --end, " +
		"to the millisecond. Records are kept for 14 days, and one read returns at most the 7 days before the end " +
		"of its range. Records from the last few minutes can still arrive.\n\n" +
		logQueryHelp + "\n\n" +
		"Reading a project's logs requires being a project admin.",
	Action: projectRead(projectLogOptions, fetchProjectLogs),
	Flags:  append([]cli.Flag{jsonFlag}, analyticsProjectLogFlags()...),
}

// projectRead builds the action of a command that reads a project's data,
// which only the Public API serves. Like sessionRead's, the action refuses to
// run without --user-auth before checking anything else, then reads the
// command's options, so a bad flag fails before the project lookup, and hands
// fetch a client signed in as the user and the selected project.
func projectRead[O any](
	readOptions func(*cli.Command) (O, error),
	fetch func(ctx context.Context, client *public.Client, projectID string, opts O, asJSON bool) error,
) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if err := requireUserAuth(cmd); err != nil {
			return err
		}
		opts, err := readOptions(cmd)
		if err != nil {
			return err
		}
		client, conf, user, err := requireCloudClient(cmd)
		if err != nil {
			return err
		}
		projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
		if err != nil {
			return err
		}
		return fetch(ctx, client, projectID, opts, cmd.Bool("json"))
	}
}

// timeRangeFlags returns fresh --since, --start and --end flags for a read of
// a project's data over a time range. defaultWindow names the range the read
// covers without them, such as "24h".
func timeRangeFlags(defaultWindow string) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "since",
			Usage: "Read the last `DURATION`, such as 15m, 6h, 2d or 1w (default " + defaultWindow + "); can't be used with --start or --end",
		},
		&cli.StringFlag{
			Name:  "start",
			Usage: "Read from `TIME`: a UTC date (YYYY-MM-DD) or an RFC 3339 time, such as 2026-01-02T15:04:05Z",
		},
		&cli.StringFlag{
			Name:  "end",
			Usage: "Read up to `TIME`, a date or time as for --start (default now)",
		},
	}
}

// timeRangeOptions reads --since, --start and --end. --since starts the range
// that long before now; --start and --end bound it. Whatever they leave unset
// the server fills in: the end is now, and the start the read's default length
// before the end.
func timeRangeOptions(cmd *cli.Command) (public.TimeRange, error) {
	if since := cmd.String("since"); since != "" {
		if cmd.IsSet("start") || cmd.IsSet("end") {
			return public.TimeRange{}, errors.New("--since can't be used with --start or --end")
		}
		d, err := parseLookback(since)
		if err != nil {
			return public.TimeRange{}, err
		}
		return public.TimeRange{Start: analyticsNow().Add(-d)}, nil
	}
	var r public.TimeRange
	var err error
	if r.Start, err = parseTimeFlag("start", cmd.String("start")); err != nil {
		return public.TimeRange{}, err
	}
	if r.End, err = parseTimeFlag("end", cmd.String("end")); err != nil {
		return public.TimeRange{}, err
	}
	if !r.Start.IsZero() && !r.End.IsZero() && !r.Start.Before(r.End) {
		return public.TimeRange{}, errors.New("--start must be before --end")
	}
	return r, nil
}

// parseTimeFlag parses the --start or --end (name) value s: a UTC date
// (YYYY-MM-DD), its midnight, or an RFC 3339 time. Empty is unset, the zero
// time.
func parseTimeFlag(name, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --%s %q, expected a date (YYYY-MM-DD) or an RFC 3339 time, such as 2026-01-02T15:04:05Z", name, s)
}

// analyticsProjectLogFlags returns fresh instances of the project logs' own
// flags (the --json flag is shared).
func analyticsProjectLogFlags() []cli.Flag {
	flags := append(timeRangeFlags("24h"),
		&cli.StringSliceFlag{
			Name:  "log-level",
			Usage: "Print only records at `LEVEL`: trace, debug, info, warn, error or fatal; repeatable, matches any (default every record)",
		},
		&cli.StringSliceFlag{
			Name: "source",
			Usage: "Print only records from `SOURCE`: agent, client (the client SDKs) or platform " +
				"(LiveKit's own, such as API calls); repeatable, matches any (default every source)",
		},
		&cli.StringFlag{
			Name:  "session-id",
			Usage: "Print only the records of the session `RM_ID`",
		},
		&cli.StringFlag{
			Name:  "query",
			Usage: "Print only records that match `QUERY`, in the dashboard's log search syntax, such as @lk.cloud_agent_id:CA_ID (see the description)",
		},
		&cli.StringFlag{
			Name:  "sort-order",
			Usage: "Order by time: `ORDER` asc or desc, default desc",
		},
	)
	return append(flags, readFlags("records")...)
}

// projectLogOptions reads the project logs flags. A bad range, limit, level,
// source or sort order fails here, before the project lookup.
func projectLogOptions(cmd *cli.Command) (projectLogRead, error) {
	page, err := pageOptions(cmd)
	if err != nil {
		return projectLogRead{}, err
	}
	timeRange, err := timeRangeOptions(cmd)
	if err != nil {
		return projectLogRead{}, err
	}
	opts := public.ProjectLogOptions{
		PageOptions: page,
		Range:       timeRange,
		Levels:      cmd.StringSlice("log-level"),
		Sources:     cmd.StringSlice("source"),
		SessionID:   strings.TrimSpace(cmd.String("session-id")),
		Query:       strings.TrimSpace(cmd.String("query")),
		SortOrder:   cmd.String("sort-order"),
	}
	if err := opts.Validate(); err != nil {
		return projectLogRead{}, err
	}
	return projectLogRead{ProjectLogOptions: opts, defaultRange: usesDefaultWindow(cmd)}, nil
}

// projectLogRead is what `analytics logs` reads, and whether it reads the
// default range, so an empty read can say how to read further back.
type projectLogRead struct {
	public.ProjectLogOptions
	defaultRange bool
}

// fetchProjectLogs reads a project's logs, every page up to opts.Limit
// records, and prints them, saying why when there are none.
func fetchProjectLogs(ctx context.Context, client *public.Client, projectID string, opts projectLogRead, asJSON bool) error {
	page := &public.ProjectLogPage{}
	var err error
	page.Records, page.NextCursor, err = readPages(opts.PageOptions, func(o public.PageOptions) ([]oapi.LivekitPublicapiObservabilityV1LogRecord, string, error) {
		read := opts.ProjectLogOptions
		read.PageOptions = o
		p, err := client.ListProjectLogs(ctx, projectID, read)
		if err != nil {
			return nil, "", err
		}
		return p.Records, p.NextCursor, nil
	})
	if err != nil {
		return projectReadError(err, "logs", projectAdminAccess)
	}
	var empty string
	if len(page.Records) == 0 {
		empty = emptyProjectLogsReason(opts)
	}
	return render.ProjectLogs(out, asJSON, *page, empty)
}

// emptyProjectLogsReason explains a read with no records. With user data
// recording off an unfiltered first page is an error instead, so an empty one
// here means nothing logged in the range, or nothing that matches.
func emptyProjectLogsReason(opts projectLogRead) string {
	switch {
	case opts.Cursor != "":
		return "No more log records"
	case len(opts.Levels) > 0 || len(opts.Sources) > 0 || opts.SessionID != "" || opts.Query != "":
		return "No log records in the range match the filters"
	case opts.defaultRange:
		return fmt.Sprintf("No log records in the last 24 hours — re-run with %s or %s to read further back (records are kept for 14 days)",
			util.Accented("--since DURATION"), util.Accented("--start TIME"))
	default:
		return "No log records in the range"
	}
}

// projectReadError explains why a read of a project's what failed: user data
// recording is off, the caller lacks the access the read requires, or the
// server can't parse the search query; and otherwise annotates the error like
// the other Public API commands.
func projectReadError(err error, what string, access sessionReadAccess) error {
	if dashboardURL, ok := public.ObservabilityDisabled(err); ok {
		return observabilityDisabledError("the project", what, dashboardURL)
	}
	if public.InvalidLogQuery(err) {
		return fmt.Errorf("%w — an attribute needs an @ before its name, such as @lk.cloud_agent_id:CA_ID; --help describes the syntax", err)
	}
	if !public.IsPermissionDenied(err) {
		return cloudAPIError(err)
	}
	if access == projectAdminAccess {
		return fmt.Errorf("%w — reading a project's %s requires being a project admin", err, what)
	}
	return fmt.Errorf("%w — you don't have access to this project", err)
}
