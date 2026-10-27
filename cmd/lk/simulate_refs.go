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

// The summarization model cites the conversation turns behind a finding with a
// <ref job="..." item="..."> tag in one of three spellings: wrapping quoted
// text and closed by </ref>, as a marker after the claim with no closing tag,
// or self-closed. Attribute order, quoting and casing are not guaranteed, so
// the tag is matched loosely and the attributes are extracted separately.
var (
	summaryRefOpenPattern  = regexp.MustCompile(`(?i)<ref\b([^>]*)>`)
	summaryRefClosePattern = regexp.MustCompile(`(?i)</ref\s*>`)
	summaryRefAttrPattern  = regexp.MustCompile(`([a-zA-Z]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'/>]+))`)
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
// nothing openable and degrades to the quoted text alone. A marker quotes
// nothing, so its number alone carries the link.
func linkSummaryRefs(text, projectID, runID string, refs *summaryRefIndex) string {
	return replaceSummaryRefs(text, func(attrs map[string]string, label string) string {
		if attrs["job"] == "" {
			return label
		}
		n, ok := refs.add(attrs)
		if !ok {
			return label
		}
		url := simulationItemDashboardURL(projectID, runID, attrs["job"], attrs["item"])
		if label == "" {
			number := dimStyle.Render(fmt.Sprintf("[%d]", n))
			if url != "" {
				number = util.Hyperlink(url, number)
			}
			return number
		}
		rendered := summaryRefStyle().Render(label)
		if url != "" {
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

// replaceSummaryRefs rewrites every <ref> in text through render, which gets
// an empty label for a marker, and drops any </ref> that closes no ref.
// Citations are often appended to a sentence with no separator, either
// directly after the full stop, back-to-back with each other, or right before
// the next word, so a ref that abuts a word gains a space on that side;
// without one the quotes run together into a single unreadable phrase. A ref
// that renders as nothing takes the space before it along unless a word
// follows, so the punctuation after it stays attached to the claim.
func replaceSummaryRefs(text string, render func(attrs map[string]string, label string) string) string {
	var b strings.Builder
	end := 0
	for _, m := range summaryRefOpenPattern.FindAllStringSubmatchIndex(text, -1) {
		attrs := make(map[string]string)
		for _, attr := range summaryRefAttrPattern.FindAllStringSubmatch(text[m[2]:m[3]], -1) {
			attrs[strings.ToLower(attr[1])] = attr[2] + attr[3] + attr[4]
		}
		label, refEnd := summaryRefBody(text, text[m[2]:m[3]], m[1])
		rendered := render(attrs, label)
		wordFollows := startsWithWord(text[refEnd:])
		prose := summaryRefClosePattern.ReplaceAllString(text[end:m[0]], "")
		if rendered == "" && !wordFollows {
			prose = strings.TrimRight(prose, " \t")
		} else if m[0] > 0 && !endsWithSpace(text[:m[0]]) {
			prose += " "
		}
		b.WriteString(prose)
		b.WriteString(rendered)
		if rendered != "" && wordFollows {
			b.WriteString(" ")
		}
		end = refEnd
	}
	b.WriteString(summaryRefClosePattern.ReplaceAllString(text[end:], ""))
	return b.String()
}

// summaryRefBody returns the text wrapped by the ref whose open tag ends at
// start, and where the ref ends. A </ref> closes it only if no other <ref>
// opens first; otherwise, or when the tag closes itself, the ref is a marker
// and wraps nothing.
func summaryRefBody(text, attrs string, start int) (string, int) {
	if strings.HasSuffix(strings.TrimSpace(attrs), "/") {
		return "", start
	}
	rest := text[start:]
	closeTag := summaryRefClosePattern.FindStringIndex(rest)
	if closeTag == nil {
		return "", start
	}
	if open := summaryRefOpenPattern.FindStringIndex(rest); open != nil && open[0] < closeTag[0] {
		return "", start
	}
	return rest[:closeTag[0]], start + closeTag[1]
}

func endsWithSpace(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func startsWithWord(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
