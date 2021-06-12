package livekit_cli

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pion/rtp"
)

type Stats struct {
	name           string
	expectedTracks int
	trackStats     *sync.Map
}

type TrackStats struct {
	trackID string

	first uint16
	max   uint16

	packets      int64
	latency      int64
	latencyCount int64
	ooo          int64
	missing      map[uint16]bool
}

func (s *Stats) AddTrack(trackID string) {
	t := &TrackStats{
		trackID: trackID,
		missing: make(map[uint16]bool),
	}
	s.trackStats.Store(trackID, t)
}

func (s *Stats) Record(trackID string, pkt *rtp.Packet) {
	payload := pkt.Payload

	value, _ := s.trackStats.Load(trackID)
	ts := value.(*TrackStats)

	sentAt := int64(binary.LittleEndian.Uint64(payload[len(payload)-8:]))
	latency := time.Now().UnixNano() - sentAt
	if latency > 0 && latency < 1000000000 {
		ts.latency += time.Now().UnixNano() - sentAt
		ts.latencyCount++
	}

	expected := ts.max + 1
	if pkt.SequenceNumber != expected && ts.packets > 0 {
		if ts.missing[pkt.SequenceNumber] {
			delete(ts.missing, pkt.SequenceNumber)
			ts.ooo++
		} else {
			for i := expected; i <= pkt.SequenceNumber; i++ {
				ts.missing[i] = true
			}
		}
	}
	ts.packets++
	if pkt.SequenceNumber > ts.max {
		ts.max = pkt.SequenceNumber
	}
}

func (s *Stats) GetSummary() (tracks, packets, latency, latencyCount, ooo, dropped int64) {
	s.trackStats.Range(func(key, value interface{}) bool {
		trackStats := value.(*TrackStats)

		tracks++
		packets += trackStats.packets
		latency += trackStats.latency
		latencyCount += trackStats.latencyCount
		ooo += trackStats.ooo
		dropped += int64(len(trackStats.missing))

		return true
	})

	return
}

func (s *Stats) PrintTrackStats() {
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprintf(w, "\n%s\t| Track\t| Packets\t| Latency\t| OOO\t| Dropped\n", s.name)
	s.trackStats.Range(func(key, value interface{}) bool {
		trackStats := value.(*TrackStats)
		trackStats.Print(w)
		return true
	})
	_ = w.Flush()
}

func (s *Stats) Reset() {
	trackStats := sync.Map{}
	s.trackStats.Range(func(key, value interface{}) bool {
		old := value.(*TrackStats)
		trackStats.Store(key, &TrackStats{
			trackID: old.trackID,
			missing: make(map[uint16]bool),
		})
		return true
	})
	s.trackStats = &trackStats
}

func (t *TrackStats) Print(w *tabwriter.Writer) {
	latency, ooo, dropped := stringFormat(t.packets, t.latency, t.latencyCount, t.ooo, int64(len(t.missing)))
	_, _ = fmt.Fprintf(w, "\t| %s\t| %d\t| %s\t| %s\t| %s\n", t.trackID, t.packets, latency, ooo, dropped)
}

func stringFormat(packets, latency, latencyCount, ooo, dropped int64) (sLatency, sOOO, sDropped string) {
	sLatency = " - "
	sOOO = " - "
	sDropped = " - "

	if packets > 0 {
		totalPackets := packets + dropped
		if latencyCount > 0 {
			sLatency = fmt.Sprint(time.Duration(latency / latencyCount))
		}
		sOOO = fmt.Sprintf("%d (%s%%)", ooo, formatFloat(ooo, totalPackets))
		sDropped = fmt.Sprintf("%d (%s%%)", dropped, formatFloat(dropped, totalPackets))
	} else {
		sDropped = fmt.Sprintf("%d (100%%)", dropped)
	}

	return
}

func formatFloat(num int64, total int64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(num)/float64(total)*100), "0"), ".")
}
