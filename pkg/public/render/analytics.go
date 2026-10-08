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
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	sessionTotalsHeaders   = []string{"Bandwidth In", "Bandwidth Out", "Connection Time"}
	sessionTimelineHeaders = []string{"Metric", "Points", "Average", "Peak"}
	participantHeaders     = []string{"Identity", "Name", "Joined", "Left", "Location", "Region", "Published"}
)

func participantRow(p oapi.LivekitPublicapiAnalyticsV1ParticipantInfo) []string {
	return []string{
		dashText(p.ParticipantIdentity), dashText(p.ParticipantName),
		util.FormatTime(p.JoinedAt), util.FormatTime(p.LeftAt),
		util.DashString(p.Location), util.DashString(p.Region), publishedSources(p.PublishedSources),
	}
}

// stripControls removes the control characters a terminal acts on from
// untrusted text, such as what a participant, an agent, an LLM or a tool
// chose: C0 controls except tab and newline, DEL, and C1 controls. Without
// their ESC (or 8-bit CSI or OSC), escape sequences can't set the window
// title, clear the screen or restyle what follows; their printable rest stays
// visible. Invalid UTF-8 becomes U+FFFD, so no lone C1 byte gets through
// either. --json needs none of this: JSON escapes control characters itself.
func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' && r != '\n' {
			return -1
		}
		return r
	}, s)
}

// dashText renders optional untrusted text for display on one line, as
// oneLine does, or a dash when it is missing or empty.
func dashText(s *string) string {
	return util.Dash(oneLine(util.Deref(s)))
}

// publishedSources lists the track sources a participant published, or a dash
// when none.
func publishedSources(s *oapi.LivekitPublicapiAnalyticsV1PublishedSources) string {
	if s == nil {
		return "-"
	}
	var names []string
	for _, src := range []struct {
		on   *bool
		name string
	}{
		{s.CameraTrack, "camera"},
		{s.MicrophoneTrack, "microphone"},
		{s.ScreenShareTrack, "screen share"},
		{s.ScreenShareAudio, "screen share audio"},
	} {
		if util.Deref(src.on) {
			names = append(names, src.name)
		}
	}
	return util.Dash(strings.Join(names, ", "))
}

var participantSessionHeaders = []string{"Identity", "Participant Session", "Joined", "Left", "Duration", "Client", "Connection", "Location"}

func participantSessionRow(identity *string, s oapi.LivekitPublicapiAnalyticsV1ParticipantSession) []string {
	return []string{
		util.DashString(identity), util.DashString(s.ParticipantSessionId),
		util.FormatTime(s.JoinedAt), util.FormatTime(s.LeftAt), formatSeconds(s.DurationSeconds),
		participantClient(s), participantConnection(s), util.DashString(s.Location),
	}
}

// participantClient names the client a participant session connected from:
// its OS, browser, device model and SDK version, each only when reported.
func participantClient(s oapi.LivekitPublicapiAnalyticsV1ParticipantSession) string {
	var parts []string
	for _, v := range []string{util.Deref(s.Os), util.Deref(s.Browser), util.Deref(s.DeviceModel)} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if v := util.Deref(s.SdkVersion); v != "" {
		parts = append(parts, "SDK "+v)
	}
	return util.Dash(strings.Join(parts, ", "))
}

// participantConnection renders the transport a participant session connected
// over and how long it took to connect, e.g. "UDP (120ms)".
func participantConnection(s oapi.LivekitPublicapiAnalyticsV1ParticipantSession) string {
	conn := util.Deref(s.ConnectionType)
	if ms := util.Deref(s.ConnectionTimeMs); ms > 0 {
		took := strconv.Itoa(int(ms)) + "ms"
		if conn == "" {
			return took
		}
		return conn + " (" + took + ")"
	}
	return util.Dash(conn)
}

