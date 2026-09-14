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

package public

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestResponseError(t *testing.T) {
	// gRPC-gateway envelope: {"code","message"} -> message.
	var apiErr *APIError
	err := responseError(http.StatusForbidden, []byte(`{"code":7,"message":"forbidden"}`))
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Status)
	assert.Equal(t, "forbidden", apiErr.Message)

	// Non-envelope body falls back to the raw text (with the status).
	err = responseError(http.StatusInternalServerError, []byte("boom"))
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Status)
	assert.Contains(t, apiErr.Message, "500")
	assert.Contains(t, apiErr.Message, "boom")

	// Empty body falls back to the HTTP status text.
	err = responseError(http.StatusNotFound, nil)
	require.ErrorAs(t, err, &apiErr)
	assert.Contains(t, apiErr.Message, "404")

	// A JSON body without a "message" is treated as raw, not an empty message.
	err = responseError(http.StatusBadRequest, []byte(`{"code":3}`))
	require.ErrorAs(t, err, &apiErr)
	assert.Contains(t, apiErr.Message, "400")
}

func TestOkOrError(t *testing.T) {
	assert.NoError(t, okOrError(http.StatusOK, nil))

	err := okOrError(http.StatusInternalServerError, []byte(`{"message":"nope"}`))
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "nope", apiErr.Message)
}

func TestIsUnauthenticated(t *testing.T) {
	assert.True(t, IsUnauthenticated(&APIError{Status: http.StatusUnauthorized}))
	// wrapped errors are unwrapped via errors.As
	assert.True(t, IsUnauthenticated(fmt.Errorf("context: %w", &APIError{Status: http.StatusUnauthorized})))
	assert.False(t, IsUnauthenticated(&APIError{Status: http.StatusForbidden}))
	assert.False(t, IsUnauthenticated(errors.New("plain")))
	assert.False(t, IsUnauthenticated(nil))
}

func TestIsPermissionDenied(t *testing.T) {
	assert.True(t, IsPermissionDenied(&APIError{Status: http.StatusForbidden}))
	assert.True(t, IsPermissionDenied(fmt.Errorf("context: %w", &APIError{Status: http.StatusForbidden})))
	assert.False(t, IsPermissionDenied(&APIError{Status: http.StatusUnauthorized}))
	assert.False(t, IsPermissionDenied(errors.New("plain")))
	assert.False(t, IsPermissionDenied(nil))
}

func TestAPIErrorError(t *testing.T) {
	assert.Equal(t, "boom", (&APIError{Status: http.StatusBadRequest, Message: "boom"}).Error())
	// no message -> synthesized from the status
	assert.Contains(t, (&APIError{Status: http.StatusInternalServerError}).Error(), "500")
}

func TestItems(t *testing.T) {
	assert.Nil(t, items[int](nil))
	assert.Equal(t, []int{1, 2, 3}, items(&[]int{1, 2, 3}))
	assert.Equal(t, []int{}, items(&[]int{}))
}

func TestPageCursor(t *testing.T) {
	assert.Equal(t, "", pageCursor(nil))
	assert.Equal(t, "", pageCursor(&oapi.LivekitPublicapiCommonV1PageInfo{}))
	assert.Equal(t, "next-123", pageCursor(&oapi.LivekitPublicapiCommonV1PageInfo{NextCursor: ptr("next-123")}))
}

func TestRequirePayload(t *testing.T) {
	// nil payload -> error naming the missing thing
	got, err := requirePayload[string](nil, "project")
	assert.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project")

	// present payload -> returned unchanged
	s := "value"
	got, err = requirePayload(&s, "project")
	require.NoError(t, err)
	assert.Same(t, &s, got)
}

func TestBearerAuth(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.test", nil)
	require.NoError(t, err)
	require.NoError(t, bearerAuth("sekret")(context.Background(), req))
	assert.Equal(t, "Bearer sekret", req.Header.Get("Authorization"))
}

func TestNewDefaultsBaseURL(t *testing.T) {
	c, err := New("", "tok")
	require.NoError(t, err)
	require.NotNil(t, c)
}

// jsonServer returns an httptest server that records the last request and
// replies with the given status and body (as application/json).
func jsonServer(t *testing.T, status int, body string, gotAuth, gotPath *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if gotPath != nil {
			*gotPath = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGetProject exercises the full plumbing: New + bearerAuth editor + envelope
// unwrap + requirePayload.
func TestGetProject(t *testing.T) {
	var gotAuth, gotPath string
	srv := jsonServer(t, http.StatusOK, `{"project":{"id":"p1","name":"Proj"}}`, &gotAuth, &gotPath)

	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	p, err := c.GetProject(context.Background(), "p1")
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "Bearer sekret", gotAuth)
	assert.Equal(t, "/v1/projects/p1", gotPath)
	require.NotNil(t, p.Id)
	assert.Equal(t, "p1", *p.Id)
	require.NotNil(t, p.Name)
	assert.Equal(t, "Proj", *p.Name)
}

// TestGetProjectUnauthenticated confirms a 401 body is classified as an
// unauthenticated APIError through the full call path.
func TestGetProjectUnauthenticated(t *testing.T) {
	srv := jsonServer(t, http.StatusUnauthorized, `{"message":"invalid token"}`, nil, nil)

	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	_, err = c.GetProject(context.Background(), "p1")
	require.Error(t, err)
	assert.True(t, IsUnauthenticated(err))
	assert.False(t, IsPermissionDenied(err))

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "invalid token", apiErr.Message)
}

// TestGetProjectMissingPayload confirms a 200 with no project payload surfaces a
// clear error (requirePayload) rather than a nil pointer the caller would deref.
func TestGetProjectMissingPayload(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{}`, nil, nil)

	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	p, err := c.GetProject(context.Background(), "p1")
	assert.Nil(t, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing project")
}

// TestListProjects exercises the list path (items unwrap of a 200 body).
func TestListProjects(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"items":[{"id":"p1"},{"id":"p2"}]}`, nil, nil)

	c, err := New(srv.URL, "sekret")
	require.NoError(t, err)

	projects, err := c.ListProjects(context.Background())
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.NotNil(t, projects[0].Id)
	assert.Equal(t, "p1", *projects[0].Id)
}

// TestDeleteProject exercises okOrError on the success and error paths.
func TestDeleteProject(t *testing.T) {
	okSrv := jsonServer(t, http.StatusOK, `{}`, nil, nil)
	okClient, err := New(okSrv.URL, "sekret")
	require.NoError(t, err)
	require.NoError(t, okClient.DeleteProject(context.Background(), "p1"))

	errSrv := jsonServer(t, http.StatusForbidden, `{"message":"denied"}`, nil, nil)
	errClient, err := New(errSrv.URL, "sekret")
	require.NoError(t, err)
	err = errClient.DeleteProject(context.Background(), "p1")
	require.Error(t, err)
	assert.True(t, IsPermissionDenied(err))
}
