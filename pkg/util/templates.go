// Copyright 2025 LiveKit, Inc.
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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

func ExpandTemplate(template string) string {
	now := time.Now().UTC()

	replacements := map[string]string{
		"%t": now.Format("20060102150405"), // compact timestamp
		"%T": now.Format(time.RFC3339),     // ISO 8601
		"%Y": now.Format("2006"),
		"%m": now.Format("01"),
		"%d": now.Format("02"),
		"%H": now.Format("15"),
		"%M": now.Format("04"),
		"%S": now.Format("05"),
		"%x": randomHex(6),
		"%U": os.Getenv("USER"),
		"%h": hostname(),
		"%p": fmt.Sprintf("%d", os.Getpid()),
	}

	// Simple replacer (no escaping)
	replacer := strings.NewReplacer(flattenMap(replacements)...)
	return replacer.Replace(template)
}

func flattenMap(m map[string]string) []string {
	out := make([]string, 0, len(m)*2)
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "xxxxxx"
	}
	return hex.EncodeToString(b)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
