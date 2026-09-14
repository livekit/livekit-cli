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

package util

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dash returns s, or "-" when s is empty or only whitespace. It is the value
// counterpart of DashString (which takes a *string).
func Dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// FormatRFC3339 renders t in RFC 3339, or the zero placeholder when t is the
// zero time.
func FormatRFC3339(t time.Time, zero string) string {
	if t.IsZero() {
		return zero
	}
	return t.Format(time.RFC3339)
}

// RawJSONToString renders a raw JSON scalar (number or string) for display,
// using a dash for an absent value.
func RawJSONToString(value json.RawMessage) string {
	if len(value) == 0 {
		return "-"
	}
	var numeric json.Number
	if err := json.Unmarshal(value, &numeric); err == nil {
		return numeric.String()
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return Dash(text)
	}
	return Dash(string(value))
}

// FormatBytes renders a raw JSON byte count as a human-readable size (SI units),
// passing through non-numeric values unchanged.
func FormatBytes(value json.RawMessage) string {
	raw := RawJSONToString(value)
	bytes, err := strconv.ParseFloat(raw, 64)
	if err != nil || bytes < 0 {
		return raw
	}
	if bytes < 1000 {
		return fmt.Sprintf("%.0f B", bytes)
	}
	units := "KMGTPE"
	unitIndex := 0
	size := bytes / 1000
	for size >= 1000 && unitIndex < len(units)-1 {
		size /= 1000
		unitIndex++
	}
	return fmt.Sprintf("%.1f %cB", size, units[unitIndex])
}
