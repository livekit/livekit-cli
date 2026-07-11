package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/twitchtv/twirp"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// The live event stream: an append-only feed per job, fetched with a
// run-global cursor alongside the 1s run poll. Events carry their own WER and
// word alignment computed by the simulator — this file only renders them.

const maxEventsPerJobFeed = 500

type eventsSupport int

const (
	eventsSupportUnknown eventsSupport = iota
	eventsSupported
	eventsUnsupported
)

type turnKey struct {
	persona bool
	ordinal uint32
	// A transcript with no uttered turn to anchor to (e.g. the agent's speech
	// when no session host reports its text) renders as its own card.
	standalone bool
}

// matrixTurn is one uttered/heard pair: what a party said and what the other
// party's transcription of it looked like, as the events arrive.
type matrixTurn struct {
	uttered   string
	utteredEv *livekit.SimulationRun_JobEvent // the utterance event: ts, agent self-reports
	heard     *livekit.SimulationRun_JobEvent // AGENT_HEARD_PERSONA / PERSONA_HEARD_AGENT
	playout   *livekit.SimulationRun_JobEvent // PERSONA_PLAYOUT
	// any playout segment cut by barge-in: the scripted tail was never voiced
	playoutInterrupted bool
}

type jobFeed struct {
	attempt uint32
	// exact-seq dedup: redelivered events drop, but a lower-seq event
	// arriving after a higher one (producer emitted out of order) still
	// applies — a high-water mark would silently eat whole turns
	seen map[int64]struct{}

	events []*livekit.SimulationRun_JobEvent // arrival order, capped
	order  []turnKey
	turns  map[turnKey]*matrixTurn

	phase       livekit.SimulationRun_JobEvent_Phase
	phaseDetail string
	// True once any agent-side perception arrived; when it never does (e.g.
	// SIP, no session host) the matrix says so once instead of per turn.
	hasAgentSide bool
	firstTs      time.Time

	rendered      string
	renderedCount int
	renderedWidth int
}

func newJobFeed() *jobFeed {
	return &jobFeed{turns: make(map[turnKey]*matrixTurn), seen: make(map[int64]struct{})}
}

func (f *jobFeed) turn(key turnKey) *matrixTurn {
	t, ok := f.turns[key]
	if !ok {
		t = &matrixTurn{}
		f.turns[key] = t
		f.order = append(f.order, key)
	}
	return t
}

func (f *jobFeed) apply(ev *livekit.SimulationRun_JobEvent) {
	if f.firstTs.IsZero() && ev.Ts != nil {
		f.firstTs = ev.Ts.AsTime()
	}
	f.events = append(f.events, ev)
	if len(f.events) > maxEventsPerJobFeed {
		f.events = f.events[len(f.events)-maxEventsPerJobFeed:]
	}

	switch ev.Type {
	case livekit.SimulationRun_JobEvent_TYPE_PERSONA_UTTERANCE:
		t := f.turn(turnKey{persona: true, ordinal: ev.Ordinal})
		t.uttered = ev.Text
		t.utteredEv = ev
	case livekit.SimulationRun_JobEvent_TYPE_AGENT_UTTERANCE:
		t := f.turn(turnKey{persona: false, ordinal: ev.Ordinal})
		t.uttered = ev.Text
		t.utteredEv = ev
		f.hasAgentSide = true
	case livekit.SimulationRun_JobEvent_TYPE_AGENT_HEARD_PERSONA:
		if t := f.turn(heardKey(true, ev)); t.heard == nil || ev.Seq > t.heard.Seq {
			t.heard = ev
		}
		f.hasAgentSide = true
	case livekit.SimulationRun_JobEvent_TYPE_PERSONA_HEARD_AGENT:
		if t := f.turn(heardKey(false, ev)); t.heard == nil || ev.Seq > t.heard.Seq {
			t.heard = ev
		}
	case livekit.SimulationRun_JobEvent_TYPE_PERSONA_PLAYOUT:
		// a turn endpointed into several segments: the first one carries the
		// turn's start time; interruption on any segment is sticky
		t := f.turn(turnKey{persona: true, ordinal: ev.RefOrdinal})
		if t.playout == nil {
			t.playout = ev
		}
		t.playoutInterrupted = t.playoutInterrupted || ev.Interrupted
	case livekit.SimulationRun_JobEvent_TYPE_JOB_PHASE:
		f.phase = ev.Phase
		f.phaseDetail = ev.Detail
	}
}

