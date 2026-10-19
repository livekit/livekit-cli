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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	authutil "github.com/livekit/livekit-cli/v2/pkg/auth"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	"github.com/urfave/cli/v3"
)

const (
	defaultAnalyticsLimit         = 10
	analyticsProjectIDRequirement = "analytics API requires a LiveKit Cloud project with a known project_id"
	analyticsProjectSelectHint    = "Select a cloud project via --project or run `lk cloud auth`"
)

// defaultAnalyticsWindow is how far back a session listing searches when no
// --since, --start or cursor sets its window.
const defaultAnalyticsWindow = 14 * 24 * time.Hour

// defaultPageLimit is how many of a session's participants a page reads by
// default.
const defaultPageLimit = 50

var (
	AnalyticsCommands = []*cli.Command{
		{
			Name:   "analytics",
			Usage:  "List and inspect LiveKit Cloud analytics",
			Hidden: true,
			Flags: []cli.Flag{
				experimentalFlag,
			},
			Commands: []*cli.Command{
				{
					Name:  "session",
					Usage: "List and inspect sessions", Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List analytics sessions",
							Action: listAnalyticsSessions,
							Flags:  append([]cli.Flag{jsonFlag}, analyticsSessionListFlags()...),
						},
						{
							Name:      "get",
							Usage:     "Get analytics session details by session ID",
							ArgsUsage: "SESSION_ID",
							Action:    getAnalyticsSession,
							Flags: []cli.Flag{
								jsonFlag,
							},
						},
						{
							Name:  "participant",
							Usage: "List a session's participants",
							Commands: []*cli.Command{
								{
									Name:      "list",
									Usage:     "List a session's participants (requires --experimental-auth)",
									ArgsUsage: "SESSION_ID",
									Action:    sessionRead(participantListOptions, fetchSessionParticipants),
									Flags:     append([]cli.Flag{jsonFlag}, analyticsParticipantListFlags()...),
								},
							},
						},
						{
							Name:      "recording",
							Usage:     "Download a session's audio recording or chat history (requires --experimental-auth)",
							UsageText: "lk analytics session recording SESSION_ID [--type audio|chat-history] [-o FILE] [--url-only]",
							Description: "Downloads one of the recordings a session's agent uploaded to LiveKit Cloud when the session ended: " +
								"by default its audio recording, the conversation as an Ogg audio file; with --type chat-history, " +
								"its chat history, the conversation as the agent recorded it (messages, tool calls and handoffs), as JSON. " +
								"The file downloads straight from the project's data region.\n\n" +
								"It saves to SESSION_ID-audio.ogg or SESSION_ID-chat-history.json in the working directory, " +
								"numbered (SESSION_ID-audio-1.ogg, ...) rather than replacing a file already there, " +
								"and prints the path it saved to. -o saves elsewhere: to a file, replaced if it exists, " +
								"or into a directory, under the default name.\n\n" +
								"--url-only prints a signed URL for the file instead, valid for 15 minutes, to download it some other way. " +
								"--json prints the API's response, signed URL and recording start included, and saves nothing.",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(sessionRecordingOptionsFrom, fetchSessionRecording),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsRecordingFlags()...),
						},
						{
							Name:      "transcript",
							Usage:     "Print a session's transcript (requires --experimental-auth)",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(pageOptions, fetchSessionTranscript),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsTranscriptFlags()...),
						},
						{
							Name:      "logs",
							Usage:     "Print a session's agent logs (requires --experimental-auth)",
							UsageText: "lk analytics session logs SESSION_ID [--log-level LEVEL ...] [--sort-order asc|desc]",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(logOptions, fetchSessionLogs),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsLogFlags()...),
						},
						{
							Name:      "traces",
							Usage:     "Print a session's trace spans as a tree (requires --experimental-auth)",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(pageOptions, fetchSessionTraces),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsTraceFlags()...),
						},
						{
							Name:      "metrics",
							Usage:     "Print a session's agent metrics (requires --experimental-auth)",
							UsageText: "lk analytics session metrics SESSION_ID [--name NAME ...]",
							Description: "Prints the OpenTelemetry metric points the session's agents exported, " +
								"such as lk.agents.turn.e2e_latency and lk.agents.usage.llm_input_tokens, one line per point: " +
								"its time, its metric's name, and a gauge's or sum's value or a histogram's count, sum, min and max. " +
								"--json prints the points with their attributes and histogram buckets.\n\n" +
								"These are the raw metrics agents emit, so they won't exactly match the dashboard's metrics panel, " +
								"which derives its numbers from the transcript and traces.",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(metricOptions, fetchSessionMetrics),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsMetricFlags()...),
						},
						{
							Name:      "events",
							Usage:     "Print a session's events (requires --experimental-auth)",
							UsageText: "lk analytics session events SESSION_ID [--type TYPE ...] [--participant PA_ID] [--sort-order asc|desc]",
							Description: "Prints the session's events, such as participants joining and leaving or tracks being published, " +
								"one line per event: its time, its type, the participant identity and participant session it is about, " +
								"and its payload. --json prints the events as the API sends them.\n\n" +
								"With no --type it prints the events the dashboard's events table shows: participant_joined, " +
								"participant_left, participant_active, participant_resumed, room_created, room_ended and api_call. " +
								"--type asks for others, such as track_published or track_muted, matched exactly. " +
								"--participant takes a participant session id (PA_...), as `lk analytics session participant list` prints it. " +
								"With --participant and no --type it prints the events the dashboard's participant events table shows, " +
								"track events included: participant_joined, participant_left, participant_resumed, participant_active, " +
								"track_muted, track_unmuted, track_published, track_unpublished, track_subscribed, " +
								"track_subscribe_requested and track_subscribe_failed. " +
								"Events are kept for 60 days.",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(eventOptions, fetchSessionEvents),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsEventFlags()...),
						},
					},
				},
			},
		},
	}
)

type analyticsListResponse struct {
	Sessions []*analyticsSession `json:"sessions"`
}

type analyticsSession struct {
	SessionID             string          `json:"sessionId"`
	RoomName              string          `json:"roomName"`
	CreatedAt             string          `json:"createdAt"`
	EndedAt               string          `json:"endedAt"`
	LastActive            string          `json:"lastActive"`
	BandwidthIn           json.RawMessage `json:"bandwidthIn"`
	BandwidthOut          json.RawMessage `json:"bandwidthOut"`
	Egress                json.RawMessage `json:"egress"`
	NumParticipants       int             `json:"numParticipants"`
	NumActiveParticipants int             `json:"numActiveParticipants"`
}