// renderParticipants prints a page of participants as a table, then their
// participant sessions, one row per connection, in a second table. A
// participant whose participant sessions couldn't be read has no rows in the
// second. Like the server, it counts a page as empty only with no
// participants and no next cursor, so it never says there are none beside a
// hint that there are more. It leaves that hint to the caller.
func renderParticipants(p *util.Printer, participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo, nextCursor string) error {
	if len(participants) == 0 {
		if nextCursor == "" {
			p.Status("No participants found")
		}
		return nil
	}
	if err := util.RenderList(p, false, participants, "", participantHeaders, participantRow); err != nil {
		return err
	}
	sessions := util.CreateTable().Headers(participantSessionHeaders...)
	rows := 0
	for _, pi := range participants {
		for _, s := range util.Deref(pi.Sessions) {
			sessions.Row(participantSessionRow(pi.ParticipantIdentity, s)...)
			rows++
		}
	}
	if rows > 0 {
		p.Result(sessions)
	}
	return nil
}

// SessionParticipantsPage prints a cursor-paginated page of a session's
// participants and their participant sessions. As JSON it emits {items,
// nextCursor} with the API's rows, participant sessions nested.
func SessionParticipantsPage(p *util.Printer, asJSON bool, participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo, nextCursor string) error {
	if asJSON {
		return util.RenderPage(p, true, participants, nextCursor, "No participants found", participantHeaders, participantRow)
	}
	if err := renderParticipants(p, participants, nextCursor); err != nil {
		return err
	}
	moreAvailable(p, "participants", nextCursor)
	return nil
}

// SessionDetail prints a session with its detail: the list row, the totals, a
// summary of each timeline, and the first page of participants. As JSON it
// emits the API's own {session, detail} response. A nil detail means the server
// is still finalizing it, so only the row prints.
func SessionDetail(p *util.Printer, asJSON bool, s oapi.LivekitPublicapiAnalyticsV1Session, d *oapi.LivekitPublicapiAnalyticsV1SessionDetail) error {
	if asJSON {
		return util.PrintJSONTo(p.ResultWriter(), oapi.LivekitPublicapiAnalyticsV1SessionsGetResponse{Session: &s, Detail: d})
	}
	if err := Session(p, false, s); err != nil {
		return err
	}
	if d == nil {
		p.Status("The session's detail isn't available yet: it is still being finalized.")
		return nil
	}

	p.Result(util.CreateTable().Headers(sessionTotalsHeaders...).Row(
		formatBytes(d.BandwidthIn), formatBytes(d.BandwidthOut), formatSeconds(d.ConnectionSeconds),
	))

	timelines := util.CreateTable().Headers(sessionTimelineHeaders...)
	for _, tl := range []struct {
		name   string
		points *[]oapi.LivekitPublicapiAnalyticsV1DataPoint
		format func(float64) string
	}{
		{"Quality", d.Quality, formatPercent},
		{"Publish bitrate", d.PublishBps, formatBitrate},
		{"Subscribe bitrate", d.SubscribeBps, formatBitrate},
		{"Publish frame rate", d.PublishFps, formatFPS},
		{"Subscribe frame rate", d.SubscribeFps, formatFPS},
	} {
		timelines.Row(timelineRow(tl.name, util.Deref(tl.points), tl.format)...)
	}
	p.Result(timelines)

	// The participant list is another command, so these hints print it in full,
	// like other lk hints, and leave the flags this command ran with
	// (--experimental-auth, --project) to the user, like a --cursor hint does.
	listCmd := "lk analytics session participant list " + util.Deref(s.SessionId)
	if d.ParticipantsPage == nil {
		p.Statusf("Participants couldn't be read — list them with %s, using the same flags as this command",
			util.Accented(listCmd))
		return nil
	}
	next := util.Deref(d.ParticipantsPage.NextCursor)
	if err := renderParticipants(p, util.Deref(d.Participants), next); err != nil {
		return err
	}
	if next != "" {
		p.Statusf("More participants available — list them with %s, using the same flags as this command",
			util.Accented(listCmd+" --cursor "+next))
	}
	return nil
}

