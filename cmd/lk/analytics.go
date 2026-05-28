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
	"strconv"
	"strings"
	"time"

	authutil "github.com/livekit/livekit-cli/v2/pkg/auth"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	"github.com/urfave/cli/v3"
)

const (
	analyticsProjectIDRequirement = "analytics API requires a LiveKit Cloud project with a known project_id"
	analyticsProjectSelectHint    = "Select a cloud project via --project or run `lk cloud auth`"
)

var (
	AnalyticsCommands = []*cli.Command{
		{
			Name:  "analytics",
			Usage: "List and inspect LiveKit Cloud analytics sessions",
			Commands: []*cli.Command{
				{
					Name:   "list",
					Usage:  "List analytics sessions",
					Action: listAnalyticsSessions,
					Flags: []cli.Flag{
						jsonFlag,
						&cli.IntFlag{
							Name:  "limit",
							Usage: "Maximum number of sessions to return",
						},
						&cli.IntFlag{
							Name:  "page",
							Usage: "Page number (starts at 0)",
						},
						&cli.StringFlag{
							Name:  "start",
							Usage: "Start date in `YYYY-MM-DD` format",
						},
						&cli.StringFlag{
							Name:  "end",
							Usage: "End date in `YYYY-MM-DD` format",
						},
					},
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

func listAnalyticsSessions(ctx context.Context, cmd *cli.Command) error {
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
		fmt.Println("No sessions found")
		return nil
	}

	table := util.CreateTable().
		Headers("Session ID", "Room", "Created", "Ended", "Participants", "Active", "Bandwidth In", "Bandwidth Out")

	for _, session := range res.Sessions {
		if session == nil {
			continue
		}
		table.Row(
			emptyDash(session.SessionID),
			emptyDash(session.RoomName),
			emptyDash(session.CreatedAt),
			emptyDash(session.EndedAt),
			strconv.Itoa(session.NumParticipants),
			strconv.Itoa(session.NumActiveParticipants),
			rawJSONToString(session.BandwidthIn),
			rawJSONToString(session.BandwidthOut),
		)
	}

	fmt.Println(table)
	return nil
}

func getAnalyticsSession(ctx context.Context, cmd *cli.Command) error {
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
			emptyDash(details.RoomID),
			emptyDash(details.RoomName),
			emptyDash(details.StartTime),
			emptyDash(details.EndTime),
			strconv.Itoa(details.NumParticipants),
			rawJSONToString(details.ConnectionMinutes),
			rawJSONToString(details.Bandwidth),
		)
	fmt.Println(summary)

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
			emptyDash(participant.ParticipantIdentity),
			emptyDash(participant.ParticipantName),
			emptyDash(participant.JoinedAt),
			emptyDash(participant.LeftAt),
			emptyDash(participant.Region),
			emptyDash(participant.ConnectionType),
			emptyDash(participant.SDKVersion),
		)
	}

	fmt.Println(participantTable)
	return nil
}

func buildAnalyticsListQuery(cmd *cli.Command) (url.Values, error) {
	query := url.Values{}

	if cmd.IsSet("limit") {
		limit := cmd.Int("limit")
		if limit <= 0 {
			return nil, errors.New("limit must be greater than 0")
		}
		query.Set("limit", strconv.Itoa(limit))
	}

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

	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return time.Time{}, time.Time{}, errors.New("start date must be less than or equal to end date")
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
		fmt.Printf("curl -H \"Authorization: Bearer %s\" \"%s\"\n", token, reqURL.String())
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

func rawJSONToString(value json.RawMessage) string {
	if len(value) == 0 {
		return "-"
	}

	var numeric json.Number
	if err := json.Unmarshal(value, &numeric); err == nil {
		return numeric.String()
	}

	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return emptyDash(text)
	}

	return emptyDash(string(value))
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