type analyticsSessionDetails struct {
	RoomID            string                  `json:"roomId"`
	RoomName          string                  `json:"roomName"`
	Bandwidth         json.RawMessage         `json:"bandwidth"`
	StartTime         string                  `json:"startTime"`
	EndTime           string                  `json:"endTime"`
	NumParticipants   int                     `json:"numParticipants"`
	ConnectionMinutes json.RawMessage         `json:"connectionMinutes"`
	Participants      []*analyticsParticipant `json:"participants"`
}

type analyticsParticipant struct {
	ParticipantIdentity string `json:"participantIdentity"`
	ParticipantName     string `json:"participantName"`
	JoinedAt            string `json:"joinedAt"`
	LeftAt              string `json:"leftAt"`
	Region              string `json:"region"`
	ConnectionType      string `json:"connectionType"`
	SDKVersion          string `json:"sdkVersion"`
}

// analyticsSessionListFlags returns fresh instances of the session list's own
// flags (the shared jsonFlag is added by the command). Tests build commands from
// it too: urfave/cli caches parse state on flag values, so reusing the command
// tree's flags across runs would leak state between cases.
func analyticsSessionListFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:  "limit",
			Usage: "Maximum number of sessions to return",
			Value: defaultAnalyticsLimit,
		},
		&cli.IntFlag{
			Name:  "page",
			Usage: "Page number (starts at 0)",
		},
		&cli.StringFlag{
			Name:  "since",
			Usage: "List sessions started within the last `DURATION`, such as 90m, 6h, 14d or 2w (default 14d unless --start is set); can't be used with --start or --end",
		},
		&cli.StringFlag{
			Name:  "start",
			Usage: "List sessions started on or after `YYYY-MM-DD` (UTC)",
		},
		&cli.StringFlag{
			Name:  "end",
			Usage: "List sessions started before `YYYY-MM-DD` (UTC, exclusive); requires --start",
		},
		// experimental-auth only: the Public API is cursor-paginated and has
		// filters the CLI doesn't yet send to the API-key endpoint. Hidden like
		// the rest of the experimental surface; pass the nextCursor from a prior
		// `--json` listing to fetch the next page.
		&cli.StringFlag{
			Name:   "cursor",
			Usage:  "Page `CURSOR` from a prior --json listing (requires --experimental-auth)",
			Hidden: true,
		},
		&cli.StringSliceFlag{
			Name:   "status",
			Usage:  "List sessions in `STATUS` (active or closed); repeatable (requires --experimental-auth)",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "room",
			Usage:  "List sessions whose room name starts with `PREFIX` (case-sensitive; requires --experimental-auth)",
			Hidden: true,
		},
		&cli.StringSliceFlag{
			Name:   "tag",
			Usage:  "List sessions carrying `TAG`; repeatable, matches any (requires --experimental-auth)",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "sort-order",
			Usage:  "Order by start time: `ORDER` asc or desc, default desc (requires --experimental-auth)",
			Hidden: true,
		},
	}
}

// pageFlags returns fresh --limit and --cursor flags for a command that prints
// one page of a session's items, for the same reason as
// analyticsSessionListFlags. These commands exist only under
// --experimental-auth, so no flag needs an auth-mode check.
func pageFlags(defaultLimit int, items string) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:  "limit",
			Usage: "Maximum number of " + items + " to read (the server caps a page's size)",
			Value: defaultLimit,
		},
		// Hidden like session list's: pass the cursor a prior page printed,
		// with the same filters and order, to fetch the next.
		&cli.StringFlag{
			Name:   "cursor",
			Usage:  "Page `CURSOR` from a prior page",
			Hidden: true,
		},
	}
}

// analyticsParticipantListFlags returns fresh instances of the participant
// list's own flags (the shared jsonFlag is added by the command).
func analyticsParticipantListFlags() []cli.Flag {
	return append([]cli.Flag{
		&cli.StringFlag{
			Name:  "sort-by",
			Usage: "Order by `FIELD`: joined or left, default joined",
		},
		&cli.StringFlag{
			Name:  "sort-order",
			Usage: "Order direction: `ORDER` asc or desc, default desc",
		},
	}, pageFlags(defaultPageLimit, "participants")...)
}

// analyticsRecordingFlags returns fresh instances of the recording download's
// own flags (the shared jsonFlag is added by the command), for the same reason
// as analyticsSessionListFlags.
func analyticsRecordingFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "type",
			Usage: "`TYPE` of recording to download: audio (the conversation's audio, Ogg) or chat-history (the agent's record of the conversation, JSON)",
			Value: public.RecordingAudio,
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Save to `FILE`, replacing it if it exists, or into a directory under the default name (default SESSION_ID-audio.ogg or SESSION_ID-chat-history.json, numbered rather than replacing a file)",
		},
		&cli.BoolFlag{
			Name:  "url-only",
			Usage: "Print a signed URL for the recording, valid for 15 minutes, instead of downloading it",
		},
	}
}

// readFlags returns fresh --limit and --cursor flags for a command that reads
// a session's items (see readPages), for the same reason as
// analyticsSessionListFlags. --limit has no default here: without it the
// command reads one page of the server's default size, so the server alone
// decides how much a plain read returns.
func readFlags(items string) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:        "limit",
			Usage:       "Maximum number of " + items + " to read, reading every page up to it (default one page of the server's default size)",
			HideDefault: true,
		},
		// Hidden like session list's: pass the cursor a read that stopped at its
		// limit printed, with the same filters and order, to read the rest.
		&cli.StringFlag{
			Name:   "cursor",
			Usage:  "Read from `CURSOR`, where a prior read stopped",
			Hidden: true,
		},
	}
}

// analyticsTranscriptFlags returns fresh instances of the transcript's own
// flags (the shared jsonFlag is added by the command).
func analyticsTranscriptFlags() []cli.Flag {
	return readFlags("transcript items")
}