// jsonPage is the --json shape of a page of a session's transcript, agent
// logs, trace spans or agent metrics: {items, nextCursor}, like
// util.RenderPage's, with each item as the API sent it.
type jsonPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	// SkippedRecords counts a transcript page's records the server couldn't
	// read as items; the other reads skip none.
	SkippedRecords int `json:"skippedRecords,omitempty"`
}

// renderPage prints a page of a session's items: one line each, as lines
// renders them, or with --json the page as it is. empty says why a page is
// empty, on stderr in both modes so --json output stays parseable. Like the
// server, it counts a page as empty only with no items, nothing skipped and
// no next cursor, so it never says there's nothing beside a hint that there's
// more. It leaves the hint for a next page to the caller.
func renderPage[T any](p *util.Printer, asJSON bool, page jsonPage[T], empty string, lines func([]T) []string) error {
	if page.Items == nil {
		page.Items = []T{}
	}
	if asJSON {
		if err := util.PrintJSONTo(p.ResultWriter(), page); err != nil {
			return err
		}
	} else {
		for _, line := range lines(page.Items) {
			p.Result(line)
		}
	}
	if len(page.Items) == 0 && page.SkippedRecords == 0 && page.NextCursor == "" && empty != "" {
		p.Status(empty)
	}
	return nil
}

// moreAvailable says a page has more after it and how to read them: re-run
// the same command, with the same flags, adding --cursor. what names the
// page's items.
func moreAvailable(p *util.Printer, what, nextCursor string) {
	if nextCursor != "" {
		p.Statusf("More %s available — re-run with %s", what, util.Accented("--cursor "+nextCursor))
	}
}

// timelineRow summarizes one timeline. The timelines share a grid with a 0 in
// every bucket that has no data, so the average is over non-zero points only;
// --json has every point.
func timelineRow(name string, points []oapi.LivekitPublicapiAnalyticsV1DataPoint, format func(float64) string) []string {
	var sum, peak float64
	var n int
	for _, pt := range points {
		v := util.Deref(pt.Value)
		if v == 0 {
			continue
		}
		sum += v
		n++
		peak = max(peak, v)
	}
	if n == 0 {
		return []string{name, strconv.Itoa(len(points)), "-", "-"}
	}
	return []string{name, strconv.Itoa(len(points)), format(sum / float64(n)), format(peak)}
}

// formatBytes renders a 64-bit byte count, which the API sends as a decimal
// string.
func formatBytes(v *string) string {
	if v == nil {
		return "-"
	}
	return util.FormatBytes(json.RawMessage(strconv.Quote(*v)))
}

// formatSeconds renders a 64-bit count of seconds as a duration, e.g. "1h2m3s".
func formatSeconds(v *string) string {
	if v == nil {
		return "-"
	}
	secs, err := strconv.ParseInt(*v, 10, 64)
	if err != nil {
		return util.Dash(*v)
	}
	return (time.Duration(secs) * time.Second).String()
}

// formatPercent renders a 0-1 fraction as a percentage.
func formatPercent(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

// formatFPS renders a frame rate.
func formatFPS(v float64) string { return fmt.Sprintf("%.1f fps", v) }

// formatBitrate renders bits per second with SI units.
func formatBitrate(bps float64) string {
	units := []string{"bps", "kbps", "Mbps", "Gbps"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", bps, units[i])
	}
	return fmt.Sprintf("%.1f %s", bps, units[i])
}

// SessionTranscript prints a page of a session's transcript, one line per
// item with its time, role or kind, and latencies; --json prints the items as
// the API sent them. empty says why a page has no items, on stderr in both
// modes so --json output stays parseable.
func SessionTranscript(p *util.Printer, asJSON bool, page public.TranscriptPage, empty string) error {
	jp := jsonPage[public.TranscriptItem]{Items: page.Items, NextCursor: page.NextCursor, SkippedRecords: page.SkippedRecords}
	if err := renderPage(p, asJSON, jp, empty, transcriptLines); err != nil || asJSON {
		return err
	}
	switch n := page.SkippedRecords; {
	case n == 1:
		p.Status("1 record couldn't be read as a transcript item and was left out")
	case n > 1:
		p.Statusf("%d records couldn't be read as transcript items and were left out", n)
	}
	moreAvailable(p, "items", page.NextCursor)
	return nil
}

// transcriptToolTextMax caps a tool call's arguments and a tool result's
// output on a line; --json has them whole.
const transcriptToolTextMax = 200

// transcriptLines renders transcript items one per line.
func transcriptLines(items []public.TranscriptItem) []string {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, transcriptLine(it))
	}
	return lines
}