// heardKey anchors a transcript to its uttered turn; unanchored transcripts
// (ref_ordinal 0) become standalone cards keyed by their own ordinal.
func heardKey(persona bool, ev *livekit.SimulationRun_JobEvent) turnKey {
	if ev.RefOrdinal == 0 {
		return turnKey{persona: persona, ordinal: ev.Ordinal, standalone: true}
	}
	return turnKey{persona: persona, ordinal: ev.RefOrdinal}
}

// audioShaped reports whether the feed carries perception or playout streams —
// the signal to render the uttered/heard matrix instead of a plain transcript.
func (f *jobFeed) audioShaped() bool {
	for _, t := range f.turns {
		if t.heard != nil || t.playout != nil {
			return true
		}
	}
	return false
}

type eventStore struct {
	cursor  int64
	feeds   map[string]*jobFeed
	support eventsSupport
	drained bool
	total   int

	failures  int
	skipTicks int
}

func newEventStore() *eventStore {
	return &eventStore{feeds: make(map[string]*jobFeed)}
}

func (s *eventStore) feed(jobID string) *jobFeed {
	return s.feeds[jobID]
}

// tickEligible implements the transient-failure backoff: after 5 consecutive
// failed polls, only every 5th tick tries again.
func (s *eventStore) tickEligible() bool {
	if s.support == eventsUnsupported || s.drained {
		return false
	}
	if s.failures >= 5 {
		if s.skipTicks < 4 {
			s.skipTicks++
			return false
		}
		s.skipTicks = 0
	}
	return true
}

func (s *eventStore) applyError(err error) {
	if isEventsUnsupported(err) {
		s.support = eventsUnsupported
		return
	}
	s.failures++
}

// Apply merges a page into the store and returns the newly applied events in
// order. Redelivered pages and lower attempts drop out here; a higher attempt
// resets its job's feed — the retry restarted the conversation.
func (s *eventStore) Apply(resp *livekit.SimulationRun_GetEvents_Response) []*livekit.SimulationRun_JobEvent {
	s.support = eventsSupported
	s.failures = 0
	var applied []*livekit.SimulationRun_JobEvent
	for _, ev := range resp.Events {
		if ev == nil || ev.Type == livekit.SimulationRun_JobEvent_TYPE_UNSPECIFIED {
			continue
		}
		feed, ok := s.feeds[ev.JobId]
		if !ok {
			feed = newJobFeed()
			feed.attempt = ev.Attempt
			s.feeds[ev.JobId] = feed
		}
		if ev.Attempt < feed.attempt {
			continue
		}
		if ev.Attempt > feed.attempt {
			reset := newJobFeed()
			reset.attempt = ev.Attempt
			s.feeds[ev.JobId] = reset
			feed = reset
		}
		if _, dup := feed.seen[ev.Seq]; dup {
			continue
		}
		feed.seen[ev.Seq] = struct{}{}
		feed.apply(ev)
		s.total++
		applied = append(applied, ev)
	}
	if resp.Next > s.cursor {
		s.cursor = resp.Next
	}
	if isTerminalRunStatus(resp.RunStatus) && !resp.HasMore && len(resp.Events) == 0 {
		s.drained = true
	}
	return applied
}

func getSimulationRunEvents(ctx context.Context, client *lksdk.AgentSimulationClient, runID string, after int64) (*livekit.SimulationRun_GetEvents_Response, error) {
	return client.GetSimulationRunEvents(ctx, &livekit.SimulationRun_GetEvents_Request{
		SimulationRunId: runID,
		After:           after,
	})
}

