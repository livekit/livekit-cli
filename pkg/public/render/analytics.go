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

// renderParticipants prints a page of participants as a table. Like the
// server, it counts a page as empty only with no participants and no next
// cursor, so it never says there are none beside a hint that there are more.
// It leaves that hint to the caller.
func renderParticipants(p *util.Printer, participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo, nextCursor string) error {
	if len(participants) == 0 {
		if nextCursor == "" {
			p.Status("No participants found")
		}
		return nil
	}
	return util.RenderList(p, false, participants, "", participantHeaders, participantRow)
}

// SessionParticipantsPage prints a cursor-paginated page of a session's
// participants. As JSON it emits {items, nextCursor} with the API's rows.
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

// jsonPage is the --json shape of a page of a session's transcript: {items,
// nextCursor}, like util.RenderPage's, with each item as the API sent it.
type jsonPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	// SkippedRecords counts the page's records the server couldn't read as
	// items.
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
