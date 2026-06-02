// Copyright 2025 LiveKit, Inc.
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
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// fakeEgressService implements livekit.Egress. Only ListEgress is exercised
// by the egress list tests; the other RPCs return empty results.
type fakeEgressService struct {
	listRequests  []*livekit.ListEgressRequest
	listResponses []*livekit.ListEgressResponse
	listErr       error
}

func (f *fakeEgressService) ListEgress(_ context.Context, req *livekit.ListEgressRequest) (*livekit.ListEgressResponse, error) {
	f.listRequests = append(f.listRequests, req)
	if f.listErr != nil {
		return nil, f.listErr
	}
	idx := len(f.listRequests) - 1
	if idx >= len(f.listResponses) {
		return &livekit.ListEgressResponse{}, nil
	}
	return f.listResponses[idx], nil
}

func (f *fakeEgressService) StartRoomCompositeEgress(_ context.Context, _ *livekit.RoomCompositeEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) StartWebEgress(_ context.Context, _ *livekit.WebEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) StartParticipantEgress(_ context.Context, _ *livekit.ParticipantEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) StartTrackCompositeEgress(_ context.Context, _ *livekit.TrackCompositeEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) StartTrackEgress(_ context.Context, _ *livekit.TrackEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) UpdateLayout(_ context.Context, _ *livekit.UpdateLayoutRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) UpdateStream(_ context.Context, _ *livekit.UpdateStreamRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}
func (f *fakeEgressService) StopEgress(_ context.Context, _ *livekit.StopEgressRequest) (*livekit.EgressInfo, error) {
	return nil, nil
}

// setupFakeEgressClient stands up an in-process twirp server backed by svc and
// rewires the package-level egressClient to point at it. The previous client is
// restored on cleanup.
func setupFakeEgressClient(t *testing.T, svc livekit.Egress) {
	t.Helper()
	server := httptest.NewServer(livekit.NewEgressServer(svc))
	prev := egressClient
	egressClient = lksdk.NewEgressClient(server.URL, "APIkey", "secret")
	t.Cleanup(func() {
		server.Close()
		egressClient = prev
	})
}

// buildListEgressCommand parses the given flag values through a urfave/cli
// command that mirrors `egress list`, and returns the parsed *cli.Command.
func buildListEgressCommand(t *testing.T, ids []string, room string, active bool, limit int) *cli.Command {
	t.Helper()
	cmd, _ := buildListEgressCommandJSON(t, ids, room, active, limit, false)
	return cmd
}

// buildListEgressCommandJSON returns the parsed *cli.Command together with a
// buffer wired to the command's Writer so tests can read what `listEgress`
// would have printed.
func buildListEgressCommandJSON(t *testing.T, ids []string, room string, active bool, limit int, asJSON bool) (*cli.Command, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	var captured *cli.Command
	app := &cli.Command{
		Name:   "test",
		Writer: buf,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "id"},
			&cli.StringFlag{Name: "room"},
			&cli.BoolFlag{Name: "active"},
			&cli.IntFlag{Name: "limit"},
			&cli.BoolFlag{Name: "json"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			captured = cmd
			return nil
		},
	}

	args := []string{"test"}
	for _, id := range ids {
		args = append(args, "--id", id)
	}
	if room != "" {
		args = append(args, "--room", room)
	}
	if active {
		args = append(args, "--active")
	}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	if asJSON {
		args = append(args, "--json")
	}

	require.NoError(t, app.Run(context.Background(), args))
	require.NotNil(t, captured)
	return captured, buf
}