// analyticsLogFlags returns fresh instances of the agent logs' own flags (the
// shared jsonFlag is added by the command).
func analyticsLogFlags() []cli.Flag {
	return append([]cli.Flag{
		&cli.StringSliceFlag{
			Name:  "log-level",
			Usage: "Print only records at `LEVEL`: trace, debug, info, warn, error or fatal; repeatable, matches any (default every record)",
		},
		&cli.StringFlag{
			Name:  "sort-order",
			Usage: "Order by time: `ORDER` asc or desc, default asc",
		},
	}, readFlags("records")...)
}

// analyticsTraceFlags returns fresh instances of the trace spans' own flags
// (the shared jsonFlag is added by the command). Their parents were read
// before a --cursor, so spans read from one print as roots.
func analyticsTraceFlags() []cli.Flag {
	return readFlags("spans")
}

// analyticsMetricFlags returns fresh instances of the agent metrics' own flags
// (the shared jsonFlag is added by the command).
func analyticsMetricFlags() []cli.Flag {
	return append([]cli.Flag{
		&cli.StringSliceFlag{
			Name:  "name",
			Usage: "Print only the points of the metric named `NAME`, matched exactly, such as lk.agents.turn.e2e_latency; repeatable (default every metric)",
		},
	}, readFlags("points")...)
}

// analyticsEventFlags returns fresh instances of the session events' own flags
// (the shared jsonFlag is added by the command).
func analyticsEventFlags() []cli.Flag {
	return append([]cli.Flag{
		&cli.StringSliceFlag{
			Name:  "type",
			Usage: "Print only events of `TYPE`, such as participant_joined or track_published; repeatable, matches any (default the dashboard's seven, or with --participant its eleven participant types)",
		},
		&cli.StringFlag{
			Name:  "participant",
			Usage: "Print only the events of the participant session `PA_ID`, as `lk analytics session participant list` prints it; with no --type, the dashboard's eleven participant types, track events included",
		},
		&cli.StringFlag{
			Name:  "sort-order",
			Usage: "Order by time: `ORDER` asc or desc, default asc",
		},
	}, readFlags("events")...)
}

// analyticsListModeFlags: --page (offset) exists only on the API-key analytics
// endpoint, and the Public API session list is cursor-paginated. --since,
// --start and --end work in both modes. The filter flags are sent only to the Public API for now.
var analyticsListModeFlags = authModeFlags{
	legacyOnly:       []string{"page"},
	experimentalOnly: []string{"cursor", "status", "room", "tag", "sort-order"},
}

func listAnalyticsSessions(ctx context.Context, cmd *cli.Command) error {
	if err := analyticsListModeFlags.validate(cmd); err != nil {
		return err
	}
	if experimentalAuthEnabled(cmd) {
		return listUserAnalyticsSessions(ctx, cmd)
	}

	query, err := buildAnalyticsListQuery(cmd)
	if err != nil {
		return err
	}

	body, err := callAnalyticsAPI(ctx, cmd, "", query)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		var obj any
		if err := json.Unmarshal(body, &obj); err != nil {
			return err
		}
		util.PrintJSON(obj)
		return nil
	}

	var res analyticsListResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("failed to parse analytics list response: %w", err)
	}

	if len(res.Sessions) == 0 {
		out.Result("No sessions found")
		if usesDefaultWindow(cmd) {
			printDefaultWindowHint()
		}
		return nil
	}

	table := util.CreateTable().
		Headers("Session ID", "Room", "Created", "Ended", "Participants", "Active", "Bandwidth In", "Bandwidth Out")

	for _, session := range res.Sessions {
		if session == nil {
			continue
		}
		table.Row(
			util.Dash(session.SessionID),
			util.Dash(session.RoomName),
			util.Dash(session.CreatedAt),
			util.Dash(session.EndedAt),
			strconv.Itoa(session.NumParticipants),
			strconv.Itoa(session.NumActiveParticipants),
			util.FormatBytes(session.BandwidthIn),
			util.FormatBytes(session.BandwidthOut),
		)
	}

	out.Result(table)
	return nil
}

func getAnalyticsSession(ctx context.Context, cmd *cli.Command) error {
	if experimentalAuthEnabled(cmd) {
		return getUserAnalyticsSession(ctx, cmd)
	}

	sessionID, err := extractArg(cmd)
	if err != nil {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("session ID is required")
	}

	body, err := callAnalyticsAPI(ctx, cmd, sessionID, nil)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		var obj any
		if err := json.Unmarshal(body, &obj); err != nil {
			return err
		}
		util.PrintJSON(obj)
		return nil
	}

	var details analyticsSessionDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return fmt.Errorf("failed to parse analytics details response: %w", err)
	}

	summary := util.CreateTable().
		Headers("Session ID", "Room", "Start", "End", "Participants", "Connection Minutes", "Bandwidth").
		Row(
			util.Dash(details.RoomID),
			util.Dash(details.RoomName),
			util.Dash(details.StartTime),
			util.Dash(details.EndTime),
			strconv.Itoa(details.NumParticipants),
			util.RawJSONToString(details.ConnectionMinutes),
			util.FormatBytes(details.Bandwidth),
		)
	out.Result(summary)

	if len(details.Participants) == 0 {
		return nil
	}

	participantTable := util.CreateTable().
		Headers("Identity", "Name", "Joined", "Left", "Region", "Connection", "SDK")

	for _, participant := range details.Participants {
		if participant == nil {
			continue
		}
		participantTable.Row(
			util.Dash(participant.ParticipantIdentity),
			util.Dash(participant.ParticipantName),
			util.Dash(participant.JoinedAt),
			util.Dash(participant.LeftAt),
			util.Dash(participant.Region),
			util.Dash(participant.ConnectionType),
			util.Dash(participant.SDKVersion),
		)
	}

	out.Result(participantTable)
	return nil
}

func buildAnalyticsListQuery(cmd *cli.Command) (url.Values, error) {
	query := url.Values{}

	limit := cmd.Int("limit")
	if limit <= 0 {
		return nil, errors.New("limit must be greater than 0")
	}
	query.Set("limit", strconv.Itoa(limit))

	if cmd.IsSet("page") {
		page := cmd.Int("page")
		if page < 0 {
			return nil, errors.New("page must be greater than or equal to 0")
		}
		query.Set("page", strconv.Itoa(page))
	}

	// The API-key endpoint reads whole UTC days, so a --since window widens to
	// start at midnight of the day it begins.
	start, end, err := analyticsWindow(cmd)
	if err != nil {
		return nil, err
	}

	if !start.IsZero() {
		query.Set("start", start.UTC().Format(time.DateOnly))
	}
	if !end.IsZero() {
		query.Set("end", end.UTC().Format(time.DateOnly))
	}

	return query, nil
}