// transcriptLine renders one transcript item as a single line.
func transcriptLine(it public.TranscriptItem) string {
	at := "-"
	if it.Timestamp != nil && !it.Timestamp.IsZero() {
		at = it.Timestamp.Local().Format("15:04:05")
	}
	kind, body := transcriptItemText(it)
	return fmt.Sprintf("%-8s  %-11s  %s", at, kind, body)
}

// transcriptItemText names an item's role or kind and says what it holds.
func transcriptItemText(it public.TranscriptItem) (kind, body string) {
	switch {
	case it.Message != nil:
		return messageText(*it.Message)
	case it.ToolCall != nil:
		c := it.ToolCall
		return "TOOL CALL", dashText(c.Name) + "(" + clip(oneLine(util.Deref(c.Arguments)), transcriptToolTextMax) + ")"
	case it.ToolResult != nil:
		r := it.ToolResult
		body = dashText(r.Name) + ": " + clip(oneLine(util.Deref(r.Output)), transcriptToolTextMax)
		if util.Deref(r.IsError) {
			body += "  [error]"
		}
		return "TOOL RESULT", body
	case it.AgentHandoff != nil:
		h := it.AgentHandoff
		from := oneLine(util.Deref(h.FromAgentId))
		if from != "" {
			from += " "
		}
		return "HANDOFF", from + "→ " + dashText(h.ToAgentId)
	case it.ConfigUpdate != nil:
		u := it.ConfigUpdate
		var parts []string
		if util.Deref(u.Instructions) != "" {
			parts = append(parts, "instructions changed")
		}
		if added := util.Deref(u.ToolsAdded); len(added) > 0 {
			parts = append(parts, "tools added: "+oneLine(strings.Join(added, ", ")))
		}
		if removed := util.Deref(u.ToolsRemoved); len(removed) > 0 {
			parts = append(parts, "tools removed: "+oneLine(strings.Join(removed, ", ")))
		}
		return "CONFIG", util.Dash(strings.Join(parts, " · "))
	default:
		return "UNKNOWN", util.Dash(oneLine(it.ID)) + " (a kind this lk doesn't know; see --json)"
	}
}

// messageText renders a message: its role, its text on one line, its flags,
// and the turn latencies the agent recorded (an agent message carries e2e,
// LLM and TTS; a user message transcription, end of turn and the
// on_user_turn_completed callback).
func messageText(m oapi.LivekitPublicapiObservabilityV1TranscriptItemMessage) (kind, body string) {
	kind = strings.TrimPrefix(util.DerefEnum(m.Role), "ROLE_")
	if kind == "UNSPECIFIED" || kind == "-" {
		kind = "MESSAGE"
	}
	body = util.Dash(oneLine(util.Deref(m.Text)))
	if util.Deref(m.Interrupted) {
		body += "  [interrupted]"
	}
	if util.Deref(m.Redacted) {
		body += "  [redacted]"
	}
	var metrics []string
	for _, l := range []struct {
		name string
		ms   *float64
	}{
		{"e2e", m.E2eLatencyMs},
		{"llm_ttft", m.LlmTtftMs},
		{"tts_ttfb", m.TtsTtfbMs},
		{"transcription", m.TranscriptionDelayMs},
		{"end_of_turn", m.EndOfTurnDelayMs},
		{"on_user_turn_completed", m.OnUserTurnCompletedDelayMs},
	} {
		if l.ms != nil {
			metrics = append(metrics, l.name+" "+formatMs(*l.ms))
		}
	}
	if m.TranscriptConfidence != nil {
		metrics = append(metrics, fmt.Sprintf("confidence %.2f", *m.TranscriptConfidence))
	}
	if len(metrics) > 0 {
		body += "  (" + strings.Join(metrics, " · ") + ")"
	}
	return kind, body
}

