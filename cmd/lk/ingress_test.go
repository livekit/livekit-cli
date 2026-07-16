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

// fakeIngressService implements livekit.Ingress. Only ListIngress is exercised
// by the ingress list tests; the other RPCs return empty results.
type fakeIngressService struct {
	listRequests  []*livekit.ListIngressRequest
	listResponses []*livekit.ListIngressResponse
	listErr       error
}

func (f *fakeIngressService) ListIngress(_ context.Context, req *livekit.ListIngressRequest) (*livekit.ListIngressResponse, error) {
	f.listRequests = append(f.listRequests, req)
	if f.listErr != nil {
		return nil, f.listErr
	}
	idx := len(f.listRequests) - 1
	if idx >= len(f.listResponses) {
		return &livekit.ListIngressResponse{}, nil
	}
	return f.listResponses[idx], nil
}

func (f *fakeIngressService) CreateIngress(_ context.Context, _ *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	return nil, nil
}
func (f *fakeIngressService) UpdateIngress(_ context.Context, _ *livekit.UpdateIngressRequest) (*livekit.IngressInfo, error) {
	return nil, nil
}
func (f *fakeIngressService) DeleteIngress(_ context.Context, _ *livekit.DeleteIngressRequest) (*livekit.IngressInfo, error) {
	return nil, nil
}

// setupFakeIngressClient stands up an in-process twirp server backed by svc and
// rewires the package-level ingressClient to point at it. The previous client is
// restored on cleanup.
func setupFakeIngressClient(t *testing.T, svc livekit.Ingress) {
	t.Helper()
	server := httptest.NewServer(livekit.NewIngressServer(svc))
	prev := ingressClient
	ingressClient = lksdk.NewIngressClient(server.URL, "APIkey", "secret")
	t.Cleanup(func() {
		server.Close()
		ingressClient = prev
	})
}

// buildListIngressCommand parses the given flag values through a urfave/cli
// command that mirrors `ingress list`, and returns the parsed *cli.Command.
func buildListIngressCommand(t *testing.T, id string, room string, limit int) *cli.Command {
	t.Helper()
	cmd, _ := buildListIngressCommandJSON(t, id, room, limit, false)
	return cmd
}

