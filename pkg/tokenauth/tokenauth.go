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

// Package tokenauth lets server-sdk-go service clients run without an API
// secret: each request is authorized with a short-lived token minted elsewhere
// (the Public API, under user-session auth) carrying exactly the grants that
// call needs.
//
// The SDK signs a token for every call from its key/secret, choosing grants per
// method (e.g. RoomList for ListRooms, SIP admin for SIP calls). Clients built
// with Placeholder credentials still do that, but the secret is a well-known
// placeholder; the twirp hook from ClientOptions decodes the placeholder token
// to recover the requested grants, swaps in a minted token with the same
// grants, and sends that instead. The SDK's own grant mapping stays the single
// source of truth, so no per-method table lives here.
package tokenauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/twitchtv/twirp"

	"github.com/livekit/protocol/auth"
)

const (
	// PlaceholderAPIKey and PlaceholderAPISecret are passed to SDK client
	// constructors in place of real credentials. Tokens signed with them are
	// never sent: the hook replaces them before the request leaves.
	PlaceholderAPIKey    = "lk-minted-placeholder"
	PlaceholderAPISecret = "lk-minted-placeholder-secret-not-a-real-credential"

	// DefaultTTL is the lifetime requested for tokens authorizing SDK calls.
	DefaultTTL = 10 * time.Minute
	// refreshMargin re-fetches a cached token this long before it expires, so a
	// token doesn't lapse in flight (or across SDK failover retries).
	refreshMargin = 30 * time.Second
)

// IsPlaceholder reports whether apiKey is the placeholder, i.e. the client's
// credentials can't sign anything locally (participant tokens, webhooks, …).
func IsPlaceholder(apiKey string) bool {
	return apiKey == PlaceholderAPIKey
}

// TokenSource fetches a token for a project carrying grants, valid for about
// ttl (or the source's default when ttl is 0). It returns the token and its
// expiry.
type TokenSource interface {
	Fetch(ctx context.Context, grants *auth.ClaimGrants, ttl time.Duration) (token string, expiresAt time.Time, err error)
}

// CachingTokenSource wraps a TokenSource and caches its tokens by grants and
// ttl, so a command making many calls with the same grants (e.g. paging
// ListRooms) fetches once. Safe for concurrent use.
type CachingTokenSource struct {
	source TokenSource
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

var _ TokenSource = (*CachingTokenSource)(nil)

// NewCachingTokenSource returns a CachingTokenSource fetching from source.
func NewCachingTokenSource(source TokenSource) *CachingTokenSource {
	return &CachingTokenSource{source: source, now: time.Now, cache: map[string]cachedToken{}}
}

// Fetch returns a cached token for grants and ttl while it is still
// comfortably valid, and fetches a new one otherwise.
func (s *CachingTokenSource) Fetch(ctx context.Context, grants *auth.ClaimGrants, ttl time.Duration) (string, time.Time, error) {
	grantsJSON, err := json.Marshal(grants)
	if err != nil {
		return "", time.Time{}, err
	}
	key := fmt.Sprintf("%d:%s", ttl, grantsJSON)

	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[key]; ok && s.now().Add(refreshMargin).Before(c.expiresAt) {
		return c.token, c.expiresAt, nil
	}
	token, expiresAt, err := s.source.Fetch(ctx, grants, ttl)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fetching API token: %w", err)
	}
	s.cache[key] = cachedToken{token: token, expiresAt: expiresAt}
	return token, expiresAt, nil
}

// Exchange returns a token fetched from s with the grants of placeholderToken,
// a token that library code built and signed with the placeholder credentials
// (e.g. protocol's egress.BuildEgressToken), valid for about ttl.
func (s *CachingTokenSource) Exchange(ctx context.Context, placeholderToken string, ttl time.Duration) (string, error) {
	grants, err := placeholderGrants("Bearer " + placeholderToken)
	if err != nil {
		return "", err
	}
	token, _, err := s.Fetch(ctx, grants, ttl)
	return token, err
}

// ClientOptions returns twirp options that authorize every request through s.
// Use them with an SDK client constructed from the Placeholder credentials.
func (s *CachingTokenSource) ClientOptions() []twirp.ClientOption {
	return []twirp.ClientOption{twirp.WithClientHooks(&twirp.ClientHooks{
		RequestPrepared: s.authorize,
	})}
}

// authorize is a twirp RequestPrepared hook: it runs after the SDK has set its
// placeholder-signed Authorization header and before the request is sent.
func (s *CachingTokenSource) authorize(ctx context.Context, req *http.Request) (context.Context, error) {
	header, err := s.swapToken(ctx, req.Header)
	if err != nil {
		return ctx, twirp.NewError(twirp.Unauthenticated, err.Error())
	}
	req.Header = header
	return ctx, nil
}

// Transport returns an http.RoundTripper for clients that sign their own HTTP
// requests outside twirp hooks (e.g. cloudagents' log and build streams). It
// swaps each request's placeholder-signed bearer token for a fetched one with
// the same grants, then sends it through base (http.DefaultTransport when nil).
// Requests without an Authorization header pass through untouched.
func (s *CachingTokenSource) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{src: s, base: base}
}

type transport struct {
	src  *CachingTokenSource
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") == "" {
		return t.base.RoundTrip(req)
	}
	header, err := t.src.swapToken(req.Context(), req.Header)
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Header = header
	return t.base.RoundTrip(req)
}

// swapToken returns a copy of header whose placeholder-signed bearer token is
// replaced by a fetched token with the same grants. The original header map may
// be shared with the caller, so it is never mutated in place.
func (s *CachingTokenSource) swapToken(ctx context.Context, header http.Header) (http.Header, error) {
	grants, err := placeholderGrants(header.Get("Authorization"))
	if err != nil {
		return nil, err
	}
	token, _, err := s.Fetch(ctx, grants, DefaultTTL)
	if err != nil {
		return nil, err
	}
	header = header.Clone()
	header.Set("Authorization", "Bearer "+token)
	return header, nil
}

// placeholderGrants recovers the grants the SDK requested from a token it
// signed with the placeholder credentials.
func placeholderGrants(authorization string) (*auth.ClaimGrants, error) {
	raw, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || raw == "" {
		return nil, fmt.Errorf("request has no bearer token to replace")
	}
	v, err := auth.ParseAPIToken(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing SDK token: %w", err)
	}
	if !IsPlaceholder(v.APIKey()) {
		return nil, fmt.Errorf("SDK token was not signed with placeholder credentials")
	}
	_, grants, err := v.Verify(PlaceholderAPISecret)
	if err != nil {
		return nil, fmt.Errorf("reading SDK token grants: %w", err)
	}
	return grants, nil
}
