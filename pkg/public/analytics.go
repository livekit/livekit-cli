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

package public

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// SessionListOptions narrows and orders one page of ListProjectSessions. Zero
// values keep the server's defaults: any status, newest first, and the window
// described on Start and End.
type SessionListOptions struct {
	// Limit caps the page size; 0 lets the server choose. The server caps pages
	// at 100.
	Limit int32
	// Cursor requests a specific page; empty starts from the beginning.
	Cursor string
	// Start and End bound when a session started, as the half-open window
	// [Start, End). The server fills in a zero side: with neither set it lists
	// the last 24 hours, with only Start it lists up to now, and with only End
	// it lists the 24 hours before End.
	Start, End time.Time
	// Statuses keeps sessions in any of these states: "active" or "closed".
	Statuses []string
	// RoomPrefix keeps sessions whose room name starts with it (case-sensitive).
	RoomPrefix string
	// Tags keeps sessions carrying any of these tags.
	Tags []string
	// SortOrder orders by start time: "asc" or "desc" (the server's default).
	SortOrder string
}

// parseSessionStatus maps a friendly status name to the wire enum.
func parseSessionStatus(s string) (oapi.LivekitPublicapiAnalyticsV1SessionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return oapi.SESSIONSTATUSACTIVE, nil
	case "closed":
		return oapi.SESSIONSTATUSCLOSED, nil
	default:
		return "", fmt.Errorf("invalid session status %q (expected \"active\" or \"closed\")", s)
	}
}

// parseSortOrder maps a friendly sort order name to the wire enum.
func parseSortOrder(s string) (oapi.LivekitPublicapiCommonV1SortOrder, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "asc":
		return oapi.SORTORDERASC, nil
	case "desc":
		return oapi.SORTORDERDESC, nil
	default:
		return "", fmt.Errorf("invalid sort order %q (expected \"asc\" or \"desc\")", s)
	}
}

// listProjectSessionsParams translates opts into the generated query params,
// rejecting unknown status or sort order names before any request is sent.
func listProjectSessionsParams(opts SessionListOptions) (*oapi.AnalyticsServiceListProjectSessionsParams, error) {
	params := &oapi.AnalyticsServiceListProjectSessionsParams{}
	if opts.Limit > 0 {
		params.PagePageSize = ptr(opts.Limit)
	}
	if opts.Cursor != "" {
		params.PageCursor = ptr(opts.Cursor)
	}
	if !opts.Start.IsZero() {
		params.FilterRangeStartTime = ptr(opts.Start)
	}
	if !opts.End.IsZero() {
		params.FilterRangeEndTime = ptr(opts.End)
	}
	if len(opts.Statuses) > 0 {
		statuses := make([]oapi.LivekitPublicapiAnalyticsV1SessionStatus, 0, len(opts.Statuses))
		for _, name := range opts.Statuses {
			status, err := parseSessionStatus(name)
			if err != nil {
				return nil, err
			}
			statuses = append(statuses, status)
		}
		params.FilterStatuses = &statuses
	}
	if opts.RoomPrefix != "" {
		params.FilterRoomName = ptr(opts.RoomPrefix)
	}
	if len(opts.Tags) > 0 {
		params.FilterTags = ptr(opts.Tags)
	}
	if opts.SortOrder != "" {
		order, err := parseSortOrder(opts.SortOrder)
		if err != nil {
			return nil, err
		}
		params.SortOrder = &order
	}
	return params, nil
}

// Validate reports an unknown status or sort order name. ListProjectSessions
// makes the same check before sending a request; callers can run it earlier.
func (o SessionListOptions) Validate() error {
	_, err := listProjectSessionsParams(o)
	return err
}

// ListProjectSessions returns one page of a project's analytics sessions,
// narrowed and ordered by opts. The operation is cursor-paginated; the returned
// nextCursor is non-empty when more pages remain (pass it back as opts.Cursor to
// fetch the next page).
func (c *Client) ListProjectSessions(ctx context.Context, projectID string, opts SessionListOptions) (sessions []oapi.LivekitPublicapiAnalyticsV1Session, nextCursor string, err error) {
	params, err := listProjectSessionsParams(opts)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.gen.AnalyticsServiceListProjectSessionsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}

