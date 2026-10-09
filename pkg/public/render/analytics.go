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
		util.DashString(p.ParticipantIdentity), util.DashString(p.ParticipantName),
		util.FormatTime(p.JoinedAt), util.FormatTime(p.LeftAt),
		util.DashString(p.Location), util.DashString(p.Region), publishedSources(p.PublishedSources),
	}
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

// SessionParticipantsPage prints a cursor-paginated page of a session's
// participants. As JSON it emits {items, nextCursor} with the API's rows.
func SessionParticipantsPage(p *util.Printer, asJSON bool, participants []oapi.LivekitPublicapiAnalyticsV1ParticipantInfo, nextCursor string) error {
	if asJSON {
		return util.RenderPage(p, true, participants, nextCursor, "No participants found", participantHeaders, participantRow)
	}
	if err := util.RenderList(p, false, participants, "No participants found", participantHeaders, participantRow); err != nil {
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
	if err := util.RenderList(p, false, util.Deref(d.Participants), "No participants found", participantHeaders, participantRow); err != nil {
		return err
	}
	if next := util.Deref(d.ParticipantsPage.NextCursor); next != "" {
		p.Statusf("More participants available — list them with %s, using the same flags as this command",
			util.Accented(listCmd+" --cursor "+next))
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
