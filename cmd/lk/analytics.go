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
							Usage:     "Download a session's audio or chat history (requires --experimental-auth)",
							UsageText: "lk analytics session recording SESSION_ID --type audio|chat-history [-o FILE] [--url-only]",
							ArgsUsage: "SESSION_ID",
							Action:    sessionRead(sessionRecordingOptionsFrom, fetchSessionRecording),
							Flags:     append([]cli.Flag{jsonFlag}, analyticsRecordingFlags()...),
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
			Usage: "Maximum number of " + items + " to read (the server caps it at 100)",
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
// as analyticsSessionListFlags. --type isn't Required at the flag level so the
// --experimental-auth gate speaks first.
func analyticsRecordingFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "type",
			Usage: "Recording `TYPE` to fetch: audio or chat-history (required)",
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Save to `FILE`, replacing it if it exists (default SESSION_ID-audio.ogg or SESSION_ID-chat-history.json, never replaced)",
		},
		&cli.BoolFlag{
			Name:  "url-only",
			Usage: "Print the signed download URL (valid for 15 minutes) instead of downloading",
		},
	}
}

// analyticsListModeFlags: --page (offset) exists only on the API-key analytics
// endpoint, and the Public API session list is cursor-paginated. --start/--end
// work in both modes. The filter flags are sent only to the Public API for now.
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

	startDate := cmd.String("start")
	endDate := cmd.String("end")
	start, end, err := validateAnalyticsDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	if !start.IsZero() {
		query.Set("start", startDate)
	}
	if !end.IsZero() {
		query.Set("end", endDate)
	}

	return query, nil
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
	return render.SessionsPage(out, cmd.Bool("json"), sessions, nextCursor)
}

// sessionListOptions reads the session list flags for the Public API. --start
// and --end mean the same as on the API-key endpoint: UTC dates bounding when a
// session started, end exclusive. Unknown status and sort order names fail here,
// before the project lookup, like the date and limit checks.
func sessionListOptions(cmd *cli.Command) (public.SessionListOptions, error) {
	limit := cmd.Int("limit")
	if limit <= 0 {
		return public.SessionListOptions{}, errors.New("limit must be greater than 0")
	}
	start, end, err := validateAnalyticsDateRange(cmd.String("start"), cmd.String("end"))
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
// session — its participants or recordings — which only the Public API
// serves. The action refuses to run without --experimental-auth before
// checking anything else, then reads the SESSION_ID argument and the
// command's options, so a bad flag fails before the project lookup, and hands
// fetch a client signed in as the user and the selected project.
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
	// Output is the file to save to; empty picks defaultRecordingFile.
	Output string
	// URLOnly prints the signed URL instead of downloading.
	URLOnly bool
}

// sessionRecordingOptionsFrom reads the recording flags. A missing or unknown
// type fails here, before the project lookup.
func sessionRecordingOptionsFrom(cmd *cli.Command) (sessionRecordingOptions, error) {
	opts := sessionRecordingOptions{
		RecordingURLOptions: public.RecordingURLOptions{Recording: strings.ToLower(strings.TrimSpace(cmd.String("type")))},
		Output:              cmd.String("output"),
		URLOnly:             cmd.Bool("url-only"),
	}
	if err := opts.Validate(); err != nil {
		return sessionRecordingOptions{}, err
	}
	if opts.URLOnly && opts.Output != "" {
		return sessionRecordingOptions{}, errors.New("--output can't be used with --url-only")
	}
	return opts, nil
}

// fetchSessionRecording asks the Public API to sign a URL for the recording,
// then prints the URL or downloads it to a file. The download goes straight to
// the object store; nothing passes through the API.
func fetchSessionRecording(ctx context.Context, client *public.Client, projectID, sessionID string, opts sessionRecordingOptions, asJSON bool) error {
	// A file -o names is replaced; the default name never replaces a file, so
	// running the command twice can't silently overwrite the first download.
	file := opts.Output
	if file == "" && !opts.URLOnly {
		file = defaultRecordingFile(sessionID, opts.Recording)
		if _, err := os.Lstat(file); err == nil {
			return fmt.Errorf("%s already exists; pass -o %s to replace it, or -o FILE to save elsewhere", file, file)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	res, err := client.GetSessionRecordingURL(ctx, projectID, sessionID, opts.RecordingURLOptions)
	if err != nil {
		return sessionRecordingError(err, projectID, sessionID, opts.Recording)
	}
	if opts.URLOnly {
		return render.RecordingURL(out, asJSON, *res)
	}

	n, err := saveRecording(ctx, *res.Url, file)
	if err != nil {
		return err
	}
	return render.RecordingSaved(out, asJSON, render.SavedRecording{
		SessionID:          sessionID,
		Recording:          opts.Recording,
		File:               file,
		Bytes:              n,
		RecordingStartedAt: res.RecordingStartedAt,
	})
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

// saveRecording downloads a signed recording URL to path. It writes a hidden
// temporary file beside path (readable only by the user, like the recording
// it holds) and renames it into place once the download completes, so a
// failed download leaves no partial file and an existing file stays intact.
func saveRecording(ctx context.Context, signedURL, path string) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		return 0, err
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
		return 0, err
	}
	return n, nil
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
// that isn't positive fails here, before the project lookup.
func pageOptions(cmd *cli.Command) (public.PageOptions, error) {
	limit := cmd.Int("limit")
	if limit <= 0 {
		return public.PageOptions{}, errors.New("limit must be greater than 0")
	}
	return public.PageOptions{Limit: int32(limit), Cursor: cmd.String("cursor")}, nil
}

// sessionReadError annotates a session read's error like sessionAPIError,
// except NotFound: the API answers an unknown session and a mistyped
// --project the same way, so it names the session and the project it asked.
// what names what the read returns, and access is what the read requires.
func sessionReadError(err error, projectID, sessionID, what string, access sessionReadAccess) error {
	if public.IsNotFound(err) {
		return fmt.Errorf("no session %s in project %s (%w)", sessionID, projectID, err)
	}
	return sessionAPIError(err, what, access)
}

// sessionReadAccess is the project access a session read requires.
type sessionReadAccess int

const (
	// projectReadAccess reads: a session's participants.
	projectReadAccess sessionReadAccess = iota
	// projectAdminAccess reads, which can hold user data: a session's
	// recordings.
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
