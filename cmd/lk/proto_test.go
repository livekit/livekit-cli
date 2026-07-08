// Copyright 2024 LiveKit, Inc.
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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withStdin replaces os.Stdin with a pipe carrying the given content for the
// duration of the test, restoring the original afterwards.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	_, err = w.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		_ = r.Close()
	})
}

func TestReadFileOrLiteral(t *testing.T) {
	t.Run("literal", func(t *testing.T) {
		b, err := readFileOrLiteral(`{"a":1}`)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(b))
	})

	t.Run("file path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "input.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"from":"file"}`), 0o600))

		b, err := readFileOrLiteral(path)
		require.NoError(t, err)
		assert.JSONEq(t, `{"from":"file"}`, string(b))
	})

	t.Run("stdin", func(t *testing.T) {
		withStdin(t, `{"from":"stdin"}`)

		b, err := readFileOrLiteral("-")
		require.NoError(t, err)
		assert.JSONEq(t, `{"from":"stdin"}`, string(b))
	})

	t.Run("missing file falls back to literal", func(t *testing.T) {
		// A path that does not exist is treated as a literal value, not an error.
		b, err := readFileOrLiteral("/no/such/file/here.json")
		require.NoError(t, err)
		assert.Equal(t, "/no/such/file/here.json", string(b))
	})
}

func TestReadJSONFileOrLiteral(t *testing.T) {
	t.Run("raw literal", func(t *testing.T) {
		raw, err := ReadJSONFileOrLiteral(`{"a":1,"b":"two"}`)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1,"b":"two"}`, string(raw))
	})

	t.Run("raw from file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "input.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"a":1}`), 0o600))

		raw, err := ReadJSONFileOrLiteral(path)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(raw))
	})

	t.Run("raw from stdin", func(t *testing.T) {
		withStdin(t, `{"a":1}`)

		raw, err := ReadJSONFileOrLiteral("-")
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(raw))
	})

	t.Run("invalid JSON without target", func(t *testing.T) {
		_, err := ReadJSONFileOrLiteral(`not json`)
		require.Error(t, err)
	})

	t.Run("decode into target", func(t *testing.T) {
		type shape struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		var got shape
		raw, err := ReadJSONFileOrLiteral(`{"name":"widget","count":3}`, &got)
		require.NoError(t, err)

		assert.Equal(t, shape{Name: "widget", Count: 3}, got)
		// The raw bytes are still returned alongside the decoded value.
		assert.JSONEq(t, `{"name":"widget","count":3}`, string(raw))
	})

	t.Run("decode error surfaces", func(t *testing.T) {
		var got struct {
			Count int `json:"count"`
		}
		_, err := ReadJSONFileOrLiteral(`{"count":"not-a-number"}`, &got)
		require.Error(t, err)
	})

	t.Run("nil target falls back to validation", func(t *testing.T) {
		// An explicit nil target behaves like no target: raw bytes, validated.
		var target any
		raw, err := ReadJSONFileOrLiteral(`{"a":1}`, target)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(raw))
	})

	t.Run("returns json.RawMessage", func(t *testing.T) {
		raw, err := ReadJSONFileOrLiteral(`[1,2,3]`)
		require.NoError(t, err)

		// The result round-trips as a json.RawMessage.
		var nums []int
		require.NoError(t, json.Unmarshal(raw, &nums))
		assert.Equal(t, []int{1, 2, 3}, nums)
	})
}