// isEventsUnsupported detects a server without the events RPC — the CLI then
// falls back to today's behavior for the rest of the run, silently.
func isEventsUnsupported(err error) bool {
	var twerr twirp.Error
	if errors.As(err, &twerr) {
		switch twerr.Code() {
		case twirp.BadRoute, twirp.Unimplemented, twirp.NotFound:
			return true
		}
	}
	return false
}

// --- rendering ---

// renderEventTranscript is the live text-mode conversation: utterances in
// arrival order with the same You/Agent styling as the final transcript,
// phase milestones as dim markers.
func renderEventTranscript(feed *jobFeed, width int) string {
	userStyle := lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
	agentStyle := lipgloss.NewStyle().Foreground(util.Success()).Bold(true)
	wrapStyle := lipgloss.NewStyle().Width(eventWrapWidth(width))

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("  Transcript"))
	b.WriteString(dimStyle.Render(" · live"))
	b.WriteString("\n")
	for _, ev := range feed.events {
		switch ev.Type {
		case livekit.SimulationRun_JobEvent_TYPE_PERSONA_UTTERANCE:
			b.WriteString("\n    " + userStyle.Render("Persona") + "\n")
			writeWrapped(&b, wrapStyle, ev.Text, "      ", "")
		case livekit.SimulationRun_JobEvent_TYPE_AGENT_UTTERANCE:
			b.WriteString("\n    " + agentStyle.Render("Agent") + "\n")
			writeWrapped(&b, wrapStyle, ev.Text, "      ", "")
		case livekit.SimulationRun_JobEvent_TYPE_JOB_PHASE:
			b.WriteString(dimStyle.Render("    · "+phaseLabel(ev)) + "\n")
		}
	}
	return b.String()
}

// renderUtteredHeard is the audio-mode matrix: per turn, what each party said
// and what the other heard, with the simulator-computed WER and alignment.
// renderUtteredHeard is the annotated conversation: one text per turn set in
// the speaker's color, perception rendered as marks ON the words (missed =
// red underline, misheard = underline + →gloss, invented = +word), state as
// right-aligned chips. `raw` switches to the classic two-transcript view.
func renderUtteredHeard(feed *jobFeed, width int, jobRunning, raw bool) string {
	if raw {
		return renderUtteredHeardClassic(feed, width, jobRunning)
	}
	return renderAnnotatedConversation(feed, width, jobRunning)
}

