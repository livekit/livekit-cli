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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderPageJSON checks every paged listing prints the Public API's
// pageInfo, empty on the last page as the API sends it.
func TestRenderPageJSON(t *testing.T) {
	row := func(s string) []string { return []string{s} }

	var stdout bytes.Buffer
	require.NoError(t, RenderPage(NewPrinter(&stdout, nil, true), true, []string{"a"}, "c2", "none", []string{"X"}, row))
	assert.JSONEq(t, `{"items":["a"],"pageInfo":{"nextCursor":"c2","hasMore":true}}`, stdout.String())

	stdout.Reset()
	require.NoError(t, RenderPage[string](NewPrinter(&stdout, nil, true), true, nil, "", "none", []string{"X"}, row))
	assert.JSONEq(t, `{"items":[],"pageInfo":{}}`, stdout.String())
}

func TestRenderAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, RenderAction(NewPrinter(&stdout, &stderr, false), true, "id", "p_1", "deleted", "Deleted project p_1"))
	assert.JSONEq(t, `{"id":"p_1","deleted":true}`, stdout.String())
	assert.Empty(t, stderr.String())

	stdout.Reset()
	require.NoError(t, RenderAction(NewPrinter(&stdout, &stderr, false), false, "id", "p_1", "deleted", "Deleted project p_1"))
	assert.Empty(t, stdout.String())
	assert.Equal(t, "Deleted project p_1\n", stderr.String())
}
