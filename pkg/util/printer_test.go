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
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrinter_StreamsAndQuiet(t *testing.T) {
	var out, err bytes.Buffer
	p := NewPrinter(&out, &err, false)

	p.Result("data")
	p.Resultf("%d items\n", 3)
	p.Status("doing thing")
	p.Statusf("using %s", "x")
	p.Warnf("permissions look off: %o", 0644)

	assert.Equal(t, "data\n3 items\n", out.String(), "Result* writes only to stdout")
	assert.Equal(t,
		"doing thing\nusing x\npermissions look off: 644\n",
		err.String(),
		"Status* and Warnf write only to stderr, with trailing newlines",
	)
}

func TestPrinter_QuietSuppressesOnlyStatus(t *testing.T) {
	var out, err bytes.Buffer
	p := NewPrinter(&out, &err, true)

	p.Status("breadcrumb")
	p.Statusf("formatted %s", "breadcrumb")
	p.Warnf("warn %d", 1)
	p.Resultf("result\n")

	assert.NotContains(t, err.String(), "breadcrumb", "--quiet suppresses Status")
	assert.Contains(t, err.String(), "warn 1", "--quiet does NOT suppress warnings")
	assert.Equal(t, "result\n", out.String(), "results unaffected by --quiet")
}

func TestPrinter_NilSafe(t *testing.T) {
	var p *Printer // calling on a nil receiver should be a no-op
	p.Status("x")
	p.Statusf("y %d", 1)
	p.Warnf("z")
	p.Result("a")
	p.Resultf("b\n")

	// Await on a nil Printer still runs the action.
	ran := false
	require.NoError(t, p.Await("doing", context.Background(), func(context.Context) error {
		ran = true
		return nil
	}))
	assert.True(t, ran)
}

func TestPrinter_AwaitNonInteractive(t *testing.T) {
	// A buffer-backed Printer is never a terminal, so Await must not animate: it runs the
	// action and emits the title once as a plain status line (suppressed by --quiet).
	t.Run("not quiet: title shown, action runs", func(t *testing.T) {
		var out, err bytes.Buffer
		p := NewPrinter(&out, &err, false)
		ran := false
		require.NoError(t, p.Await("Loading...", context.Background(), func(context.Context) error {
			ran = true
			return nil
		}))
		assert.True(t, ran)
		assert.Equal(t, "Loading...\n", err.String())
		assert.Empty(t, out.String())
	})

	t.Run("quiet: silent, action still runs", func(t *testing.T) {
		var out, err bytes.Buffer
		p := NewPrinter(&out, &err, true)
		ran := false
		require.NoError(t, p.Await("Loading...", context.Background(), func(context.Context) error {
			ran = true
			return nil
		}))
		assert.True(t, ran)
		assert.Empty(t, err.String())
	})

	t.Run("propagates the action error", func(t *testing.T) {
		p := NewPrinter(io.Discard, io.Discard, false)
		sentinel := errors.New("boom")
		assert.ErrorIs(t, p.Await("x", context.Background(), func(context.Context) error {
			return sentinel
		}), sentinel)
	})
}
