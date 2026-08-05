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

package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// The summarization model cites the conversation turns behind a finding with
// <ref job="..." item="...">quoted text</ref>. Attribute order is not
// guaranteed, so the tag is matched loosely and the attributes are extracted
// separately.
var (
	summaryRefPattern     = regexp.MustCompile(`(?s)<ref\s([^>]*)>(.*?)</ref>`)
	summaryRefAttrPattern = regexp.MustCompile(`([a-zA-Z]+)\s*=\s*"([^"]*)"`)
)

// summaryRefStyle marks cited text as a link, for terminals that render OSC 8
// hyperlinks no differently from surrounding text.
func summaryRefStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(util.Brand()).Underline(true)
}

// linkSummaryRefs replaces each <ref> in summary prose with its quoted text as
// a clickable link to the cited chat item. A ref missing a job, or a run with
// no dashboard URL, degrades to the quoted text alone.
func linkSummaryRefs(text, projectID, runID string) string {
	return replaceSummaryRefs(text, func(attrs map[string]string, label string) string {
		url := simulationItemDashboardURL(projectID, runID, attrs["job"], attrs["item"])
		if url == "" {
			return label
		}
		return util.Hyperlink(url, summaryRefStyle().Render(label))
	})
}

// stripSummaryRefs reduces each <ref> in summary prose to its quoted text, for
// output that cannot carry a link (files, CI logs, redirected stdout).
func stripSummaryRefs(text string) string {
	return replaceSummaryRefs(text, func(_ map[string]string, label string) string {
		return label
	})
}

// replaceSummaryRefs rewrites every <ref> in text through render. Citations are
// often appended to a sentence with no separator, either directly after the
// full stop or back-to-back with each other, so a ref that abuts the text
// before it gains a leading space; without one the quotes run together into a
// single unreadable phrase.
func replaceSummaryRefs(text string, render func(attrs map[string]string, label string) string) string {
	var b strings.Builder
	end := 0
	for _, m := range summaryRefPattern.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[end:m[0]])
		if m[0] > 0 && !endsWithSpace(text[:m[0]]) {
			b.WriteString(" ")
		}
		attrs := make(map[string]string)
		for _, attr := range summaryRefAttrPattern.FindAllStringSubmatch(text[m[2]:m[3]], -1) {
			attrs[strings.ToLower(attr[1])] = attr[2]
		}
		b.WriteString(render(attrs, text[m[4]:m[5]]))
		end = m[1]
	}
	b.WriteString(text[end:])
	return b.String()
}

func endsWithSpace(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}