// analyticsNow is the clock --since counts back from; tests replace it.
var analyticsNow = time.Now

// analyticsWindow reads a session listing's window from --since, or else from
// --start and --end, or else defaults it to the last defaultAnalyticsWindow
// rather than leaving it to the endpoint (the Public API searches only the
// last 24 hours). A window starting at now - d leaves end zero: both
// endpoints list up to now.
func analyticsWindow(cmd *cli.Command) (time.Time, time.Time, error) {
	if usesDefaultWindow(cmd) {
		return analyticsNow().Add(-defaultAnalyticsWindow), time.Time{}, nil
	}
	since := cmd.String("since")
	if since == "" {
		return validateAnalyticsDateRange(cmd.String("start"), cmd.String("end"))
	}
	if cmd.IsSet("start") || cmd.IsSet("end") {
		return time.Time{}, time.Time{}, errors.New("--since can't be used with --start or --end")
	}
	d, err := parseLookback(since)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return analyticsNow().Add(-d), time.Time{}, nil
}

// parseLookback parses a --since duration: a whole number of days (14d) or
// weeks (2w), or anything time.ParseDuration reads, such as 6h or 1h30m. It
// must be positive.
func parseLookback(s string) (time.Duration, error) {
	invalid := fmt.Errorf("invalid duration %q, expected a positive duration such as 90m, 6h, 14d or 2w", s)
	s = strings.TrimSpace(s)
	var d time.Duration
	if n := len(s); n > 1 && (s[n-1] == 'd' || s[n-1] == 'w') {
		count, err := strconv.Atoi(s[:n-1])
		if err != nil {
			return 0, invalid
		}
		unit := 24 * time.Hour
		if s[n-1] == 'w' {
			unit *= 7
		}
		d = time.Duration(count) * unit
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, invalid
		}
	}
	if d <= 0 {
		return 0, invalid
	}
	return d, nil
}

// validateAnalyticsDateRange parses --start and --end. Both endpoints read them
// as the half-open window [start, end), so equal dates are an empty window. A
// lone --end is rejected: each endpoint fills in a different start (the API-key
// endpoint from now, the Public API from end), and neither is what it suggests.
func validateAnalyticsDateRange(startDate, endDate string) (time.Time, time.Time, error) {
	var (
		start time.Time
		end   time.Time
		err   error
	)

	if startDate != "" {
		start, err = time.Parse("2006-01-02", startDate)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start date %q, expected YYYY-MM-DD", startDate)
		}
	}

	if endDate != "" {
		end, err = time.Parse("2006-01-02", endDate)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end date %q, expected YYYY-MM-DD", endDate)
		}
	}

	if !end.IsZero() && start.IsZero() {
		return time.Time{}, time.Time{}, errors.New("--end requires --start")
	}
	if !end.IsZero() && !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("start date must be before end date")
	}

	return start, end, nil
}

func callAnalyticsAPI(ctx context.Context, cmd *cli.Command, sessionID string, query url.Values) ([]byte, error) {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return nil, err
	}

	projectID, err := resolveAnalyticsProjectID()
	if err != nil {
		return nil, err
	}

	token, err := createAnalyticsAccessToken(project.APIKey, project.APISecret)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSuffix(serverURL, "/")
	endpoint := fmt.Sprintf("%s/api/project/%s/sessions", baseURL, url.PathEscape(projectID))
	if sessionID != "" {
		endpoint += "/" + url.PathEscape(sessionID)
	}

	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	if len(query) != 0 {
		reqURL.RawQuery = query.Encode()
	}

	if printCurl {
		out.Resultf("curl -H \"Authorization: Bearer %s\" \"%s\"\n", token, reqURL.String())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = authutil.NewHeaderWithToken(token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, mapAnalyticsHTTPError(resp.StatusCode, string(body))
	}

	return body, nil
}

func resolveAnalyticsProjectID() (string, error) {
	if project != nil && project.ProjectId != "" {
		return project.ProjectId, nil
	}

	if project == nil {
		return "", fmt.Errorf("%s; %s", analyticsProjectIDRequirement, analyticsProjectSelectHint)
	}

	projectName := project.Name
	if strings.TrimSpace(projectName) == "" {
		projectName = "<selected>"
	}

	return "", fmt.Errorf(
		"selected project [%s] is missing project_id; %s. %s",
		projectName,
		analyticsProjectIDRequirement,
		analyticsProjectSelectHint,
	)
}

func createAnalyticsAccessToken(apiKey, apiSecret string) (string, error) {
	token, err := auth.NewAccessToken(apiKey, apiSecret).
		SetVideoGrant(&auth.VideoGrant{RoomList: true}).
		SetIdentity("lk-analytics").
		ToJWT()
	if err != nil {
		return "", err
	}
	return token, nil
}

func mapAnalyticsHTTPError(statusCode int, body string) error {
	trimmedBody := strings.TrimSpace(body)
	if len(trimmedBody) > 200 {
		trimmedBody = trimmedBody[:200] + "..."
	}

	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		lowerBody := strings.ToLower(trimmedBody)
		if strings.Contains(lowerBody, "scale plan") {
			return errors.New("analytics API requires LiveKit Cloud Scale plan or higher")
		}
		if trimmedBody == "" {
			return fmt.Errorf("analytics API is not authorized (HTTP %d)", statusCode)
		}
		return fmt.Errorf("analytics API is not authorized (HTTP %d): %s", statusCode, trimmedBody)
	}

	if statusCode == http.StatusNotFound {
		if trimmedBody == "" {
			return errors.New("analytics resource not found")
		}
		return fmt.Errorf("analytics resource not found: %s", trimmedBody)
	}

	if trimmedBody == "" {
		return fmt.Errorf("analytics API request failed with HTTP %d", statusCode)
	}
	return fmt.Errorf("analytics API request failed with HTTP %d: %s", statusCode, trimmedBody)
}

