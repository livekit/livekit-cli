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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/livekit"
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

// The Public API refuses a call placed with waitUntilAnswered that rings
// longer than 75s, so under --user-auth lk lowers the 80s --timeout default
// to 75s and refuses a longer --timeout before any request.
func TestSIPWaitTimeout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flag     time.Duration
		set      bool
		userAuth bool
		want     time.Duration
		wantErr  bool
	}{
		{name: "api key default", flag: 80 * time.Second, want: 80 * time.Second},
		{name: "api key long", flag: 2 * time.Minute, set: true, want: 2 * time.Minute},
		{name: "zero", flag: 0, set: true, want: 30 * time.Second},
		{name: "user auth default", flag: 80 * time.Second, userAuth: true, want: 75 * time.Second},
		{name: "user auth short", flag: 20 * time.Second, set: true, userAuth: true, want: 20 * time.Second},
		{name: "user auth at limit", flag: 75 * time.Second, set: true, userAuth: true, want: 75 * time.Second},
		{name: "user auth long", flag: 76 * time.Second, set: true, userAuth: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sipWaitTimeout(tc.flag, tc.set, tc.userAuth)
			if tc.wantErr {
				require.EqualError(t, err, "under --user-auth, --wait waits at most 1m15s for an answer: set --timeout to 1m15s or less")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// fakeSIPGets answers the get-by-ID calls of a sipAPI as server-sdk-go's
// client does with an API key: a nil slot for an ID the SIP API doesn't have.
type fakeSIPGets struct {
	sipAPI
	in   *livekit.SIPInboundTrunkInfo
	out  *livekit.SIPOutboundTrunkInfo
	rule *livekit.SIPDispatchRuleInfo
	err  error
}

func (f *fakeSIPGets) GetSIPInboundTrunksByIDs(context.Context, []string) ([]*livekit.SIPInboundTrunkInfo, error) {
	return []*livekit.SIPInboundTrunkInfo{f.in}, f.err
}

func (f *fakeSIPGets) GetSIPOutboundTrunksByIDs(context.Context, []string) ([]*livekit.SIPOutboundTrunkInfo, error) {
	return []*livekit.SIPOutboundTrunkInfo{f.out}, f.err
}

func (f *fakeSIPGets) GetSIPDispatchRulesByIDs(context.Context, []string) ([]*livekit.SIPDispatchRuleInfo, error) {
	return []*livekit.SIPDispatchRuleInfo{f.rule}, f.err
}

// The get commands never print a trunk's password, whatever the SIP API
// answers with, and name what they didn't find as the Public API does.
func TestSIPGetByID(t *testing.T) {
	ctx := context.Background()
	cli := &fakeSIPGets{
		in:   &livekit.SIPInboundTrunkInfo{SipTrunkId: "ST_in", AuthUsername: "u", AuthPassword: "secret"},
		out:  &livekit.SIPOutboundTrunkInfo{SipTrunkId: "ST_out", AuthUsername: "u", AuthPassword: "secret"},
		rule: &livekit.SIPDispatchRuleInfo{SipDispatchRuleId: "SDR_1"},
	}
	in, err := sipInboundTrunkByID(ctx, cli, "ST_in")
	require.NoError(t, err)
	require.Equal(t, "ST_in", in.SipTrunkId)
	require.Equal(t, "********", in.AuthPassword)
	out, err := sipOutboundTrunkByID(ctx, cli, "ST_out")
	require.NoError(t, err)
	require.Equal(t, "********", out.AuthPassword)
	rule, err := sipDispatchRuleByID(ctx, cli, "SDR_1")
	require.NoError(t, err)
	require.Equal(t, "SDR_1", rule.SipDispatchRuleId)

	// A trunk with no password keeps none.
	cli.in = &livekit.SIPInboundTrunkInfo{SipTrunkId: "ST_open"}
	in, err = sipInboundTrunkByID(ctx, cli, "ST_open")
	require.NoError(t, err)
	require.Empty(t, in.AuthPassword)

	none := &fakeSIPGets{}
	_, err = sipInboundTrunkByID(ctx, none, "ST_x")
	require.EqualError(t, err, "inbound trunk not found")
	_, err = sipOutboundTrunkByID(ctx, none, "ST_x")
	require.EqualError(t, err, "outbound trunk not found")
	_, err = sipDispatchRuleByID(ctx, none, "SDR_x")
	require.EqualError(t, err, "dispatch rule not found")

	failing := &fakeSIPGets{err: errors.New("twirp error unavailable")}
	_, err = sipInboundTrunkByID(ctx, failing, "ST_x")
	require.EqualError(t, err, "twirp error unavailable")
}
