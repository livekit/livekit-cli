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
// rather than one session's: its logs, log patterns and agent insights, which
// only the Public API serves.

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
		"[--source SOURCE ...] [--session-id RM_ID] [--query QUERY] [--sort-order asc|desc] [--limit N]",
	Description: "Prints the log records that the logs page on the dashboard lists, newest first, one line per record: " +
		"its time, level, source, session, logger and message. --json prints the records as the API sends them, " +
		"attributes included. Times print in local time.\n\n" +
		"It reads the 24 hours before --end (default now), unless --since or --start sets the start, " +
		"and includes --end, to the millisecond. Records are kept for 14 days, and one read returns at most " +
		"the 7 days before the end of its range. Records from the last few minutes can still arrive.\n\n" +
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
				"(LiveKit's own, such as a participant joining); repeatable, matches any (default every source)",
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
// recording is off, or the server can't parse the search query; and otherwise
// annotates the error as readAPIError does.
func projectReadError(err error, what string, access sessionReadAccess) error {
	if dashboardURL, ok := public.ObservabilityDisabled(err); ok {
		return observabilityDisabledError("the project", what, dashboardURL)
	}
	if public.InvalidLogQuery(err) {
		return fmt.Errorf("%w — check the syntax: an attribute needs an @ before its name, "+
			"such as @lk.cloud_agent_id:CA_ID, and --help describes the rest", err)
	}
	return readAPIError(err, "a project's "+what, access)
}

// analyticsLogPatternsCommand reads a project's log patterns.
var analyticsLogPatternsCommand = &cli.Command{
	Name:  "log-patterns",
	Usage: "Print a project's log patterns: its log records grouped by message, with counts (requires --user-auth)",
	UsageText: "lk analytics log-patterns [--since DURATION | --start TIME [--end TIME]] [--min-level LEVEL] [--source SOURCE ...] " +
		"[--agent-name NAME] [--agent-id CA_ID] [--session-id RM_ID] [--query QUERY] [--rank RANK] [--limit N]",
	Description: "Prints the log patterns that the logs page on the dashboard groups a project's log records into. " +
		"A pattern groups the records that log the same message from one place in the code, at one level. " +
		"Its template is the message with each variable part, such as a number or an ID, replaced by a placeholder, " +
		"such as <N> or <*>. It prints one row per pattern: its number of records, level, source, first and last record, " +
		"and template. --json prints the API's response, with each pattern's counts over time, its variants, " +
		"and its logger and code location. Times print in local time.\n\n" +
		"By default it lists the 50 patterns with the most records among the agents' records at WARN or higher. " +
		"--rank first-seen or last-seen lists the patterns with the newest first or last record instead. " +
		"It reads the 24 hours before --end (default now), unless --since or --start sets the start. " +
		"The range doesn't include --end, and one read covers at most the 7 days before the end of its range. " +
		"The counts leave out the current minute (five minutes for a range of a day or more), and simulated sessions " +
		"unless --session-id names one. On a project with many records the counts are estimates from a share of " +
		"the sessions, as a note on stderr says.\n\n" +
		logQueryHelp + "\n\n" +
		"Reading a project's log patterns requires being a project admin.",
	Action: projectRead(logPatternOptions, fetchLogPatterns),
	Flags:  append([]cli.Flag{jsonFlag}, analyticsLogPatternFlags()...),
}