// listUserAnalyticsSessions lists project sessions via the Public API under
// --experimental-auth. The project comes from the global --project selection (or
// a cached alias).
func listUserAnalyticsSessions(ctx context.Context, cmd *cli.Command) error {
	opts, err := sessionListOptions(cmd)
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
	sessions, nextCursor, err := client.ListProjectSessions(ctx, projectID, opts)
	if err != nil {
		return cloudAPIError(err)
	}
	asJSON := cmd.Bool("json")
	if err := render.SessionsPage(out, asJSON, sessions, nextCursor); err != nil {
		return err
	}
	if len(sessions) == 0 && !asJSON && usesDefaultWindow(cmd) {
		printDefaultWindowHint()
	}
	return nil
}

// usesDefaultWindow reports whether a listing searches the default window:
// no --since, --start or --end set one, and no cursor carries the window of
// the listing it came from.
func usesDefaultWindow(cmd *cli.Command) bool {
	for _, name := range []string{"since", "start", "end", "cursor"} {
		if cmd.String(name) != "" {
			return false
		}
	}
	return true
}

// printDefaultWindowHint says an empty listing searched only the default
// window, so a quiet fortnight doesn't read as a project with no sessions.
func printDefaultWindowHint() {
	out.Statusf("Only the last 14 days were searched — re-run with %s or %s to look further back",
		util.Accented("--since DURATION"), util.Accented("--start YYYY-MM-DD"))
}

// sessionListOptions reads the session list flags for the Public API. --start
// and --end mean the same as on the API-key endpoint: UTC dates bounding when a
// session started, end exclusive. --since starts the window that long before
// now, to the second. Unknown status and sort order names fail here,
// before the project lookup, like the date and limit checks.
func sessionListOptions(cmd *cli.Command) (public.SessionListOptions, error) {
	limit := cmd.Int("limit")
	if limit <= 0 {
		return public.SessionListOptions{}, errors.New("limit must be greater than 0")
	}
	start, end, err := analyticsWindow(cmd)
	if err != nil {
		return public.SessionListOptions{}, err
	}
	opts := public.SessionListOptions{
		Limit:      int32(limit),
		Cursor:     cmd.String("cursor"),
		Start:      start,
		End:        end,
		Statuses:   cmd.StringSlice("status"),
		RoomPrefix: cmd.String("room"),
		Tags:       cmd.StringSlice("tag"),
		SortOrder:  cmd.String("sort-order"),
	}
	if err := opts.Validate(); err != nil {
		return public.SessionListOptions{}, err
	}
	return opts, nil
}

// getUserAnalyticsSession fetches a single project session and its detail
// (totals, timelines, first page of participants) via the Public API under
// --experimental-auth.
func getUserAnalyticsSession(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	sessionID, err := extractArg(cmd)
	if err != nil {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("session ID is required")
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	session, detail, err := client.GetSession(ctx, projectID, sessionID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.SessionDetail(out, cmd.Bool("json"), *session, detail)
}

// sessionRead builds the action of a command that reads one thing about a
// session — its participants, recordings, transcript, agent logs, trace
// spans, agent metrics or events — which only the Public API serves. The
// action refuses to run without --experimental-auth before checking anything
// else, then reads the SESSION_ID argument and the command's options, so a
// bad flag fails before the project lookup, and hands fetch a client signed
// in as the user and the selected project.
func sessionRead[O any](
	readOptions func(*cli.Command) (O, error),
	fetch func(ctx context.Context, client *public.Client, projectID, sessionID string, opts O, asJSON bool) error,
) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if err := requireExperimentalAuth(cmd); err != nil {
			return err
		}
		sessionID, err := extractArg(cmd)
		if err != nil {
			_ = cli.ShowSubcommandHelp(cmd)
			return errors.New("session ID is required")
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
		return fetch(ctx, client, projectID, sessionID, opts, cmd.Bool("json"))
	}
}

// fetchSessionParticipants reads one page of a session's participants and
// prints it.
func fetchSessionParticipants(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.ParticipantListOptions, asJSON bool) error {
	participants, nextCursor, err := client.ListSessionParticipants(ctx, projectID, sessionID, opts)
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "participants", projectReadAccess)
	}
	return render.SessionParticipantsPage(out, asJSON, participants, nextCursor)
}

// participantListOptions reads the participant list flags. Bad limits and sort
// names fail here, before the project lookup.
func participantListOptions(cmd *cli.Command) (public.ParticipantListOptions, error) {
	page, err := pageOptions(cmd)
	if err != nil {
		return public.ParticipantListOptions{}, err
	}
	opts := public.ParticipantListOptions{
		PageOptions: page,
		SortBy:      cmd.String("sort-by"),
		SortOrder:   cmd.String("sort-order"),
	}
	if err := opts.Validate(); err != nil {
		return public.ParticipantListOptions{}, err
	}
	return opts, nil
}

// sessionRecordingOptions is what `session recording` fetches and where it
// puts it.
type sessionRecordingOptions struct {
	public.RecordingURLOptions
	// Output is where -o saves: a file, replaced if it exists, or a directory
	// to save under the default name in. Empty saves under the default name in
	// the working directory. A default name never replaces a file; see
	// recordingDestination.
	Output string
	// URLOnly prints the signed URL response instead of downloading; --json
	// does too.
	URLOnly bool
}

// sessionRecordingOptionsFrom reads the recording flags. An unknown type, or
// an --output that won't be saved to, fails here, before the project lookup.
func sessionRecordingOptionsFrom(cmd *cli.Command) (sessionRecordingOptions, error) {
	if cmd.Args().Len() > 1 {
		return sessionRecordingOptions{}, fmt.Errorf("unexpected argument %q; save elsewhere with -o FILE", cmd.Args().Get(1))
	}
	opts := sessionRecordingOptions{
		RecordingURLOptions: public.RecordingURLOptions{Recording: strings.ToLower(strings.TrimSpace(cmd.String("type")))},
		Output:              cmd.String("output"),
		URLOnly:             cmd.Bool("url-only"),
	}
	if err := opts.Validate(); err != nil {
		return sessionRecordingOptions{}, err
	}
	if opts.URLOnly && cmd.Bool("json") {
		return sessionRecordingOptions{}, errors.New("--url-only can't be used with --json, which already prints the signed URL response and saves nothing")
	}
	if opts.Output != "" {
		switch {
		case opts.URLOnly:
			return sessionRecordingOptions{}, errors.New("--output can't be used with --url-only, which saves nothing")
		case cmd.Bool("json"):
			return sessionRecordingOptions{}, errors.New("--output can't be used with --json, which prints the signed URL response and saves nothing")
		}
	}
	return opts, nil
}

