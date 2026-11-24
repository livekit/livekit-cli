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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
)

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

// A get by ID returns the item the SIP API found, and names what it didn't
// find as the Public API does.
func TestGetOne(t *testing.T) {
	ctx := context.Background()
	cli := &fakeSIPGets{
		in:   &livekit.SIPInboundTrunkInfo{SipTrunkId: "ST_in"},
		out:  &livekit.SIPOutboundTrunkInfo{SipTrunkId: "ST_out"},
		rule: &livekit.SIPDispatchRuleInfo{SipDispatchRuleId: "SDR_1"},
	}
	in, err := getOne(ctx, "ST_in", "inbound trunk", cli.GetSIPInboundTrunksByIDs)
	require.NoError(t, err)
	require.Equal(t, "ST_in", in.SipTrunkId)
	out, err := getOne(ctx, "ST_out", "outbound trunk", cli.GetSIPOutboundTrunksByIDs)
	require.NoError(t, err)
	require.Equal(t, "ST_out", out.SipTrunkId)
	rule, err := getOne(ctx, "SDR_1", "dispatch rule", cli.GetSIPDispatchRulesByIDs)
	require.NoError(t, err)
	require.Equal(t, "SDR_1", rule.SipDispatchRuleId)

	none := &fakeSIPGets{}
	_, err = getOne(ctx, "ST_x", "inbound trunk", none.GetSIPInboundTrunksByIDs)
	require.EqualError(t, err, "inbound trunk not found")
	_, err = getOne(ctx, "SDR_x", "dispatch rule", none.GetSIPDispatchRulesByIDs)
	require.EqualError(t, err, "dispatch rule not found")

	failing := &fakeSIPGets{err: errors.New("twirp error unavailable")}
	_, err = getOne(ctx, "ST_x", "inbound trunk", failing.GetSIPInboundTrunksByIDs)
	require.EqualError(t, err, "twirp error unavailable")
}

// Under --user-auth, a SIP list by IDs gets each ID, in order, since the
// Public API's list has no ID filter. A list with IDs and another filter is
// refused before any request, since a get can't apply that filter.
func TestPublicSIPListByIDs(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		switch {
		case strings.Contains(r.URL.Path, "/inbound-trunks/"), strings.Contains(r.URL.Path, "/outbound-trunks/"):
			_, _ = fmt.Fprintf(w, `{"trunk":{"sipTrunkId":%q}}`, id)
		case strings.Contains(r.URL.Path, "/dispatch-rules/"):
			_, _ = fmt.Fprintf(w, `{"dispatchRule":{"sipDispatchRuleId":%q}}`, id)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := public.New(srv.URL, "tok")
	require.NoError(t, err)
	p := &publicSIP{client: client, projectID: "p_1"}
	ctx := context.Background()

	in, err := p.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{TrunkIds: []string{"ST_2", "ST_1"}})
	require.NoError(t, err)
	require.Equal(t, []string{"ST_2", "ST_1"}, []string{in.Items[0].SipTrunkId, in.Items[1].SipTrunkId})
	out, err := p.ListSIPOutboundTrunk(ctx, &livekit.ListSIPOutboundTrunkRequest{TrunkIds: []string{"ST_3"}})
	require.NoError(t, err)
	require.Equal(t, "ST_3", out.Items[0].SipTrunkId)
	rules, err := p.ListSIPDispatchRule(ctx, &livekit.ListSIPDispatchRuleRequest{DispatchRuleIds: []string{"SDR_1"}})
	require.NoError(t, err)
	require.Equal(t, "SDR_1", rules.Items[0].SipDispatchRuleId)
	require.Equal(t, []string{
		"/v0/projects/p_1/sip/inbound-trunks/ST_2",
		"/v0/projects/p_1/sip/inbound-trunks/ST_1",
		"/v0/projects/p_1/sip/outbound-trunks/ST_3",
		"/v0/projects/p_1/sip/dispatch-rules/SDR_1",
	}, paths)

	paths = nil
	const both = "under --user-auth, a SIP list takes IDs or a filter, not both"
	_, err = p.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{TrunkIds: []string{"ST_1"}, Numbers: []string{"+15550100"}})
	require.EqualError(t, err, both)
	_, err = p.ListSIPOutboundTrunk(ctx, &livekit.ListSIPOutboundTrunkRequest{TrunkIds: []string{"ST_1"}, Numbers: []string{"+15550100"}})
	require.EqualError(t, err, both)
	_, err = p.ListSIPDispatchRule(ctx, &livekit.ListSIPDispatchRuleRequest{DispatchRuleIds: []string{"SDR_1"}, TrunkIds: []string{"ST_1"}})
	require.EqualError(t, err, both)
	require.Empty(t, paths)
}