// analyticsLogPatternFlags returns fresh instances of the log patterns' own
// flags (the --json flag is shared).
func analyticsLogPatternFlags() []cli.Flag {
	return append(timeRangeFlags("24h"),
		&cli.StringFlag{
			Name:  "min-level",
			Usage: "Print only patterns at `LEVEL` or higher: trace (every level, records with none included), debug, info, warn, error or fatal (default warn)",
		},
		&cli.StringSliceFlag{
			Name:  "source",
			Usage: "Print only patterns from `SOURCE`: agent or client (the client SDKs); repeatable, matches any (default agent)",
		},
		&cli.StringFlag{
			Name:  "agent-name",
			Usage: "Print only the patterns of the agents named `NAME`",
		},
		&cli.StringFlag{
			Name:  "agent-id",
			Usage: "Print only the patterns of the cloud agent `CA_ID`",
		},
		&cli.StringFlag{
			Name:  "session-id",
			Usage: "Print only the patterns of the session `RM_ID`",
		},
		&cli.StringFlag{
			Name:  "query",
			Usage: "Count only records that match `QUERY`, in the dashboard's log search syntax, such as @lk.cloud_agent_id:CA_ID (see the description)",
		},
		&cli.StringFlag{
			Name:  "rank",
			Usage: "Which patterns to print, best first: `RANK` events (most records), first-seen or last-seen (newest first or last record); default events",
		},
		&cli.IntFlag{
			Name:        "limit",
			Usage:       "Maximum number of patterns to print (default 50, at most 200)",
			HideDefault: true,
		},
	)
}

// logPatternRead is what `analytics log-patterns` reads, and whether it reads
// the default range, so an empty read can say how to read further back.
type logPatternRead struct {
	public.LogPatternOptions
	defaultRange bool
}

// logPatternOptions reads the log patterns flags. A bad range, limit, level,
// source or rank fails here, before the project lookup.
func logPatternOptions(cmd *cli.Command) (logPatternRead, error) {
	limit := cmd.Int("limit")
	if limit < 0 || (limit == 0 && cmd.IsSet("limit")) {
		return logPatternRead{}, errors.New("limit must be greater than 0")
	}
	timeRange, err := timeRangeOptions(cmd)
	if err != nil {
		return logPatternRead{}, err
	}
	opts := public.LogPatternOptions{
		Range:     timeRange,
		Limit:     int32(limit),
		Query:     strings.TrimSpace(cmd.String("query")),
		MinLevel:  cmd.String("min-level"),
		Sources:   cmd.StringSlice("source"),
		AgentName: strings.TrimSpace(cmd.String("agent-name")),
		AgentID:   strings.TrimSpace(cmd.String("agent-id")),
		SessionID: strings.TrimSpace(cmd.String("session-id")),
		RankBy:    cmd.String("rank"),
	}
	if err := opts.Validate(); err != nil {
		return logPatternRead{}, err
	}
	return logPatternRead{LogPatternOptions: opts, defaultRange: usesDefaultWindow(cmd)}, nil
}

// fetchLogPatterns reads a project's log patterns and prints them, saying why
// when there are none.
func fetchLogPatterns(ctx context.Context, client *public.Client, projectID string, opts logPatternRead, asJSON bool) error {
	resp, err := client.ListLogPatterns(ctx, projectID, opts.LogPatternOptions)
	if err != nil {
		return projectReadError(err, "log patterns", projectAdminAccess)
	}
	var empty string
	if len(util.Deref(resp.Items)) == 0 {
		empty = emptyLogPatternsReason(opts)
	}
	return render.LogPatterns(out, asJSON, *resp, empty)
}

// emptyLogPatternsReason explains a read with no patterns. With user data
// recording off an unfiltered read is an error instead, so an empty one here
// means nothing logged in the range at the levels asked for, or nothing that
// matches.
func emptyLogPatternsReason(opts logPatternRead) string {
	filtered := opts.Query != "" || len(opts.Sources) > 0 || opts.AgentName != "" || opts.AgentID != "" || opts.SessionID != ""
	switch {
	case filtered:
		return "No log patterns in the range match the filters"
	case opts.MinLevel != "":
		return fmt.Sprintf("No log patterns at %s or higher in the range", strings.ToUpper(strings.TrimSpace(opts.MinLevel)))
	case opts.defaultRange:
		return fmt.Sprintf("No agent log patterns at WARN or higher in the last 24 hours — re-run with %s for every level, or %s to read further back",
			util.Accented("--min-level trace"), util.Accented("--since DURATION"))
	default:
		return "No agent log patterns at WARN or higher in the range"
	}
}