// extractEgressIDs decodes JSON produced by `listEgress --json` and returns
// the egress_id values in the order they appear in the output array.
func extractEgressIDs(t *testing.T, out []byte) []string {
	t.Helper()
	var decoded []struct {
		EgressID string `json:"egress_id"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	ids := make([]string, len(decoded))
	for i, d := range decoded {
		ids[i] = d.EgressID
	}
	return ids
}

func TestListEgress_ByID_Single(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{{EgressId: "EG_1"}}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, []string{"EG_1"}, "", false, 0)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Equal(t, "EG_1", svc.listRequests[0].EgressId)
	assert.Empty(t, svc.listRequests[0].RoomName)
	assert.False(t, svc.listRequests[0].Active)
}

func TestListEgress_ByID_Multiple(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{{EgressId: "EG_A"}}},
			{Items: []*livekit.EgressInfo{{EgressId: "EG_B"}}},
			{Items: []*livekit.EgressInfo{{EgressId: "EG_C"}}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, []string{"EG_A", "EG_B", "EG_C"}, "", false, 0)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 3)
	assert.Equal(t, "EG_A", svc.listRequests[0].EgressId)
	assert.Equal(t, "EG_B", svc.listRequests[1].EgressId)
	assert.Equal(t, "EG_C", svc.listRequests[2].EgressId)
}

func TestListEgress_FiltersPassedThrough(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{{EgressId: "EG_1"}}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, nil, "my-room", true, 0)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Equal(t, "my-room", svc.listRequests[0].RoomName)
	assert.True(t, svc.listRequests[0].Active)
	assert.Empty(t, svc.listRequests[0].EgressId)
	assert.Nil(t, svc.listRequests[0].PageToken)
}

func TestListEgress_Pagination_WalksUntilLimitReached(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{
				Items:         []*livekit.EgressInfo{{EgressId: "1"}, {EgressId: "2"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items:         []*livekit.EgressInfo{{EgressId: "3"}, {EgressId: "4"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
			{
				Items: []*livekit.EgressInfo{{EgressId: "5"}},
			},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, nil, "", false, 5)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 3)
	assert.Nil(t, svc.listRequests[0].PageToken)
	require.NotNil(t, svc.listRequests[1].PageToken)
	assert.Equal(t, "page-2", svc.listRequests[1].PageToken.Token)
	require.NotNil(t, svc.listRequests[2].PageToken)
	assert.Equal(t, "page-3", svc.listRequests[2].PageToken.Token)
}

func TestListEgress_Pagination_TruncatesOvershoot(t *testing.T) {
	// First page returns 2 items, second page returns 5 items but limit=4 so
	// only the first 2 of page 2 should be kept and pagination should stop.
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{
				Items:         []*livekit.EgressInfo{{EgressId: "1"}, {EgressId: "2"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "3"}, {EgressId: "4"},
					{EgressId: "5"}, {EgressId: "6"}, {EgressId: "7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, nil, "", false, 4)
	require.NoError(t, listEgress(context.Background(), cmd))

	// Should have stopped after page 2 — never requested page-3.
	require.Len(t, svc.listRequests, 2)
}

func TestListEgress_Pagination_StopsWhenNoNextPageToken(t *testing.T) {
	// limit large enough not to bound; should stop because page 2 has no
	// NextPageToken.
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{
				Items:         []*livekit.EgressInfo{{EgressId: "1"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.EgressInfo{{EgressId: "2"}},
			},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, nil, "", false, 100)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 2)
}

func TestListEgress_SinglePage_NoLimit(t *testing.T) {
	// No --limit flag and a single page with no NextPageToken should produce
	// exactly one request and no pagination follow-up.
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{{EgressId: "1"}, {EgressId: "2"}}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd := buildListEgressCommand(t, nil, "", false, 0)
	require.NoError(t, listEgress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Nil(t, svc.listRequests[0].PageToken)
}

// TestListEgress_JSONOrdering_AcrossPages verifies the chronological order of
// the emitted JSON array when multiple pages are stitched together.
//
// The API contract is: each successive page contains *older* items than the
// previous page, and within a single page items are sorted newest-last
// (oldest-first within the page). The CLI prepends each fetched page to the
// accumulated list so the final output is the full corpus in oldest-first
// order across page boundaries.
//
// Page 1 = newest items: [n5, n6, n7]   (n7 = newest overall)
// Page 2 = older items:  [n2, n3, n4]
// Page 3 = oldest items: [n0, n1]
// Expected JSON: [n0, n1, n2, n3, n4, n5, n6, n7]
func TestListEgress_JSONOrdering_AcrossPages(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "n5"}, {EgressId: "n6"}, {EgressId: "n7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "n2"}, {EgressId: "n3"}, {EgressId: "n4"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "n0"}, {EgressId: "n1"},
				},
			},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd, buf := buildListEgressCommandJSON(t, nil, "", false, 100, true)
	require.NoError(t, listEgress(context.Background(), cmd))

	ids := extractEgressIDs(t, buf.Bytes())
	assert.Equal(t, []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}, ids)
}

// TestListEgress_JSONOrdering_TruncatedOvershoot verifies that when a later
// (older) page overshoots the requested limit, the newest tail of that page
// is kept (since later pages are older, dropping the front drops the oldest
// items) and the final JSON output remains in chronological oldest-first
// order.
//
// Page 1 = newest: [n5, n6, n7]
// Page 2 = older:  [n0, n1, n2, n3, n4] (5 items, only 2 slots remain)
// limit = 5, so page 2 contributes only its last 2 items: [n3, n4]
// Expected JSON: [n3, n4, n5, n6, n7]
func TestListEgress_JSONOrdering_TruncatedOvershoot(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "n5"}, {EgressId: "n6"}, {EgressId: "n7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.EgressInfo{
					{EgressId: "n0"}, {EgressId: "n1"}, {EgressId: "n2"},
					{EgressId: "n3"}, {EgressId: "n4"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd, buf := buildListEgressCommandJSON(t, nil, "", false, 5, true)
	require.NoError(t, listEgress(context.Background(), cmd))

	ids := extractEgressIDs(t, buf.Bytes())
	assert.Equal(t, []string{"n3", "n4", "n5", "n6", "n7"}, ids)
	require.Len(t, svc.listRequests, 2)
}

// TestListEgress_JSONOrdering_SinglePage verifies that for a single-page
// response the JSON output preserves the server's intra-page order verbatim.
func TestListEgress_JSONOrdering_SinglePage(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{
				{EgressId: "a"}, {EgressId: "b"}, {EgressId: "c"},
			}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd, buf := buildListEgressCommandJSON(t, nil, "", false, 0, true)
	require.NoError(t, listEgress(context.Background(), cmd))

	assert.Equal(t, []string{"a", "b", "c"}, extractEgressIDs(t, buf.Bytes()))
}

// TestListEgress_JSONOrdering_ByID verifies that --id queries preserve the
// order in which the IDs were supplied on the command line, since each ID is
// resolved with a separate ListEgress call and appended in turn.
func TestListEgress_JSONOrdering_ByID(t *testing.T) {
	svc := &fakeEgressService{
		listResponses: []*livekit.ListEgressResponse{
			{Items: []*livekit.EgressInfo{{EgressId: "EG_C"}}},
			{Items: []*livekit.EgressInfo{{EgressId: "EG_A"}}},
			{Items: []*livekit.EgressInfo{{EgressId: "EG_B"}}},
		},
	}
	setupFakeEgressClient(t, svc)

	cmd, buf := buildListEgressCommandJSON(t, []string{"EG_C", "EG_A", "EG_B"}, "", false, 0, true)
	require.NoError(t, listEgress(context.Background(), cmd))

	assert.Equal(t, []string{"EG_C", "EG_A", "EG_B"}, extractEgressIDs(t, buf.Bytes()))
}