// formatMs renders milliseconds, keeping one decimal under 100ms where it
// still carries information.
func formatMs(ms float64) string {
	if ms >= 100 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fms", ms)
}

// oneLine joins text recorded across lines with spaces, its control
// characters stripped: what an agent, an LLM or a tool wrote is untrusted.
func oneLine(s string) string { return stripControls(strings.Join(strings.Fields(s), " ")) }

// clip shortens s to max runes, marking the cut with an ellipsis.
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// SessionLogs prints a page of a session's agent logs, one line per record
// with its time, level, logger and message; --json prints the records as the
// API sent them. empty says why a page has no records, on stderr in both modes
// so --json output stays parseable.
func SessionLogs(p *util.Printer, asJSON bool, page public.LogPage, empty string) error {
	jp := jsonPage[oapi.LivekitPublicapiObservabilityV1LogRecord]{Items: page.Records, NextCursor: page.NextCursor}
	if err := renderPage(p, asJSON, jp, empty, logLines); err != nil || asJSON {
		return err
	}
	moreAvailable(p, "records", page.NextCursor)
	return nil
}

// logLines renders log records one per line, their loggers padded so the
// messages line up.
func logLines(records []oapi.LivekitPublicapiObservabilityV1LogRecord) []string {
	loggerWidth := 1
	for _, r := range records {
		loggerWidth = max(loggerWidth, len([]rune(dashText(r.Logger))))
	}
	lines := make([]string, 0, len(records))
	for _, r := range records {
		lines = append(lines, logLine(r, loggerWidth))
	}
	return lines
}

// logLine renders one log record as a single line, its logger padded to
// loggerWidth so the messages line up.
func logLine(r oapi.LivekitPublicapiObservabilityV1LogRecord, loggerWidth int) string {
	at := "-"
	if r.Timestamp != nil && !r.Timestamp.IsZero() {
		at = r.Timestamp.Local().Format("15:04:05.000")
	}
	return fmt.Sprintf("%-12s  %-6s  %-*s  %s", at, logLevelName(r), loggerWidth, dashText(r.Logger), oneLine(util.Deref(r.Message)))
}

// logLevelName names a record's level: its typed level, or the name the agent
// logged when the level isn't one the API knows, or "-" for a record with
// neither, such as a passed evaluation.
func logLevelName(r oapi.LivekitPublicapiObservabilityV1LogRecord) string {
	if r.Level != nil && *r.Level != oapi.LOGLEVELUNSPECIFIED {
		return strings.TrimPrefix(string(*r.Level), "LOG_LEVEL_")
	}
	return util.Dash(strings.ToUpper(oneLine(util.Deref(r.SeverityText))))
}

// SessionTraces prints a session's spans as a tree built from their parent
// ids, one line per span with its start time, duration and name, and a failed
// span's status message; --json prints the spans as the API sent them. A span
// whose parent isn't among them prints as a root, as the dashboard shows it.
// empty says why there are no spans, on stderr in both modes so --json output
// stays parseable. page.NextCursor is where the read stopped short of the
// session's last span.
func SessionTraces(p *util.Printer, asJSON bool, page public.TracePage, empty string) error {
	jp := jsonPage[oapi.LivekitPublicapiObservabilityV1Span]{Items: page.Spans, NextCursor: page.NextCursor}
	if err := renderPage(p, asJSON, jp, empty, spanLines); err != nil || asJSON || page.NextCursor == "" {
		return err
	}
	p.Statusf("Printed %d spans; more remain — raise --limit to read them into this tree, or re-run with %s for the next ones",
		len(page.Spans), util.Accented("--cursor "+page.NextCursor))
	return nil
}