func renderUtteredHeardClassic(feed *jobFeed, width int, jobRunning bool) string {
	userStyle := lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
	agentStyle := lipgloss.NewStyle().Foreground(util.Success()).Bold(true)
	wrapStyle := lipgloss.NewStyle().Width(eventWrapWidth(width))

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("  Conversation"))
	b.WriteString(dimStyle.Render(" · live · uttered vs heard"))
	b.WriteString("\n")
	if !feed.hasAgentSide {
		b.WriteString(dimStyle.Render("    agent-side capture unavailable (no remote session)") + "\n")
	}

	// The newest heard snapshot per lane is provisional while the job runs:
	// its turn may still be mid-transcription, so verdicts soften to "so far".
	var lastPersona, lastAgent turnKey
	var maxPersona, maxAgent int64
	for key, t := range feed.turns {
		if t == nil || t.heard == nil {
			continue
		}
		if key.persona && t.heard.Seq > maxPersona {
			maxPersona, lastPersona = t.heard.Seq, key
		}
		if !key.persona && t.heard.Seq > maxAgent {
			maxAgent, lastAgent = t.heard.Seq, key
		}
	}

	// Real agent turns supersede the bare pre-gold snapshots (a greeting's
	// STT final lands before the first gold event): once any agent turn has
	// its own text, standalone simulator-transcription cards are stale
	// duplicates of content shown under the real turns.
	agentGold := false
	for key, t := range feed.turns {
		if !key.persona && !key.standalone && t != nil && t.uttered != "" {
			agentGold = true
			break
		}
	}

	for _, key := range sortedTurnKeys(feed) {
		t := feed.turns[key]
		if t == nil {
			continue
		}
		if !key.persona && key.standalone && agentGold {
			continue
		}
		if key.persona {
			// No uttered text: either a playout-only ghost or a perception
			// fragment that raced ahead of any persona turn — a later
			// re-segmentation anchors it, so an empty card would be noise.
			if t.uttered == "" {
				continue
			}
			header := "    " + userStyle.Render("Persona (spoke)")
			if off, ok := turnOffset(feed, t); ok {
				header += dimStyle.Render(" · " + formatTurnOffset(off))
			}
			if t.playoutInterrupted {
				header += " " + redStyle().Render("· ⚡ interrupted")
			}
			b.WriteString("\n" + header + "\n")
			writeWrapped(&b, wrapStyle, t.uttered, "      ", "")
			b.WriteString(renderHeardLine(
				t, "agent heard", wrapStyle, jobRunning, feed.hasAgentSide,
				jobRunning && maxPersona > 0 && key == lastPersona,
			))
		} else {
			if t.uttered == "" && t.heard == nil {
				continue
			}
			header := "    " + agentStyle.Render("Agent (spoke)")
			if off, ok := turnOffset(feed, t); ok {
				header += dimStyle.Render(" · " + formatTurnOffset(off))
			}
			if t.utteredEv != nil && t.utteredEv.E2ELatencyMs != nil {
				header += dimStyle.Render(fmt.Sprintf(" · ↳ %dms", *t.utteredEv.E2ELatencyMs))
			}
			b.WriteString("\n" + header + "\n")
			if t.uttered != "" {
				writeWrapped(&b, wrapStyle, t.uttered, "      ", "")
			} else {
				// No agent gold text (no session host): the simulator's own
				// transcription is the only record of what was said.
				writeWrapped(&b, wrapStyle, t.heard.Text, "      ", dimStyle.Render(" (simulator transcription)"))
				continue
			}
			b.WriteString(renderHeardLine(
				t, "sim heard", wrapStyle, jobRunning, true,
				jobRunning && maxAgent > 0 && key == lastAgent,
			))
		}
	}
	return b.String()
}

// sortedTurnKeys orders cards by when their turn actually happened — playout
// start when audio timing exists, event timestamps otherwise — not by event
// arrival, which interleaves lanes out of conversational order. The sort is
// stable on arrival order for ties and unknown times.
func sortedTurnKeys(feed *jobFeed) []turnKey {
	keys := make([]turnKey, len(feed.order))
	copy(keys, feed.order)
	at := func(key turnKey) (time.Time, bool) {
		t := feed.turns[key]
		if t == nil {
			return time.Time{}, false
		}
		if t.playout != nil {
			return t.playout.GetStartedAt().AsTime(), true
		}
		if t.utteredEv != nil && t.utteredEv.Ts != nil {
			return t.utteredEv.Ts.AsTime(), true
		}
		if t.heard != nil && t.heard.Ts != nil {
			return t.heard.Ts.AsTime(), true
		}
		return time.Time{}, false
	}
	sort.SliceStable(keys, func(i, j int) bool {
		ti, iok := at(keys[i])
		tj, jok := at(keys[j])
		if !iok || !jok {
			return false // keep arrival order when either time is unknown
		}
		return ti.Before(tj)
	})
	return keys
}

// turnOffset is the turn's start relative to the first event: the playout
// clock when audio timing exists, the utterance event's timestamp otherwise.
func turnOffset(feed *jobFeed, t *matrixTurn) (time.Duration, bool) {
	if feed.firstTs.IsZero() {
		return 0, false
	}
	if t.playout != nil {
		return t.playout.GetStartedAt().AsTime().Sub(feed.firstTs), true
	}
	if t.utteredEv != nil && t.utteredEv.Ts != nil {
		return t.utteredEv.Ts.AsTime().Sub(feed.firstTs), true
	}
	return 0, false
}

