package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/twitchtv/twirp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/livekit/protocol/livekit"
)

func evUtterance(jobID string, attempt uint32, seq int64, ordinal uint32, text string) *livekit.SimulationRun_JobEvent {
	return &livekit.SimulationRun_JobEvent{
		RunSeq:  seq, // tests reuse seq as run_seq; the store only needs monotonicity
		JobId:   jobID,
		Attempt: attempt,
		Seq:     seq,
		Ts:      timestamppb.Now(),
		Type:    livekit.SimulationRun_JobEvent_TYPE_PERSONA_UTTERANCE,
		Ordinal: ordinal,
		Text:    text,
	}
}

func page(status livekit.SimulationRun_Status, hasMore bool, events ...*livekit.SimulationRun_JobEvent) *livekit.SimulationRun_GetEvents_Response {
	next := int64(0)
	for _, ev := range events {
		if ev.RunSeq > next {
			next = ev.RunSeq
		}
	}
	return &livekit.SimulationRun_GetEvents_Response{
		Events:    events,
		Next:      next,
		HasMore:   hasMore,
		RunStatus: status,
	}
}

func TestEventStoreDedupesRedeliveredPages(t *testing.T) {
	s := newEventStore()
	p := page(livekit.SimulationRun_STATUS_RUNNING, false,
		evUtterance("SRJ_a", 0, 1, 1, "one"),
		evUtterance("SRJ_a", 0, 2, 2, "two"),
	)
	if got := len(s.Apply(p)); got != 2 {
		t.Fatalf("first apply: %d", got)
	}
	if got := len(s.Apply(p)); got != 0 {
		t.Fatalf("redelivered page applied %d events", got)
	}
	if s.total != 2 || s.feed("SRJ_a").lastSeq != 2 {
		t.Fatalf("store state: total=%d lastSeq=%d", s.total, s.feed("SRJ_a").lastSeq)
	}
}

func TestEventStoreInterleavesJobsAndKeepsCursor(t *testing.T) {
	s := newEventStore()
	s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false,
		evUtterance("SRJ_a", 0, 1, 1, "a1"),
		evUtterance("SRJ_b", 0, 2, 1, "b1"),
		evUtterance("SRJ_a", 0, 3, 2, "a2"),
	))
	if s.cursor != 3 {
		t.Fatalf("cursor: %d", s.cursor)
	}
	if len(s.feed("SRJ_a").events) != 2 || len(s.feed("SRJ_b").events) != 1 {
		t.Fatalf("feed sizes: a=%d b=%d", len(s.feed("SRJ_a").events), len(s.feed("SRJ_b").events))
	}
}

func TestEventStoreHigherAttemptResetsTheFeed(t *testing.T) {
	s := newEventStore()
	s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false,
		evUtterance("SRJ_a", 0, 1, 1, "first try"),
		evUtterance("SRJ_a", 0, 2, 2, "still first"),
	))
	s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false,
		evUtterance("SRJ_a", 1, 3, 1, "take two"),
		// a straggler from the dead attempt must not resurrect it
		evUtterance("SRJ_a", 0, 4, 3, "ghost"),
	))
	feed := s.feed("SRJ_a")
	if feed.attempt != 1 || len(feed.events) != 1 {
		t.Fatalf("feed after retry: attempt=%d events=%d", feed.attempt, len(feed.events))
	}
	if feed.events[0].Text != "take two" {
		t.Fatalf("feed content: %q", feed.events[0].Text)
	}
}

func TestEventStoreCapsPerJobFeed(t *testing.T) {
	s := newEventStore()
	for i := range maxEventsPerJobFeed + 50 {
		seq := int64(i + 1)
		s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false,
			evUtterance("SRJ_a", 0, seq, uint32(seq), fmt.Sprintf("t%d", seq))))
	}
	if got := len(s.feed("SRJ_a").events); got != maxEventsPerJobFeed {
		t.Fatalf("cap: %d", got)
	}
}

func TestEventStoreDrainsOnTerminalEmptyPage(t *testing.T) {
	s := newEventStore()
	s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false, evUtterance("SRJ_a", 0, 1, 1, "hi")))
	if s.drained {
		t.Fatal("drained while running")
	}
	// terminal but still delivering: keep going
	s.Apply(page(livekit.SimulationRun_STATUS_COMPLETED, false, evUtterance("SRJ_a", 0, 2, 2, "tail")))
	if s.drained {
		t.Fatal("drained while a terminal page still had events")
	}
	s.Apply(page(livekit.SimulationRun_STATUS_COMPLETED, false))
	if !s.drained || s.tickEligible() {
		t.Fatalf("terminal empty page must drain the store")
	}
}

func TestEventStoreBackoffAfterConsecutiveFailures(t *testing.T) {
	s := newEventStore()
	for range 5 {
		s.applyError(errors.New("boom"))
	}
	eligible := 0
	for range 10 {
		if s.tickEligible() {
			eligible++
		}
	}
	if eligible != 2 {
		t.Fatalf("backoff let %d of 10 ticks through", eligible)
	}
	// one success resets the backoff
	s.Apply(page(livekit.SimulationRun_STATUS_RUNNING, false))
	if !s.tickEligible() {
		t.Fatal("success did not reset the backoff")
	}
}

func TestEventsUnsupportedDetection(t *testing.T) {
	s := newEventStore()
	s.applyError(twirp.NewError(twirp.BadRoute, "no such method"))
	if s.support != eventsUnsupported || s.tickEligible() {
		t.Fatal("bad_route must disable the feed for good")
	}
	if isEventsUnsupported(errors.New("connection refused")) {
		t.Fatal("transient errors are not unsupported")
	}
}

func TestFormatEventLine(t *testing.T) {
	wer := float32(0.25)
	heard := &livekit.SimulationRun_JobEvent{
		Type:       livekit.SimulationRun_JobEvent_TYPE_AGENT_HEARD_PERSONA,
		RefOrdinal: 2,
		Text:       "book a table for for",
		Wer:        &wer,
	}
	if got := formatEventLine("greeting", heard); got != "[greeting] agent heard #2 (WER 25%): book a table for for" {
		t.Fatalf("heard line: %q", got)
	}
	phase := &livekit.SimulationRun_JobEvent{
		Type:   livekit.SimulationRun_JobEvent_TYPE_JOB_PHASE,
		Phase:  livekit.SimulationRun_JobEvent_PHASE_FINISHED,
		Detail: "goal met",
	}
	if got := formatEventLine("greeting", phase); got != "[greeting] · finished (goal met)" {
		t.Fatalf("phase line: %q", got)
	}
}
