package loadtester

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func init() {
	rand.Seed(time.Now().UnixNano())
}

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func formatStrings(packets, dropped int64) (sDropped string) {
	sDropped = " - "

	if packets > 0 {
		totalPackets := packets + dropped
		sDropped = fmt.Sprintf("%d (%s%%)", dropped, formatPercentage(dropped, totalPackets))
	}

	return
}

func formatPercentage(num int64, total int64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", float64(num)/float64(total)*100), "0"), ".")
}

func formatBitrate(bytes int64, elapsed time.Duration) string {
	bps := float64(bytes*8) / elapsed.Seconds()
	if bps < 1000 {
		return fmt.Sprintf("%dbps", int(bps))
	} else if bps < 1000000 {
		return fmt.Sprintf("%.1fkbps", bps/1000)
	} else {
		return fmt.Sprintf("%.1fmbps", bps/1000000)
	}
}