// renderHeardLine renders the perception sub-line of a turn: verbatim
// collapse, the simulator-computed alignment with errors highlighted, or a
// pending marker while the job still runs.
type styledWord struct {
	text  string
	style lipgloss.Style
}

// annotatedWords builds the turn's word flow: gold text styled by span
// status, glosses and insertions interleaved where they happened, and an
// interrupted tail faded after the inline cut marker.
func annotatedWords(t *matrixTurn, speech lipgloss.Style, cutAt string) []styledWord {
	missed := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Underline(true)
	gloss := redStyle()
	faint := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	var words []styledWord
	goldCount := 0
	if t.heard != nil && len(t.heard.Alignment) > 0 {
		for _, span := range t.heard.Alignment {
			goldFields := strings.Fields(span.Gold)
			goldCount += len(goldFields)
			switch span.Kind {
			case livekit.SimulationRun_JobEvent_Align_KIND_EQUAL:
				for _, w := range goldFields {
					words = append(words, styledWord{w, speech})
				}
			case livekit.SimulationRun_JobEvent_Align_KIND_DELETION:
				for _, w := range goldFields {
					words = append(words, styledWord{w, missed})
				}
			case livekit.SimulationRun_JobEvent_Align_KIND_SUBSTITUTION:
				for _, w := range goldFields {
					words = append(words, styledWord{w, missed})
				}
				if span.Heard != "" {
					words = append(words, styledWord{"→" + span.Heard, gloss})
				}
			case livekit.SimulationRun_JobEvent_Align_KIND_INSERTION:
				if span.Heard != "" {
					words = append(words, styledWord{"+" + span.Heard, gloss})
				}
			}
		}
	}
	uttered := strings.Fields(t.uttered)
	if len(words) == 0 {
		for _, w := range uttered {
			words = append(words, styledWord{w, speech})
		}
		if t.playoutInterrupted && cutAt != "" && len(words) > 0 {
			// cut position unknown without alignment: mark up front
			words = append([]styledWord{{"⚡" + cutAt, warn}}, words...)
			for i := 1; i < len(words); i++ {
				words[i].style = faint
			}
		}
		return words
	}
	// the scored gold is a prefix of the script; anything beyond it was
	// never voiced (interrupted) or not yet confirmed — render it faint
	if goldCount < len(uttered) {
		if cutAt != "" {
			words = append(words, styledWord{"⚡" + cutAt, warn})
		}
		for _, w := range uttered[goldCount:] {
			words = append(words, styledWord{w, faint})
		}
	}
	return words
}