// buildListIngressCommandJSON returns the parsed *cli.Command together with a
// buffer wired to the command's Writer so tests can read what `listIngress`
// would have printed.
func buildListIngressCommandJSON(t *testing.T, id string, room string, limit int, asJSON bool) (*cli.Command, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	var captured *cli.Command
	app := &cli.Command{
		Name:   "test",
		Writer: buf,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "id"},
			&cli.StringFlag{Name: "room"},
			&cli.IntFlag{Name: "limit"},
			&cli.BoolFlag{Name: "json"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			captured = cmd
			return nil
		},
	}

	args := []string{"test"}
	if id != "" {
		args = append(args, "--id", id)
	}
	if room != "" {
		args = append(args, "--room", room)
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

// extractIngressIDs decodes JSON produced by `listIngress --json` and returns
// the ingressId values in the order they appear in the response's items array.
func extractIngressIDs(t *testing.T, out []byte) []string {
	t.Helper()
	var decoded struct {
		Items []struct {
			IngressID string `json:"ingressId"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	ids := make([]string, len(decoded.Items))
	for i, d := range decoded.Items {
		ids[i] = d.IngressID
	}
	return ids
}

func TestListIngress_ByID(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{Items: []*livekit.IngressInfo{{IngressId: "IN_1"}}},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "IN_1", "", 0)
	require.NoError(t, listIngress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Equal(t, "IN_1", svc.listRequests[0].IngressId)
	assert.Empty(t, svc.listRequests[0].RoomName)
	assert.Nil(t, svc.listRequests[0].PageToken)
}

func TestListIngress_FiltersPassedThrough(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{Items: []*livekit.IngressInfo{{IngressId: "IN_1"}}},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "", "my-room", 0)
	require.NoError(t, listIngress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Equal(t, "my-room", svc.listRequests[0].RoomName)
	assert.Empty(t, svc.listRequests[0].IngressId)
	assert.Nil(t, svc.listRequests[0].PageToken)
}

func TestListIngress_Pagination_WalksUntilLimitReached(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{
				Items:         []*livekit.IngressInfo{{IngressId: "1"}, {IngressId: "2"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items:         []*livekit.IngressInfo{{IngressId: "3"}, {IngressId: "4"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
			{
				Items: []*livekit.IngressInfo{{IngressId: "5"}},
			},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "", "", 5)
	require.NoError(t, listIngress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 3)
	assert.Nil(t, svc.listRequests[0].PageToken)
	require.NotNil(t, svc.listRequests[1].PageToken)
	assert.Equal(t, "page-2", svc.listRequests[1].PageToken.Token)
	require.NotNil(t, svc.listRequests[2].PageToken)
	assert.Equal(t, "page-3", svc.listRequests[2].PageToken.Token)
}

func TestListIngress_Pagination_TruncatesOvershoot(t *testing.T) {
	// First page returns 2 items, second page returns 5 items but limit=4 so
	// only 2 items of page 2 should be kept and pagination should stop.
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{
				Items:         []*livekit.IngressInfo{{IngressId: "1"}, {IngressId: "2"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "3"}, {IngressId: "4"},
					{IngressId: "5"}, {IngressId: "6"}, {IngressId: "7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "", "", 4)
	require.NoError(t, listIngress(context.Background(), cmd))

	// Should have stopped after page 2 — never requested page-3.
	require.Len(t, svc.listRequests, 2)
}

func TestListIngress_Pagination_StopsWhenNoNextPageToken(t *testing.T) {
	// limit large enough not to bound; should stop because page 2 has no
	// NextPageToken.
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{
				Items:         []*livekit.IngressInfo{{IngressId: "1"}},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.IngressInfo{{IngressId: "2"}},
			},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "", "", 100)
	require.NoError(t, listIngress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 2)
}

func TestListIngress_SinglePage_NoLimit(t *testing.T) {
	// No --limit flag and a single page with no NextPageToken should produce
	// exactly one request and no pagination follow-up.
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{Items: []*livekit.IngressInfo{{IngressId: "1"}, {IngressId: "2"}}},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd := buildListIngressCommand(t, "", "", 0)
	require.NoError(t, listIngress(context.Background(), cmd))

	require.Len(t, svc.listRequests, 1)
	assert.Nil(t, svc.listRequests[0].PageToken)
}

// TestListIngress_JSONOrdering_AcrossPages verifies the chronological order of
// the emitted items when multiple pages are stitched together.
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
// Expected items: [n0, n1, n2, n3, n4, n5, n6, n7]
func TestListIngress_JSONOrdering_AcrossPages(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "n5"}, {IngressId: "n6"}, {IngressId: "n7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "n2"}, {IngressId: "n3"}, {IngressId: "n4"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "n0"}, {IngressId: "n1"},
				},
			},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd, buf := buildListIngressCommandJSON(t, "", "", 100, true)
	require.NoError(t, listIngress(context.Background(), cmd))

	ids := extractIngressIDs(t, buf.Bytes())
	assert.Equal(t, []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}, ids)
}

// TestListIngress_JSONOrdering_TruncatedOvershoot verifies that when a later
// (older) page overshoots the requested limit, the newest tail of that page
// is kept (since later pages are older, dropping the front drops the oldest
// items) and the final output remains in chronological oldest-first order.
//
// Page 1 = newest: [n5, n6, n7]
// Page 2 = older:  [n0, n1, n2, n3, n4] (5 items, only 2 slots remain)
// limit = 5, so page 2 contributes only its last 2 items: [n3, n4]
// Expected items: [n3, n4, n5, n6, n7]
func TestListIngress_JSONOrdering_TruncatedOvershoot(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "n5"}, {IngressId: "n6"}, {IngressId: "n7"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-2"},
			},
			{
				Items: []*livekit.IngressInfo{
					{IngressId: "n0"}, {IngressId: "n1"}, {IngressId: "n2"},
					{IngressId: "n3"}, {IngressId: "n4"},
				},
				NextPageToken: &livekit.TokenPagination{Token: "page-3"},
			},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd, buf := buildListIngressCommandJSON(t, "", "", 5, true)
	require.NoError(t, listIngress(context.Background(), cmd))

	ids := extractIngressIDs(t, buf.Bytes())
	assert.Equal(t, []string{"n3", "n4", "n5", "n6", "n7"}, ids)
	require.Len(t, svc.listRequests, 2)
}

// TestListIngress_JSONOrdering_SinglePage verifies that for a single-page
// response the output preserves the server's intra-page order verbatim.
func TestListIngress_JSONOrdering_SinglePage(t *testing.T) {
	svc := &fakeIngressService{
		listResponses: []*livekit.ListIngressResponse{
			{Items: []*livekit.IngressInfo{
				{IngressId: "a"}, {IngressId: "b"}, {IngressId: "c"},
			}},
		},
	}
	setupFakeIngressClient(t, svc)

	cmd, buf := buildListIngressCommandJSON(t, "", "", 0, true)
	require.NoError(t, listIngress(context.Background(), cmd))

	assert.Equal(t, []string{"a", "b", "c"}, extractIngressIDs(t, buf.Bytes()))
}