// spanLines renders spans as a tree, one line per span.
func spanLines(spans []oapi.LivekitPublicapiObservabilityV1Span) []string {
	tree := spanTree(spans)
	lines := make([]string, 0, len(tree))
	for _, l := range tree {
		lines = append(lines, spanLine(l.span, l.guide))
	}
	return lines
}

// spanTreeLine is a span in tree order with the guide that draws its place.
type spanTreeLine struct {
	span  oapi.LivekitPublicapiObservabilityV1Span
	guide string
}

// spanTree orders spans depth-first from their roots, keeping the API's start
// time order among siblings. A root is a span with no parent or whose parent
// isn't among spans. Spans whose parents only name each other, which an agent
// shouldn't export, print from the first of them, so every span prints once.
func spanTree(spans []oapi.LivekitPublicapiObservabilityV1Span) []spanTreeLine {
	ids := make(map[string]bool, len(spans))
	for _, s := range spans {
		ids[util.Deref(s.SpanId)] = true
	}
	children := make(map[string][]int)
	var roots []int
	for i, s := range spans {
		if parent := util.Deref(s.ParentSpanId); parent != "" && ids[parent] {
			children[parent] = append(children[parent], i)
		} else {
			roots = append(roots, i)
		}
	}

	lines := make([]spanTreeLine, 0, len(spans))
	visited := make([]bool, len(spans))
	var walk func(i int, guide, indent string)
	walk = func(i int, guide, indent string) {
		visited[i] = true
		lines = append(lines, spanTreeLine{span: spans[i], guide: guide})
		var next []int
		for _, c := range children[util.Deref(spans[i].SpanId)] {
			if !visited[c] {
				next = append(next, c)
			}
		}
		for n, c := range next {
			if n == len(next)-1 {
				walk(c, indent+"└─ ", indent+"   ")
			} else {
				walk(c, indent+"├─ ", indent+"│  ")
			}
		}
	}
	for _, i := range roots {
		walk(i, "", "")
	}
	for i := range spans {
		if !visited[i] {
			walk(i, "", "")
		}
	}
	return lines
}

// spanLine renders one span as a single line: its start time, its duration,
// its name after the tree guide, and a failed span's status message.
func spanLine(s oapi.LivekitPublicapiObservabilityV1Span, guide string) string {
	at := "-"
	if s.StartTime != nil && !s.StartTime.IsZero() {
		at = s.StartTime.Local().Format("15:04:05.000")
	}
	line := fmt.Sprintf("%-12s  %8s  %s%s", at, spanDuration(s), guide, dashText(s.Name))
	if s.Status != nil && *s.Status == oapi.SPANSTATUSERROR {
		if msg := oneLine(util.Deref(s.StatusMessage)); msg != "" {
			line += "  [error: " + msg + "]"
		} else {
			line += "  [error]"
		}
	}
	return line
}