// wrapStyledWords lays out pre-styled words with a hanging indent, returning
// rendered lines. Widths are computed on plain text so ANSI codes never split.
func wrapStyledWords(words []styledWord, width, indent int) []string {
	if width < 20 {
		width = 20
	}
	var lines []string
	var line strings.Builder
	lineWidth := 0
	pad := strings.Repeat(" ", indent)
	flush := func() {
		if lineWidth > 0 {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
	}
	for _, w := range words {
		wl := len([]rune(w.text))
		if lineWidth > 0 && indent+lineWidth+1+wl > width {
			flush()
		}
		if lineWidth == 0 {
			line.WriteString(pad)
		} else {
			line.WriteString(" ")
			lineWidth++
		}
		line.WriteString(w.style.Render(w.text))
		lineWidth += wl
	}
	flush()
	return lines
}

// turnChips is the right-aligned state column: verdict, latency, honesty.
func turnChips(t *matrixTurn, jobRunning, captured, provisional, suspect bool, cutAt string) string {
	var chips []string
	ev := t.heard
	switch {
	case ev == nil && t.uttered == "":
	case ev == nil && !captured:
	case ev == nil && jobRunning:
		chips = append(chips, dimStyle.Render("… listening"))
	case ev == nil && t.playoutInterrupted && cutAt != "":
		chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("⚡ cut "+cutAt))
	case ev == nil:
		chips = append(chips, dimStyle.Render("∅ no transcript"))
	case ev.Wer != nil && *ev.Wer == 0 && !provisional:
		chips = append(chips, greenStyle().Render("✓ verbatim"))
	case ev.Wer != nil && *ev.Wer == 0:
		chips = append(chips, dimStyle.Render("✓ so far"))
	case ev.Wer != nil:
		pct := 100 - int(math.Round(float64(*ev.Wer)*100))
		if pct < 0 {
			pct = 0
		}
		total := int(ev.GetWords())
		correct := total - int(ev.GetWordErrors())
		if correct < 0 {
			correct = 0
		}
		label := fmt.Sprintf("%d%% · %d/%d", pct, correct, total)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
		if pct < 80 {
			style = redStyle()
		}
		if provisional {
			chips = append(chips, dimStyle.Render("~"+label))
		} else {
			chips = append(chips, style.Render(label))
		}
	}
	if suspect {
		chips = append(chips, redStyle().Render("⚠"))
	}
	if t.utteredEv != nil && t.utteredEv.E2ELatencyMs != nil {
		chips = append(chips, dimStyle.Render(fmt.Sprintf("↳ %.1fs reply", float64(*t.utteredEv.E2ELatencyMs)/1000)))
	}
	return strings.Join(chips, " ")
}

// transcriptSuspect flags a confirmed catastrophic turn between clean
// neighbors — the signature of a transcript that doesn't match the audio.
func transcriptSuspect(feed *jobFeed, key turnKey, t *matrixTurn) bool {
	if key.standalone || t.heard == nil || t.heard.Wer == nil || *t.heard.Wer < 0.9 {
		return false
	}
	clean := 0
	for _, d := range []int{-1, 1} {
		n := feed.turns[turnKey{persona: key.persona, ordinal: uint32(int(key.ordinal) + d)}]
		if n != nil && n.heard != nil && n.heard.Wer != nil && *n.heard.Wer <= 0.1 {
			clean++
		}
	}
	return clean == 2
}

