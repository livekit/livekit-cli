package livekit_cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

type Stats struct {
	Tracks  int64
	Packets int64
	Latency int64
	OOO     int64
	Dropped int64

	// parent
	name           string
	expectedTracks int
	children       []*Stats

	// children
	trackID string
	missing map[int64]bool
}

func (s *Stats) AddChild(child *Stats) {
	s.Tracks += child.Tracks
	s.Packets += child.Packets
	s.Latency += child.Latency
	s.OOO += child.OOO
	s.Dropped += child.Dropped

	s.expectedTracks += child.expectedTracks
	s.children = append(s.children, child)
}

func PrintResults(testers []*LoadTester) {
	summary := &Stats{}
	for _, t := range testers {
		ts := t.collectStats()
		ts.Print(false)
		summary.AddChild(ts)
	}

	summary.Print(true)
}

func (s *Stats) Print(summary bool) {
	var w *tabwriter.Writer
	if summary {
		w = tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
		_, _ = fmt.Fprint(w, "\nSummary\t| Tester\t| Tracks \t| Latency \t| Total OOO \t| Total Dropped\n")
	} else {
		w = tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
		_, _ = fmt.Fprintf(w, "\n%s\t| Track \t| Packets \t| Latency \t| OOO \t| Dropped\n", s.name)
	}

	for _, child := range s.children {
		child.PrintRow(w, summary, false)
	}
	s.PrintRow(w, summary, true)
	_ = w.Flush()
}

func (s *Stats) PrintRow(w *tabwriter.Writer, summary, total bool) {
	var name string
	if total {
		name = "Total"
	} else if summary {
		name = s.name
	} else {
		name = s.trackID
	}

	latency := " - "
	ooo := " - "
	dropped := " - "

	if s.Packets > 0 {
		latency = fmt.Sprint(time.Duration(s.Latency / s.Packets))
		ooo = fmt.Sprintf("%d (%s%%)", s.OOO, formatFloat(s.OOO, s.Packets))
		dropped = fmt.Sprintf("%d (%s%%)", s.Dropped, formatFloat(s.Dropped, s.Packets))
	}

	if summary {
		_, _ = fmt.Fprintf(w, "\t| %s\t| %d/%d\t| %s\t| %s\t| %s\n",
			name, s.Tracks, s.expectedTracks, latency, ooo, dropped)
	} else {
		_, _ = fmt.Fprintf(w, "\t| %s\t| %d\t| %s\t| %s\t| %s\n",
			name, s.Packets, latency, ooo, dropped)
	}
}

func formatFloat(num int64, total int64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", float64(num)/float64(total)), "0"), ".")
}
