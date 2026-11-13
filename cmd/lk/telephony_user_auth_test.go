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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPhoneNumberRef(t *testing.T) {
	require.Equal(t, "PN_1", phoneNumberRef("PN_1", ""))
	require.Equal(t, "+15550100", phoneNumberRef("", "+15550100"))
}

// A failure partway through several purchases or releases names the numbers
// that the earlier calls already changed, so the user knows what happened.
func TestPartialError(t *testing.T) {
	cause := errors.New("phone number not found")
	err := partialError("released", nil, "PN_1", cause)
	require.EqualError(t, err, "PN_1: phone number not found")
	require.ErrorIs(t, err, cause)

	err = partialError("purchased", []string{"+15550100", "+15550101"}, "+15550102", cause)
	require.EqualError(t, err, "purchased +15550100, +15550101, then +15550102 failed: phone number not found")
	require.ErrorIs(t, err, cause)
}