func renderAnnotatedConversation(feed *jobFeed, width int, jobRunning bool) string {
	personaName := lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
	agentName := lipgloss.NewStyle().Foreground(util.Success()).Bold(true)
	personaSpeech := lipgloss.NewStyle().Foreground(util.Brand())
	agentSpeech := lipgloss.NewStyle().Foreground(util.Success())

	const gutter = 16 // "m:ss Persona › "
	wrapWidth := width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}

	var lastPersona, lastAgent turnKey
	var maxPersona, maxAgent int64
	for key, t := range feed.turns {
		if t == nil || t.heard == nil {
			continue
		}
		if key.persona && t.heard.Seq > maxPersona {
			maxPersona, lastPersona = t.heard.Seq, key
		}
		if !key.persona && t.heard.Seq > maxAgent {
			maxAgent, lastAgent = t.heard.Seq, key
		}
	}
	agentGold := false
	for key, t := range feed.turns {
		if !key.persona && !key.standalone && t != nil && t.uttered != "" {
			agentGold = true
			break
		}
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("  Conversation"))
	b.WriteString(dimStyle.Render(" · live · d raw transcripts"))
	b.WriteString("\n\n")
	if !feed.hasAgentSide {
		b.WriteString(dimStyle.Render("    agent-side capture unavailable (no remote session)") + "\n\n")
	}

	for _, key := range sortedTurnKeys(feed) {
		t := feed.turns[key]
		if t == nil {
			continue
		}
		if key.persona && t.uttered == "" {
			continue
		}
		if !key.persona && key.standalone && agentGold {
			continue
		}
		if !key.persona && t.uttered == "" && t.heard == nil {
			continue
		}

		offset := "     "
		if off, ok := turnOffset(feed, t); ok {
			offset = fmt.Sprintf("%5s", formatTurnOffset(off))
		}
		var nameCell string
		var speech lipgloss.Style
		if key.persona {
			nameCell = personaName.Render("Persona ›")
			speech = personaSpeech
		} else {
			nameCell = agentName.Render("Agent   ‹")
			speech = agentSpeech
		}

		cutAt := ""
		if t.playoutInterrupted && t.playout != nil && !feed.firstTs.IsZero() {
			cutAt = formatTurnOffset(t.playout.GetEndedAt().AsTime().Sub(feed.firstTs))
		}
		body := t.uttered
		if body == "" && t.heard != nil {
			body = t.heard.Text // simulator transcription is the only record
		}
		turn := *t
		turn.uttered = body

		provisional := jobRunning &&
			((key.persona && maxPersona > 0 && key == lastPersona) ||
				(!key.persona && maxAgent > 0 && key == lastAgent))
		captured := !key.persona || feed.hasAgentSide
		suspect := transcriptSuspect(feed, key, t)

		words := annotatedWords(&turn, speech, cutAt)
		lines := wrapStyledWords(words, wrapWidth, gutter)
		chips := turnChips(t, jobRunning, captured, provisional, suspect, cutAt)

		for i, line := range lines {
			if i == 0 {
				prefix := dimStyle.Render(offset) + " " + nameCell + " "
				content := prefix + strings.TrimPrefix(line, strings.Repeat(" ", gutter))
				if chips != "" {
					pad := wrapWidth - lipgloss.Width(content) - lipgloss.Width(chips)
					if pad < 2 {
						b.WriteString(content + "\n")
						b.WriteString(strings.Repeat(" ", max(2, wrapWidth-lipgloss.Width(chips))) + chips + "\n")
						continue
					}
					content += strings.Repeat(" ", pad) + chips
				}
				b.WriteString(content + "\n")
			} else {
				b.WriteString(line + "\n")
			}
		}
		if len(lines) == 0 {
			prefix := dimStyle.Render(offset) + " " + nameCell + " "
			if chips != "" {
				b.WriteString(prefix + chips + "\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderHeardLine(t *matrixTurn, label string, wrapStyle lipgloss.Style, jobRunning, captured, provisional bool) string {
	if t.heard == nil {
		if t.uttered == "" || !captured {
			return ""
		}
		if jobRunning {
			return dimStyle.Render("      … awaiting transcription") + "\n"
		}
		// Job over, nothing ever heard: say why instead of going silent.
		// A persona turn without a playout event was generated but never
		// voiced (interrupted before speaking) — nothing existed to hear.
		if t.utteredEv != nil &&
			t.utteredEv.Type == livekit.SimulationRun_JobEvent_TYPE_PERSONA_UTTERANCE &&
			t.playout == nil {
			return dimStyle.Render("      ∅ never spoken (playout interrupted)") + "\n"
		}
		return dimStyle.Render("      ∅ no transcript received") + "\n"
	}
	ev := t.heard
	chip := ""
	if ev.Wer != nil && *ev.Wer == 0 {
		if provisional {
			// scored against the gold prefix heard so far — no verdict yet
			return dimStyle.Render("      ✓ heard cleanly · so far") + "\n"
		}
		return "      " + greenStyle().Render("✓ heard verbatim") + "\n"
	}
	if ev.Wer != nil {
		pct := 100 - int(math.Round(float64(*ev.Wer)*100))
		if pct < 0 {
			pct = 0
		}
		chip = fmt.Sprintf(" · %d%% correct", pct)
		if provisional {
			chip += " · so far"
		}
	}
	body := ev.Text
	if len(ev.Alignment) > 0 {
		body = renderAlignment(ev.Alignment)
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render("      " + label + chip + ":"))
	b.WriteString("\n")
	writeWrapped(&b, wrapStyle, body, "        ", "")
	return b.String()
}

// renderAlignment turns the event's word alignment into an inline diff:
// intact spans stay dim, substituted/inserted words go red, dropped gold
// words go red strikethrough in brackets.
func renderAlignment(spans []*livekit.SimulationRun_JobEvent_Align) string {
	strike := redStyle().Strikethrough(true)
	parts := make([]string, 0, len(spans))
	for _, span := range spans {
		switch span.Kind {
		case livekit.SimulationRun_JobEvent_Align_KIND_EQUAL:
			// heard side only: an equal span with no heard text is a gold
			// word whose heard words were already rendered elsewhere —
			// repeating the gold here would duplicate content
			if span.Heard != "" {
				parts = append(parts, dimStyle.Render(span.Heard))
			}
		case livekit.SimulationRun_JobEvent_Align_KIND_SUBSTITUTION:
			if span.Heard != "" {
				parts = append(parts, redStyle().Render(span.Heard))
			}
		case livekit.SimulationRun_JobEvent_Align_KIND_INSERTION:
			if span.Heard != "" {
				parts = append(parts, redStyle().Render("+"+span.Heard))
			}
		case livekit.SimulationRun_JobEvent_Align_KIND_DELETION:
			if span.Gold != "" {
				words := strings.Fields(span.Gold)
				if len(words) > 4 {
					// a long unheard stretch reads better as a count than
					// as a paragraph of strikethrough
					head := strings.Join(words[:3], " ")
					parts = append(parts, strike.Render(fmt.Sprintf("[%s … +%d unheard]", head, len(words)-3)))
				} else {
					parts = append(parts, strike.Render("["+span.Gold+"]"))
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

func phaseLabel(ev *livekit.SimulationRun_JobEvent) string {
	label := strings.ToLower(strings.TrimPrefix(ev.Phase.String(), "PHASE_"))
	label = strings.ReplaceAll(label, "_", " ")
	if ev.Detail != "" {
		label += " (" + ev.Detail + ")"
	}
	return label
}

func formatTurnOffset(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func eventWrapWidth(width int) int {
	w := width - 10
	if w < 40 {
		w = 40
	}
	return w
}

func writeWrapped(b *strings.Builder, wrapStyle lipgloss.Style, text, indent, suffix string) {
	lines := strings.Split(wrapStyle.Render(text), "\n")
	for i, line := range lines {
		b.WriteString(indent + line)
		if i == len(lines)-1 && suffix != "" {
			b.WriteString(suffix)
		}
		b.WriteString("\n")
	}
}

// formatEventLine is the CI mode's one-line rendering, prefixed with the job
// label so concurrent jobs interleave docker-compose style.
func formatEventLine(label string, ev *livekit.SimulationRun_JobEvent) string {
	prefix := fmt.Sprintf("[%s] ", label)
	switch ev.Type {
	case livekit.SimulationRun_JobEvent_TYPE_PERSONA_UTTERANCE:
		return prefix + "Persona: " + ev.Text
	case livekit.SimulationRun_JobEvent_TYPE_AGENT_UTTERANCE:
		return prefix + "Agent: " + ev.Text
	case livekit.SimulationRun_JobEvent_TYPE_AGENT_HEARD_PERSONA:
		chip := ""
		if ev.Wer != nil {
			chip = fmt.Sprintf(" (WER %.0f%%)", *ev.Wer*100)
		}
		return prefix + fmt.Sprintf("agent heard #%d%s: %s", ev.RefOrdinal, chip, ev.Text)
	case livekit.SimulationRun_JobEvent_TYPE_PERSONA_HEARD_AGENT:
		return prefix + fmt.Sprintf("sim heard #%d: %s", ev.RefOrdinal, ev.Text)
	case livekit.SimulationRun_JobEvent_TYPE_JOB_PHASE:
		return prefix + "· " + phaseLabel(ev)
	default:
		return ""
	}
}