// analyticsInsightsCommand reads a project's agent insights.
var analyticsInsightsCommand = &cli.Command{
	Name:      "insights",
	Usage:     "Print the agent insights of a project: totals, latencies, agents and models (requires --user-auth)",
	UsageText: "lk analytics insights [--since DURATION | --start TIME [--end TIME]] [--agent-id CA_ID ...] [--agent-name NAME ...]",
	Description: "Prints the data that the agent dashboards of LiveKit Cloud show, from the turns, model requests and tool calls " +
		"that the project's agents recorded: their totals; the latencies that have measurements, such as end-to-end " +
		"and LLM time to first token; one row for each agent and deployment, busiest first; and one row for each model " +
		"that the agents used. --json prints the API's response.\n\n" +
		"It reads the 7 days before --end (default now), unless --since or --start sets the start. " +
		"The range doesn't include --end, and can be at most 30 days. Simulated sessions are left out. --agent-id and --agent-name select agents, at most 25 " +
		"together; a self-hosted agent has no ID, so select it by name.",
	Action: projectRead(agentInsightsOptions, fetchAgentInsights),
	Flags:  append([]cli.Flag{jsonFlag}, analyticsInsightsFlags()...),
}

// analyticsInsightsFlags returns fresh instances of the agent insights' own
// flags (the --json flag is shared).
func analyticsInsightsFlags() []cli.Flag {
	return append(timeRangeFlags("7d"),
		&cli.StringSliceFlag{
			Name:  "agent-id",
			Usage: "Print only the data of the cloud agent `CA_ID`; repeatable, with --agent-name matches any (default every agent)",
		},
		&cli.StringSliceFlag{
			Name:  "agent-name",
			Usage: "Print only the data of the agents named `NAME`; repeatable, with --agent-id matches any (default every agent)",
		},
	)
}

// agentInsightsRead is what `analytics insights` reads, and whether it reads
// the default range, so an empty read can say how to read further back.
type agentInsightsRead struct {
	public.AgentInsightsOptions
	defaultRange bool
}

// agentInsightsOptions reads the agent insights flags. A bad range or a blank
// agent fails here, before the project lookup.
func agentInsightsOptions(cmd *cli.Command) (agentInsightsRead, error) {
	timeRange, err := timeRangeOptions(cmd)
	if err != nil {
		return agentInsightsRead{}, err
	}
	opts := public.AgentInsightsOptions{
		Range:      timeRange,
		AgentIDs:   cmd.StringSlice("agent-id"),
		AgentNames: cmd.StringSlice("agent-name"),
	}
	if err := opts.Validate(); err != nil {
		return agentInsightsRead{}, err
	}
	return agentInsightsRead{AgentInsightsOptions: opts, defaultRange: usesDefaultWindow(cmd)}, nil
}

// fetchAgentInsights reads a project's agent insights and prints them, saying
// why when there are none.
func fetchAgentInsights(ctx context.Context, client *public.Client, projectID string, opts agentInsightsRead, asJSON bool) error {
	resp, err := client.GetAgentInsights(ctx, projectID, opts.AgentInsightsOptions)
	if err != nil {
		return projectReadError(err, "agent insights", projectReadAccess)
	}
	var empty string
	sessions := util.Deref(util.Deref(resp.Summary).Sessions)
	if (sessions == "" || sessions == "0") && len(util.Deref(resp.Agents)) == 0 && len(util.Deref(resp.Models)) == 0 {
		empty = emptyAgentInsightsReason(opts)
	}
	return render.AgentInsights(out, asJSON, *resp, empty)
}

// emptyAgentInsightsReason explains a read with no agent data.
func emptyAgentInsightsReason(opts agentInsightsRead) string {
	switch {
	case len(opts.AgentIDs) > 0 || len(opts.AgentNames) > 0:
		return "No data in the range for the agents asked for"
	case opts.defaultRange:
		return fmt.Sprintf("No agent data in the last 7 days — re-run with %s or %s to read further back (at most 30 days)",
			util.Accented("--since DURATION"), util.Accented("--start TIME"))
	default:
		return "No agent data in the range"
	}
}
