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
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

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

// A citation's number is an invitation to press that digit, so only as many
// citations as there are digits to press carry one.
const maxNumberedSummaryRefs = 9

// summaryRefTarget is the chat item a numbered citation points at.
type summaryRefTarget struct {
	job  string
	item string
}

// summaryRefIndex numbers citations as they are rendered. The number a reader
// sees has to select the same citation when pressed, so one index is threaded
// through every block of a summary and numbering follows render order.
type summaryRefIndex struct {
	targets []summaryRefTarget
}

// add records a citation and returns its 1-based number, or false once every
// digit is spoken for.
func (x *summaryRefIndex) add(attrs map[string]string) (int, bool) {
	if len(x.targets) >= maxNumberedSummaryRefs {
		return 0, false
	}
	x.targets = append(x.targets, summaryRefTarget{job: attrs["job"], item: attrs["item"]})
	return len(x.targets), true
}

// linkSummaryRefs replaces each <ref> in summary prose with its quoted text,
// numbered so the digit keys can open the cited turn, and hyperlinked to the
// cited item when the run has a dashboard URL. A ref naming no job cites
// nothing openable and degrades to the quoted text alone.
func linkSummaryRefs(text, projectID, runID string, refs *summaryRefIndex) string {
	return replaceSummaryRefs(text, func(attrs map[string]string, label string) string {
		if attrs["job"] == "" {
			return label
		}
		n, ok := refs.add(attrs)
		if !ok {
			return label
		}
		rendered := summaryRefStyle().Render(label)
		if url := simulationItemDashboardURL(projectID, runID, attrs["job"], attrs["item"]); url != "" {
			rendered = util.Hyperlink(url, rendered)
		}
		return rendered + dimStyle.Render(fmt.Sprintf(" [%d]", n))
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