// fetchSessionRecording asks the Public API to sign a URL for the recording,
// then prints the response (--url-only, --json) or downloads the recording to
// a file and prints the file's path. The download goes straight to the object
// store; nothing passes through the API.
func fetchSessionRecording(ctx context.Context, client *public.Client, projectID, sessionID string, opts sessionRecordingOptions, asJSON bool) error {
	res, err := client.GetSessionRecordingURL(ctx, projectID, sessionID, opts.RecordingURLOptions)
	if err != nil {
		return sessionRecordingError(err, projectID, sessionID, opts.Recording)
	}
	if opts.URLOnly || asJSON {
		return render.RecordingURL(out, asJSON, *res)
	}

	path, replace, err := recordingDestination(sessionID, opts)
	if err != nil {
		return err
	}
	path, n, err := saveRecording(ctx, *res.Url, path, replace)
	if err != nil {
		return err
	}
	return render.RecordingSaved(out, *res, render.SavedRecording{
		SessionID: sessionID,
		Recording: opts.Recording,
		File:      path,
		Bytes:     n,
	})
}

// recordingDestination is where a download saves, and whether it replaces a
// file already there. A file -o names is replaced, as asked; the default
// name, in the working directory or in a directory -o names, never is:
// saveRecording numbers it instead.
func recordingDestination(sessionID string, opts sessionRecordingOptions) (path string, replace bool, err error) {
	name := defaultRecordingFile(sessionID, opts.Recording)
	if opts.Output == "" {
		return name, false, nil
	}
	if strings.HasSuffix(opts.Output, string(filepath.Separator)) || strings.HasSuffix(opts.Output, "/") {
		return filepath.Join(opts.Output, name), false, nil
	}
	info, err := os.Stat(opts.Output)
	switch {
	case err == nil && info.IsDir():
		return filepath.Join(opts.Output, name), false, nil
	case err == nil || errors.Is(err, os.ErrNotExist):
		return opts.Output, true, nil
	default:
		return "", false, err
	}
}

