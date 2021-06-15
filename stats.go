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

	resets int64
	first  int64
	max    int64

	packets      int64
	latency      int64
	latencyCount int64
	ooo          int64
	missing      map[int64]bool
}

func (s *Stats) AddTrack(trackID string) {
	t := &TrackStats{
		trackID: trackID,
		missing: make(map[int64]bool),
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

	if ts.max%65535 > 48000 && pkt.SequenceNumber < 16000 {
		ts.resets++
	}

	expected := ts.max + 1
	sequence := int64(pkt.SequenceNumber) + ts.resets*65536
	// correct for when sequence just reset and then a high sequence number came late
	if sequence > expected+32000 {
		sequence -= 65536
	}

	if sequence != expected && ts.packets > 0 {
		if ts.missing[sequence] {
			delete(ts.missing, sequence)
			ts.ooo++
		} else {
			for i := expected; i <= sequence; i++ {
				ts.missing[i] = true
			}
		}
	}
	ts.packets++
	if sequence > ts.max {
		ts.max = sequence
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
			missing: make(map[int64]bool),
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
	}

	return
}

func formatFloat(num int64, total int64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(num)/float64(total)*100), "0"), ".")
}
