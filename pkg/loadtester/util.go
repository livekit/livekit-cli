// Copyright 2021-2023 LiveKit, Inc.
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

package loadtester

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func formatLossRate(packets, dropped int64) (sDropped string) {
	sDropped = " - "

	if packets > 0 {
		totalPackets := packets + dropped
		sDropped = fmt.Sprintf("%d (%s%%)", dropped, formatPercentage(dropped, totalPackets))
	}

	return
}

func formatPercentage(num int64, total int64) string {
	if total == 0 {
		return "0"
	}
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
