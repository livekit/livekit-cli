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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `lk agent session start` runs the agent in a detached daemon process, so
// runtime args forwarded after "--" cross the process boundary through the
// LK_SESSION_FWD env var: sessionFwdEnv encodes them on the start side and
// sessionFwdArgs decodes them on the daemon side.
func TestSessionForwardedArgsEnvRoundTrip(t *testing.T) {
	t.Run("forwarded args reach the daemon verbatim", func(t *testing.T) {
		fwd := []string{"--env-file=.env", `--title=a "quoted" value`, "-X", "utf8=1"}

		entry := sessionFwdEnv(fwd)
		name, value, ok := strings.Cut(entry, "=")
		require.True(t, ok)
		assert.Equal(t, envSessionFwd, name)

		decoded, err := sessionFwdArgs(value)
		require.NoError(t, err)
		assert.Equal(t, fwd, decoded)
	})

	t.Run("nothing forwarded sets no env var", func(t *testing.T) {
		assert.Equal(t, "", sessionFwdEnv(nil))

		// The daemon then sees an unset env var and runs without runtime args.
		decoded, err := sessionFwdArgs("")
		require.NoError(t, err)
		assert.Nil(t, decoded)
	})

	t.Run("corrupted env value fails instead of dropping args", func(t *testing.T) {
		_, err := sessionFwdArgs("not json")
		assert.ErrorContains(t, err, envSessionFwd)
	})
}
