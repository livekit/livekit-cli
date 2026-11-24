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
	"net/http"
	"strings"
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// TimeRange bounds a read of a project's data over time. A zero Start or End
// is unset: an unset End is now, and an unset Start is the read's default
// length before the end. Each read says whether it includes End.
type TimeRange struct {
	Start, End time.Time
}

// params returns the range's query params, each nil when it is unset.
func (r TimeRange) params() (start, end *time.Time) {
	if !r.Start.IsZero() {
		start = ptr(r.Start)
	}
	if !r.End.IsZero() {
		end = ptr(r.End)
	}
	return start, end
}

// Log source names accepted by ProjectLogOptions.Sources and
// LogPatternOptions.Sources.
const (
	LogSourceAgent    = "agent"
	LogSourceClient   = "client"
	LogSourcePlatform = "platform"
)

// parseLogSource maps a friendly log source name to the wire enum.
func parseLogSource(s string) (oapi.LivekitPublicapiObservabilityV1LogSource, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case LogSourceAgent:
		return oapi.LOGSOURCEAGENT, nil
	case LogSourceClient:
		return oapi.LOGSOURCECLIENT, nil
	case LogSourcePlatform:
		return oapi.LOGSOURCEPLATFORM, nil
	default:
		return "", fmt.Errorf("invalid log source %q (expected agent, client or platform)", s)
	}
}

// invalidLogQueryMessage is the Public API's message for a log search query
// it can't parse.
const invalidLogQueryMessage = "query isn't valid log search syntax"

// InvalidLogQuery reports whether err is the Public API's refusal of a log
// search query it can't parse (HTTP 400), which says nothing about what is
// wrong with it.
func InvalidLogQuery(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && apiErr.Message == invalidLogQueryMessage
}

// ProjectLogOptions filters, orders and pages ListProjectLogs. Zero values
// return every record of the last 24 hours, newest first, with the server's
// page size. A cursor pages only a request with the same filters and
// SortOrder, and reads the Range of the first page.
type ProjectLogOptions struct {
	PageOptions
	// Range bounds the record time. The read includes End, to the
	// millisecond, and returns at most the 7 days before it.
	Range TimeRange
	// Levels keeps records at any of these levels, as LogOptions.Levels.
	Levels []string
	// Sources keeps records from any of these sources: "agent", "client" or
	// "platform" (LiveKit's own records about the project, such as API
	// calls). Empty returns every source.
	Sources []string
	// SessionID keeps one session's records (RM_...).
	SessionID string
	// Query is a search in the syntax of the dashboard's logs search box.
	Query string
	// SortOrder orders by time: "asc" or "desc" (the server's default).
	SortOrder string
}