// PageOptions pages a read of one of a session's lists: its participants,
// transcript or agent logs. Zero values start from the first item with the
// server's page size.
type PageOptions struct {
	// Limit caps how many items a page holds; 0 lets the server choose (50).
	// The server caps pages at 100, and a page can hold fewer items than its
	// limit.
	Limit int32
	// Cursor requests a specific page, the NextCursor of the page before it;
	// empty starts from the beginning.
	Cursor string
}

// Validate reports a negative limit. Every read that takes PageOptions makes
// the same check before sending a request; callers can run it earlier.
func (o PageOptions) Validate() error {
	if o.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

// params returns the page size and cursor query params, each nil when it
// leaves the server's default.
func (o PageOptions) params() (pageSize *int32, cursor *string) {
	if o.Limit > 0 {
		pageSize = ptr(o.Limit)
	}
	if o.Cursor != "" {
		cursor = ptr(o.Cursor)
	}
	return pageSize, cursor
}

// ParticipantListOptions orders and pages one page of ListSessionParticipants.
// Zero values keep the server's defaults: newest join first, with a page size
// the server picks. To continue from a session detail's first page, pass its
// participants page cursor with SortBy and SortOrder unset.
type ParticipantListOptions struct {
	PageOptions
	// SortBy orders by "joined" (the server's default) or "left".
	SortBy string
	// SortOrder is "asc" or "desc" (the server's default).
	SortOrder string
}

// parseParticipantSort maps a friendly participant sort name to the wire enum.
func parseParticipantSort(s string) (oapi.LivekitPublicapiAnalyticsV1ParticipantSortField, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "joined":
		return oapi.PARTICIPANTSORTFIELDJOINEDAT, nil
	case "left":
		return oapi.PARTICIPANTSORTFIELDLEFTAT, nil
	default:
		return "", fmt.Errorf("invalid participant sort %q (expected \"joined\" or \"left\")", s)
	}
}

// listSessionParticipantsParams translates opts into the generated query
// params, rejecting a negative limit and unknown sort names before any request
// is sent.
func listSessionParticipantsParams(opts ParticipantListOptions) (*oapi.AnalyticsServiceListSessionParticipantsParams, error) {
	if err := opts.PageOptions.Validate(); err != nil {
		return nil, err
	}
	params := &oapi.AnalyticsServiceListSessionParticipantsParams{}
	params.PagePageSize, params.PageCursor = opts.params()
	if opts.SortBy != "" {
		field, err := parseParticipantSort(opts.SortBy)
		if err != nil {
			return nil, err
		}
		params.SortBy = &field
	}
	if opts.SortOrder != "" {
		order, err := parseSortOrder(opts.SortOrder)
		if err != nil {
			return nil, err
		}
		params.SortOrder = &order
	}
	return params, nil
}

// Validate reports a negative limit or an unknown sort name.
// ListSessionParticipants makes the same checks before sending a request;
// callers can run them earlier.
func (o ParticipantListOptions) Validate() error {
	_, err := listSessionParticipantsParams(o)
	return err
}

// ListSessionParticipants returns one page of a session's participants, one row
// per identity, ordered by opts. The returned nextCursor is non-empty when more
// pages remain (pass it back as opts.Cursor to fetch the next page).
func (c *Client) ListSessionParticipants(ctx context.Context, projectID, sessionID string, opts ParticipantListOptions) (participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo, nextCursor string, err error) {
	params, err := listSessionParticipantsParams(opts)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.gen.AnalyticsServiceListSessionParticipantsWithResponse(ctx, projectID, sessionID, params)
	if err != nil {
		return nil, "", err
	}
	if resp.JSON200 == nil {
		return nil, "", responseError(resp.StatusCode(), resp.Body)
	}
	return items(resp.JSON200.Items), pageCursor(resp.JSON200.PageInfo), nil
}

// GetSession returns a single analytics session by id: its list row and its
// detail (totals, timelines, and the first page of participants). detail is nil
// while the server is still finalizing it; the session row is always set.
func (c *Client) GetSession(ctx context.Context, projectID, sessionID string) (session *oapi.LivekitPublicapiAnalyticsV1Session, detail *oapi.LivekitPublicapiAnalyticsV1SessionDetail, err error) {
	resp, err := c.gen.AnalyticsServiceGetSessionWithResponse(ctx, projectID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil, responseError(resp.StatusCode(), resp.Body)
	}
	session, err = requirePayload(resp.JSON200.Session, "session")
	if err != nil {
		return nil, nil, err
	}
	return session, resp.JSON200.Detail, nil
}