// defaultRecordingFile names a downloaded recording in the working directory:
// Ogg for audio, JSON for chat history. A path separator in the session id
// can't send the file elsewhere.
func defaultRecordingFile(sessionID, recording string) string {
	ext := ".ogg"
	if recording == public.RecordingChatHistory {
		ext = ".json"
	}
	name := strings.NewReplacer("/", "_", `\`, "_").Replace(sessionID)
	return name + "-" + recording + ext
}

// saveRecording downloads a signed recording URL to path and returns the path
// it saved to. Unless replace is set it never replaces a file: when path is
// taken it saves to the first free path-1.ext, path-2.ext and so on, claiming
// the name before downloading so a concurrent run can't take it too. It
// writes a hidden temporary file beside the destination (readable only by the
// user, like the recording it holds) and renames it into place once the
// download completes, so a failed download leaves no partial file, no claimed
// name, and an existing file intact.
func saveRecording(ctx context.Context, signedURL, path string, replace bool) (string, int64, error) {
	if !replace {
		var err error
		if path, err = claimFreeName(path); err != nil {
			return "", 0, err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		if !replace {
			_ = os.Remove(path)
		}
		return "", 0, err
	}
	n, err := public.DownloadRecording(ctx, signedURL, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		if !replace {
			_ = os.Remove(path)
		}
		return "", 0, err
	}
	return path, n, nil
}

// maxRecordingNameTries bounds how many numbered names claimFreeName tries.
const maxRecordingNameTries = 1000

// claimFreeName creates an empty file at path, or else at the first of
// path-1.ext, path-2.ext and so on that doesn't exist, and returns its name.
// Creating it exclusively claims the name: no other run picks it too.
func claimFreeName(path string) (string, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 0; i < maxRecordingNameTries; i++ {
		name := path
		if i > 0 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, f.Close()
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%s and %d numbered names after it already exist; pass -o FILE to save elsewhere", path, maxRecordingNameTries-1)
}

// sessionRecordingError explains why a session has nothing to download, and
// otherwise annotates the error like the other Public API commands.
func sessionRecordingError(err error, projectID, sessionID, recording string) error {
	label := render.RecordingLabel(recording)
	if dashboardURL, ok := public.ObservabilityDisabled(err); ok {
		return observabilityDisabledError(sessionID, label, dashboardURL)
	}
	if public.IsNotFound(err) {
		// The API answers an unknown session, or a mistyped --project, the same
		// way, so say so too and name the project.
		return fmt.Errorf("session %s has no %s (%w): it wasn't recorded, has expired, "+
			"or the session ended less than a minute ago and the recording is still being indexed; "+
			"or there is no such session in project %s", sessionID, label, err, projectID)
	}
	return sessionAPIError(err, label, projectAdminAccess)
}

// observabilityDisabledError says a session has nothing to read because the
// project's user data recording is off, and where an admin turns it on.
func observabilityDisabledError(sessionID, what, dashboardURL string) error {
	where := "in the project's settings on the LiveKit Cloud dashboard"
	if dashboardURL != "" {
		where = "at " + dashboardURL
	}
	return fmt.Errorf("session %s has no %s: user data recording is off for this project, so nothing was recorded. "+
		"A project admin can turn it on %s; it records new sessions only", sessionID, what, where)
}

// pageOptions reads a session read's --limit and --cursor flags. A limit
// given that isn't positive fails here, before the project lookup; none
// leaves the limit 0, the server's default.
func pageOptions(cmd *cli.Command) (public.PageOptions, error) {
	limit := cmd.Int("limit")
	if limit < 0 || (limit == 0 && cmd.IsSet("limit")) {
		return public.PageOptions{}, errors.New("limit must be greater than 0")
	}
	return public.PageOptions{Limit: int32(limit), Cursor: cmd.String("cursor")}, nil
}

// readPages reads a session's items from page.Cursor and returns them with
// the cursor where it stopped: empty once it read the last page. With no
// page.Limit it reads one page, sending no page size, so the server picks it.
// With one it reads page by page until it has page.Limit items, asking each
// page for as many as are left; the server caps a page's size, so a large
// limit takes several. fetch reads one page.
func readPages[T any](page public.PageOptions, fetch func(public.PageOptions) ([]T, string, error)) ([]T, string, error) {
	var all []T
	next := page.Cursor
	for {
		items, cursor, err := fetch(public.PageOptions{
			Limit:  max(0, page.Limit-int32(len(all))),
			Cursor: next,
		})
		if err != nil {
			return nil, "", err
		}
		all = append(all, items...)
		// A cursor that didn't move would read the same page forever.
		stuck := cursor == next
		next = cursor
		if next == "" || stuck || len(all) >= int(page.Limit) {
			return all, next, nil
		}
	}
}

// fetchSessionTranscript reads a session's transcript, every page up to
// opts.Limit items, and prints it like the agent debugger's history, saying
// why when there is none.
func fetchSessionTranscript(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.PageOptions, asJSON bool) error {
	page := &public.TranscriptPage{}
	var err error
	page.Items, page.NextCursor, err = readPages(opts, func(o public.PageOptions) ([]public.TranscriptItem, string, error) {
		p, err := client.GetSessionTranscript(ctx, projectID, sessionID, o)
		if err != nil {
			return nil, "", err
		}
		page.SkippedRecords += p.SkippedRecords
		return p.Items, p.NextCursor, nil
	})
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "transcript", projectAdminAccess)
	}
	// Only an empty page needs a reason, and only the server's empty: no items,
	// nothing skipped and no next cursor. Anything else prints no reason, so
	// it skips emptyTranscriptReason's GetSession too.
	var empty string
	if len(page.Items) == 0 && page.SkippedRecords == 0 && page.NextCursor == "" {
		empty = emptyTranscriptReason(ctx, client, projectID, sessionID, opts.Cursor != "")
	}
	return render.SessionTranscript(out, asJSON, *page, empty, transcriptLines)
}

// transcriptLines renders a recorded transcript as `lk agent debugger` and
// `lk agent console` render a live one, adding each item's time and its
// latencies, followed by a blank line.
func transcriptLines(items []public.TranscriptItem) []string {
	opts := renderOptions{Metrics: true, Times: true, UserLabel: "User"}
	var lines []string
	for _, e := range transcriptItemsToTurnEvents(items) {
		if line := renderTurnEvent(e, opts); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return lines
}

// emptyTranscriptReason explains a page with no items. The transcript read
// doesn't say whether the session is still going, so a first page asks
// GetSession: an active session's agent exports its transcript only when the
// session ends. A session that stopped reporting keeps its ACTIVE status but
// gets an end time, so only one with no end time is still active.
func emptyTranscriptReason(ctx context.Context, client *public.Client, projectID, sessionID string, laterPage bool) string {
	if laterPage {
		return "No more transcript items"
	}
	if session, _, err := client.GetSession(ctx, projectID, sessionID); err == nil &&
		util.Deref(session.Status) == oapi.SESSIONSTATUSACTIVE && session.EndedAt == nil {
		return fmt.Sprintf("Session %s is still active: its transcript appears after it ends", sessionID)
	}
	return fmt.Sprintf("Session %s has no transcript to read. The agent exports it when the session ends, "+
		"and it can be read a minute or two after the session ends (on a project with PII redaction, once "+
		"the recording is redacted); a session without an agent has none", sessionID)
}

// sessionReadError explains why a session has no participants, transcript,
// agent logs, trace spans, agent metrics or events (what) to print, and
// otherwise annotates the error like the other Public API commands. access
// is what the read requires.
func sessionReadError(err error, projectID, sessionID, what string, access sessionReadAccess) error {
	if dashboardURL, ok := public.ObservabilityDisabled(err); ok {
		return observabilityDisabledError(sessionID, what, dashboardURL)
	}
	if public.IsNotFound(err) {
		return fmt.Errorf("no session %s in project %s (%w)", sessionID, projectID, err)
	}
	return sessionAPIError(err, what, access)
}

// sessionReadAccess is the project access a session read requires.
type sessionReadAccess int

const (
	// projectReadAccess reads: a session's participants and events.
	projectReadAccess sessionReadAccess = iota
	// projectAdminAccess reads, which can hold user data: a session's
	// recordings, transcript, agent logs, trace spans and agent metrics.
	projectAdminAccess
)

// sessionAPIError annotates a Public API error from a session read like
// cloudAPIError, except a permission denial: cloudAPIError suggests API-key
// credentials, which these Public-API-only reads can't use, so it says what
// the read requires instead. what names what the read returns.
func sessionAPIError(err error, what string, access sessionReadAccess) error {
	if !public.IsPermissionDenied(err) {
		return cloudAPIError(err)
	}
	if access == projectAdminAccess {
		return fmt.Errorf("%w — reading a session's %s requires being a project admin", err, what)
	}
	return fmt.Errorf("%w — you don't have access to this project", err)
}

// logOptions reads the agent logs flags. A bad limit, level or sort order
// fails here, before the project lookup.
func logOptions(cmd *cli.Command) (public.LogOptions, error) {
	page, err := pageOptions(cmd)
	if err != nil {
		return public.LogOptions{}, err
	}
	opts := public.LogOptions{
		PageOptions: page,
		Levels:      cmd.StringSlice("log-level"),
		SortOrder:   cmd.String("sort-order"),
	}
	if err := opts.Validate(); err != nil {
		return public.LogOptions{}, err
	}
	return opts, nil
}

// fetchSessionLogs reads a session's agent logs, every page up to opts.Limit
// records, and prints them, saying why when there are none.
func fetchSessionLogs(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.LogOptions, asJSON bool) error {
	page := &public.LogPage{}
	var err error
	page.Records, page.NextCursor, err = readPages(opts.PageOptions, func(o public.PageOptions) ([]oapi.LivekitPublicapiObservabilityV1LogRecord, string, error) {
		read := opts
		read.PageOptions = o
		p, err := client.GetSessionLogs(ctx, projectID, sessionID, read)
		if err != nil {
			return nil, "", err
		}
		return p.Records, p.NextCursor, nil
	})
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "agent logs", projectAdminAccess)
	}
	var empty string
	if len(page.Records) == 0 {
		empty = emptyLogsReason(sessionID, opts)
	}
	return render.SessionLogs(out, asJSON, *page, empty)
}

// emptyLogsReason explains a page with no records. With user data recording
// off an unfiltered first page is an error instead, so an empty one here means
// the agents exported nothing, or nothing at the levels asked for.
func emptyLogsReason(sessionID string, opts public.LogOptions) string {
	if opts.Cursor != "" {
		return "No more log records"
	}
	if len(opts.Levels) > 0 {
		return fmt.Sprintf("Session %s has no agent log records at the levels asked for (%s)",
			sessionID, strings.ToLower(strings.Join(opts.Levels, ", ")))
	}
	return fmt.Sprintf("Session %s has no agent logs: a session without an agent has none, "+
		"and a running agent's records appear as it exports them", sessionID)
}

// fetchSessionTraces reads a session's spans, every page up to opts.Limit,
// and prints them as one tree, saying why when there are none. A read that
// stops at the limit keeps the cursor for the rest.
func fetchSessionTraces(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.PageOptions, asJSON bool) error {
	read := public.TracePage{}
	var err error
	read.Spans, read.NextCursor, err = readPages(opts, func(o public.PageOptions) ([]oapi.LivekitPublicapiObservabilityV1Span, string, error) {
		p, err := client.GetSessionTraces(ctx, projectID, sessionID, o)
		if err != nil {
			return nil, "", err
		}
		return p.Spans, p.NextCursor, nil
	})
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "trace spans", projectAdminAccess)
	}
	var empty string
	if len(read.Spans) == 0 {
		empty = emptyTracesReason(sessionID, opts.Cursor != "")
	}
	return render.SessionTraces(out, asJSON, read, empty)
}

// emptyTracesReason explains a read with no spans. With user data recording
// off a first page with none is an error instead, so an empty one here means
// the agents exported nothing.
func emptyTracesReason(sessionID string, laterPage bool) string {
	if laterPage {
		return "No more spans"
	}
	return fmt.Sprintf("Session %s has no trace spans: a session without an agent has none, "+
		"and a running agent's spans appear as it exports them", sessionID)
}

// metricOptions reads the agent metrics flags. A bad limit or a blank name
// fails here, before the project lookup.
func metricOptions(cmd *cli.Command) (public.MetricOptions, error) {
	page, err := pageOptions(cmd)
	if err != nil {
		return public.MetricOptions{}, err
	}
	opts := public.MetricOptions{
		PageOptions: page,
		Names:       cmd.StringSlice("name"),
	}
	if err := opts.Validate(); err != nil {
		return public.MetricOptions{}, err
	}
	return opts, nil
}

// fetchSessionMetrics reads a session's agent metrics, every page up to
// opts.Limit points, and prints them, saying why when there are none.
func fetchSessionMetrics(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.MetricOptions, asJSON bool) error {
	page := &public.MetricPage{}
	var err error
	page.Points, page.NextCursor, err = readPages(opts.PageOptions, func(o public.PageOptions) ([]public.MetricPoint, string, error) {
		read := opts
		read.PageOptions = o
		p, err := client.GetSessionMetrics(ctx, projectID, sessionID, read)
		if err != nil {
			return nil, "", err
		}
		return p.Points, p.NextCursor, nil
	})
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "agent metrics", projectAdminAccess)
	}
	var empty string
	if len(page.Points) == 0 {
		empty = emptyMetricsReason(sessionID, opts)
	}
	return render.SessionMetrics(out, asJSON, *page, empty)
}

// emptyMetricsReason explains a page with no points. With user data recording
// off an unfiltered first page is an error instead, so an empty one here means
// the agents exported none, or none of the metrics asked for.
func emptyMetricsReason(sessionID string, opts public.MetricOptions) string {
	if opts.Cursor != "" {
		return "No more metric points"
	}
	if len(opts.Names) > 0 {
		return fmt.Sprintf("Session %s has no points for the metrics asked for (%s)",
			sessionID, strings.Join(opts.Names, ", "))
	}
	return fmt.Sprintf("Session %s has no agent metrics: only agents that export OpenTelemetry metrics "+
		"to LiveKit Cloud produce them, and a running agent's points appear as it exports them", sessionID)
}

// eventOptions reads the session events flags. A bad limit, an unknown type
// or sort order, or a participant session id that isn't one fails here,
// before the project lookup.
func eventOptions(cmd *cli.Command) (public.EventOptions, error) {
	page, err := pageOptions(cmd)
	if err != nil {
		return public.EventOptions{}, err
	}
	opts := public.EventOptions{
		PageOptions:          page,
		Types:                cmd.StringSlice("type"),
		ParticipantSessionID: cmd.String("participant"),
		SortOrder:            cmd.String("sort-order"),
	}
	if err := opts.Validate(); err != nil {
		return public.EventOptions{}, err
	}
	return opts, nil
}

// fetchSessionEvents reads a session's events, every page up to opts.Limit,
// and prints them, saying why when there are none.
func fetchSessionEvents(ctx context.Context, client *public.Client, projectID, sessionID string, opts public.EventOptions, asJSON bool) error {
	page := &public.EventPage{}
	var err error
	page.Events, page.NextCursor, err = readPages(opts.PageOptions, func(o public.PageOptions) ([]oapi.LivekitPublicapiAnalyticsV1SessionEvent, string, error) {
		read := opts
		read.PageOptions = o
		p, err := client.ListSessionEvents(ctx, projectID, sessionID, read)
		if err != nil {
			return nil, "", err
		}
		return p.Events, p.NextCursor, nil
	})
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "events", projectReadAccess)
	}
	var empty string
	if len(page.Events) == 0 {
		empty = emptyEventsReason(sessionID, opts)
	}
	return render.SessionEvents(out, asJSON, *page, empty)
}

// emptyEventsReason explains a page with no events: none of the types or for
// the participant session asked for, or none kept at all.
func emptyEventsReason(sessionID string, opts public.EventOptions) string {
	if opts.Cursor != "" {
		return "No more events"
	}
	var filters []string
	if len(opts.Types) > 0 {
		filters = append(filters, fmt.Sprintf("of the types asked for (%s)", strings.ToLower(strings.Join(opts.Types, ", "))))
	}
	if opts.ParticipantSessionID != "" {
		filters = append(filters, "for participant session "+opts.ParticipantSessionID)
	}
	if len(filters) > 0 {
		return fmt.Sprintf("Session %s has no events %s", sessionID, strings.Join(filters, " "))
	}
	return fmt.Sprintf("Session %s has no events: they are kept for 60 days, "+
		"and an active session's appear as they are recorded", sessionID)
}