// spanDuration renders how long a span took, or a dash for one that hasn't
// ended.
func spanDuration(s oapi.LivekitPublicapiObservabilityV1Span) string {
	if s.StartTime == nil || s.EndTime == nil || s.EndTime.Before(*s.StartTime) {
		return "-"
	}
	d := s.EndTime.Sub(*s.StartTime)
	switch {
	case d < time.Second:
		return formatMs(float64(d) / float64(time.Millisecond))
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// SessionMetrics prints a page of a session's agent metrics, one line per
// point with its end time, its metric's name and its value or histogram
// summary; --json prints the points as the API sent them, attributes and
// buckets included. empty says why a page has no points, on stderr in both
// modes so --json output stays parseable.
func SessionMetrics(p *util.Printer, asJSON bool, page public.MetricPage, empty string) error {
	jp := jsonPage[public.MetricPoint]{Items: page.Points, NextCursor: page.NextCursor}
	if err := renderPage(p, asJSON, jp, empty, metricLines); err != nil || asJSON {
		return err
	}
	moreAvailable(p, "points", page.NextCursor)
	return nil
}

// metricLines renders metric points one per line, their names padded so the
// values line up.
func metricLines(points []public.MetricPoint) []string {
	nameWidth := 1
	for _, pt := range points {
		nameWidth = max(nameWidth, len([]rune(oneLine(pt.Name))))
	}
	lines := make([]string, 0, len(points))
	for _, pt := range points {
		lines = append(lines, metricLine(pt, nameWidth))
	}
	return lines
}

// metricLine renders one metric point as a single line, its name padded to
// nameWidth so the values line up.
func metricLine(pt public.MetricPoint, nameWidth int) string {
	at := "-"
	if pt.EndTime != nil && !pt.EndTime.IsZero() {
		at = pt.EndTime.Local().Format("15:04:05.000")
	}
	return fmt.Sprintf("%-12s  %-*s  %s", at, nameWidth, util.Dash(oneLine(pt.Name)), metricValue(pt))
}

// metricValue renders a gauge's or sum's value, or a histogram's count, sum,
// min and max (each only when the agent recorded it), in the metric's unit;
// a dash for a point of a kind newer than this client.
func metricValue(pt public.MetricPoint) string {
	unit := metricUnit(pt.Unit)
	switch {
	case pt.Value != nil:
		return formatMetricNumber(*pt.Value, unit)
	case pt.Histogram != nil:
		h := pt.Histogram
		parts := []string{"count " + util.Dash(util.Deref(h.Count))}
		for _, f := range []struct {
			label string
			v     *float64
		}{{"sum", h.Sum}, {"min", h.Min}, {"max", h.Max}} {
			if f.v != nil {
				parts = append(parts, f.label+" "+formatMetricNumber(*f.v, unit))
			}
		}
		return strings.Join(parts, ", ")
	default:
		return "-"
	}
}

// metricUnit makes an OpenTelemetry unit readable after a number: an
// annotation such as {token} loses its braces, and the dimensionless "1" is
// dropped.
func metricUnit(unit string) string {
	unit = strings.NewReplacer("{", "", "}", "").Replace(oneLine(unit))
	if unit == "1" {
		return ""
	}
	return unit
}

// formatMetricNumber renders a metric's number with its unit: a whole number
// in full, anything else to six significant digits.
func formatMetricNumber(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'g', 6, 64)
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		s = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if unit == "" {
		return s
	}
	return s + " " + unit
}

// RecordingLabel names a recording ("audio" or "chat-history") for a sentence.
func RecordingLabel(recording string) string {
	if recording == public.RecordingChatHistory {
		return "chat history"
	}
	return recording + " recording"
}

// RecordingURL prints a signed recording URL. As text it is the bare URL on
// stdout, so it pipes into curl, with its expiry as a status line; as JSON it
// is the API's response, recording start included.
func RecordingURL(p *util.Printer, asJSON bool, r oapi.LivekitPublicapiObservabilityV1RecordingGetURLResponse) error {
	if asJSON {
		return util.PrintJSONTo(p.ResultWriter(), r)
	}
	p.Result(util.Deref(r.Url))
	p.Statusf("The URL expires %s", util.FormatTime(r.ExpiresAt))
	return nil
}

// SavedRecording is a recording downloaded to a file, and its --json shape.
type SavedRecording struct {
	SessionID          string     `json:"sessionId"`
	Recording          string     `json:"recording"`
	File               string     `json:"file"`
	Bytes              int64      `json:"bytes"`
	RecordingStartedAt *time.Time `json:"recordingStartedAt,omitempty"`
}

// RecordingSaved says where a downloaded recording was saved, and when the
// recording started (to line the audio up with the transcript).
func RecordingSaved(p *util.Printer, asJSON bool, s SavedRecording) error {
	if asJSON {
		return util.PrintJSONTo(p.ResultWriter(), s)
	}
	p.Statusf("Saved %s of session %s to %s (%s)", RecordingLabel(s.Recording), s.SessionID,
		util.Accented(s.File), util.FormatBytes(json.RawMessage(strconv.FormatInt(s.Bytes, 10))))
	if s.RecordingStartedAt != nil {
		p.Statusf("The recording started %s", util.FormatTime(s.RecordingStartedAt))
	}
	return nil
}
