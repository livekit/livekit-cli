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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const refProse = `it kept asking for details in <ref job="SRJ_Bzb9ZaoJFJyp" item="item_dd0ee81187bd">"I've had a few, sure"</ref> and <ref item="item_13b90227fe38" job="SRJ_Bzb9ZaoJFJyp">"I'm totally fine to drive"</ref>.`

func TestStripSummaryRefs(t *testing.T) {
	require.Equal(t,
		`it kept asking for details in "I've had a few, sure" and "I'm totally fine to drive".`,
		stripSummaryRefs(refProse),
	)

	// footnote-style citations: appended to a sentence and to each other
	require.Equal(t,
		"left out the passport requirement. accepted cards exchange rate posting",
		stripSummaryRefs(`left out the passport requirement.<ref job="J" item="i1">accepted cards</ref><ref job="J" item="i2">exchange rate posting</ref>`),
	)

	// prose without refs, and a ref spanning a newline
	require.Equal(t, "nothing to strip", stripSummaryRefs("nothing to strip"))
	require.Equal(t, "a\nquote", stripSummaryRefs("<ref job=\"J\" item=\"I\">a\nquote</ref>"))
}

func TestLinkSummaryRefs(t *testing.T) {
	linked := linkSummaryRefs(refProse, "proj", "run")

	require.NotContains(t, linked, "<ref")
	require.NotContains(t, linked, "</ref>")
	// both refs link to their own item, whatever the attribute order
	require.Contains(t, linked, "runs/run?job=SRJ_Bzb9ZaoJFJyp&item=item_dd0ee81187bd")
	require.Contains(t, linked, "runs/run?job=SRJ_Bzb9ZaoJFJyp&item=item_13b90227fe38")
	require.Contains(t, linked, `"I've had a few, sure"`)
	require.Equal(t, 2, strings.Count(linked, "\x1b]8;;"+dashboardBaseURL()))
}

func TestLinkSummaryRefsWithoutTarget(t *testing.T) {
	// no project or run to link to, and a ref with no job: quoted text only
	require.Equal(t, stripSummaryRefs(refProse), linkSummaryRefs(refProse, "", ""))
	require.Equal(t, "quoted", linkSummaryRefs(`<ref item="item_x">quoted</ref>`, "proj", "run"))
}