// listProjectLogsParams translates opts into the generated query params,
// rejecting a negative limit and unknown level, source or sort order names
// before any request is sent.
func listProjectLogsParams(opts ProjectLogOptions) (*oapi.ObservabilityServiceListProjectLogsParams, error) {
	if err := opts.PageOptions.Validate(); err != nil {
		return nil, err
	}
	params := &oapi.ObservabilityServiceListProjectLogsParams{}
	params.PagePageSize, params.PageCursor = opts.params()
	params.RangeStartTime, params.RangeEndTime = opts.Range.params()
	if len(opts.Levels) > 0 {
		levels := make([]oapi.LivekitPublicapiObservabilityV1LogLevel, 0, len(opts.Levels))
		for _, name := range opts.Levels {
			level, err := parseLogLevel(name)
			if err != nil {
				return nil, err
			}
			levels = append(levels, level)
		}
		params.LogLevels = &levels
	}
	if len(opts.Sources) > 0 {
		sources := make([]oapi.LivekitPublicapiObservabilityV1LogSource, 0, len(opts.Sources))
		for _, name := range opts.Sources {
			source, err := parseLogSource(name)
			if err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
		params.Sources = &sources
	}
	if opts.SessionID != "" {
		params.SessionId = ptr(opts.SessionID)
	}
	if opts.Query != "" {
		params.Query = ptr(opts.Query)
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

// Validate reports a negative limit or an unknown level, source or sort order
// name. ListProjectLogs makes the same checks before sending a request;
// callers can run them earlier.
func (o ProjectLogOptions) Validate() error {
	_, err := listProjectLogsParams(o)
	return err
}

// ProjectLogPage is one page of a project's log records.
type ProjectLogPage struct {
	// Records are in the requested time order, newest first by default.
	Records []oapi.LivekitPublicapiObservabilityV1LogRecord
	// NextCursor is non-empty when more pages remain (pass it back as
	// ProjectLogOptions.Cursor).
	NextCursor string
}

// ListProjectLogs returns one page of a project's log records from its
// agents, its client SDKs and LiveKit: what the dashboard's logs page lists.
// It is for project admins only (see IsPermissionDenied). Records are kept for
// 14 days; a range that ends before then, or starts after now, is a 400. A
// query the server can't parse is a 400 that InvalidLogQuery recognizes. An
// empty unfiltered first page while the project's user data recording is off
// is a FailedPrecondition that ObservabilityDisabled recognizes.
func (c *Client) ListProjectLogs(ctx context.Context, projectID string, opts ProjectLogOptions) (*ProjectLogPage, error) {
	params, err := listProjectLogsParams(opts)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.ObservabilityServiceListProjectLogsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return &ProjectLogPage{Records: items(resp.JSON200.Items), NextCursor: pageCursor(resp.JSON200.PageInfo)}, nil
}

// Log pattern rank names accepted by LogPatternOptions.RankBy.
const (
	LogPatternRankEvents    = "events"
	LogPatternRankFirstSeen = "first-seen"
	LogPatternRankLastSeen  = "last-seen"
)

// parseLogPatternRank maps a friendly log pattern rank name to the wire enum.
func parseLogPatternRank(s string) (oapi.LivekitPublicapiObservabilityV1LogPatternRank, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case LogPatternRankEvents:
		return oapi.LOGPATTERNRANKEVENTS, nil
	case LogPatternRankFirstSeen:
		return oapi.LOGPATTERNRANKFIRSTSEEN, nil
	case LogPatternRankLastSeen:
		return oapi.LOGPATTERNRANKLASTSEEN, nil
	default:
		return "", fmt.Errorf("invalid rank %q (expected events, first-seen or last-seen)", s)
	}
}

// LogPatternOptions filters and ranks ListLogPatterns. Zero values return the
// server's 50 patterns with the most records among the agents' records at
// WARN or higher of the last 24 hours.
type LogPatternOptions struct {
	// Range bounds the record time. The read excludes End, and covers at most
	// the 7 days before it.
	Range TimeRange
	// Limit caps how many patterns the list holds; 0 sends none, so the server
	// uses its default of 50. The server caps it at 200. The list is one page.
	Limit int32
	// Query is a search in the syntax of the dashboard's logs search box.
	Query string
	// MinLevel keeps patterns at this level or higher, as a LogOptions.Levels
	// name. Empty is the server's default, "warn"; "trace" keeps every level,
	// records with no level included.
	MinLevel string
	// Sources keeps patterns from any of these sources: "agent" or "client".
	// Empty is the server's default, the agents'.
	Sources []string
	// AgentName, AgentID (CA_...) and SessionID (RM_...) keep the patterns of
	// one agent name, cloud agent or session.
	AgentName string
	AgentID   string
	SessionID string
	// RankBy chooses which patterns the list holds, best first: "events" (the
	// server's default), "first-seen" or "last-seen".
	RankBy string
}

// listLogPatternsParams translates opts into the generated query params,
// rejecting a negative limit and unknown level, source or rank names before
// any request is sent.
func listLogPatternsParams(opts LogPatternOptions) (*oapi.ObservabilityServiceListLogPatternsParams, error) {
	if opts.Limit < 0 {
		return nil, errors.New("limit must not be negative")
	}
	params := &oapi.ObservabilityServiceListLogPatternsParams{}
	if opts.Limit > 0 {
		params.PagePageSize = ptr(opts.Limit)
	}
	params.RangeStartTime, params.RangeEndTime = opts.Range.params()
	if opts.Query != "" {
		params.Query = ptr(opts.Query)
	}
	if opts.MinLevel != "" {
		level, err := parseLogLevel(opts.MinLevel)
		if err != nil {
			return nil, err
		}
		params.MinLevel = &level
	}
	if len(opts.Sources) > 0 {
		sources := make([]oapi.LivekitPublicapiObservabilityV1LogSource, 0, len(opts.Sources))
		for _, name := range opts.Sources {
			source, err := parseLogSource(name)
			if err == nil && source == oapi.LOGSOURCEPLATFORM {
				err = fmt.Errorf("invalid log source %q: only agent and client logs have patterns", name)
			}
			if err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
		params.Sources = &sources
	}
	if opts.AgentName != "" {
		params.AgentName = ptr(opts.AgentName)
	}
	if opts.AgentID != "" {
		params.AgentId = ptr(opts.AgentID)
	}
	if opts.SessionID != "" {
		params.SessionId = ptr(opts.SessionID)
	}
	if opts.RankBy != "" {
		rank, err := parseLogPatternRank(opts.RankBy)
		if err != nil {
			return nil, err
		}
		params.RankBy = &rank
	}
	return params, nil
}

// Validate reports a negative limit or an unknown level, source or rank name.
// ListLogPatterns makes the same checks before sending a request; callers can
// run them earlier.
func (o LogPatternOptions) Validate() error {
	_, err := listLogPatternsParams(o)
	return err
}

// ListLogPatterns returns a project's log patterns, ranked: groups of log
// records that log the same message from one place in the code, at one level,
// with their counts over time. They are the patterns that the logs page on
// the dashboard groups the records into. On a project with many records the
// counts can be estimates, as the response's Estimated and SampleShare say.
// It is for project admins only, and fails as ListProjectLogs does.
func (c *Client) ListLogPatterns(ctx context.Context, projectID string, opts LogPatternOptions) (*oapi.LivekitPublicapiObservabilityV1LogPatternsListResponse, error) {
	params, err := listLogPatternsParams(opts)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.ObservabilityServiceListLogPatternsWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, responseError(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200, nil
}
