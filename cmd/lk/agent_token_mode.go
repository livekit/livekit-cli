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
	"fmt"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/tokenauth"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/auth"
	agent "github.com/livekit/protocol/livekit/agent"
)

// Token mode for `lk agent dev`.
//
// Under --user-auth, when the credential chain (app_credentials.go) would end
// in asking for a key (no key found, and lk couldn't create one), `lk agent
// dev` runs the agent on a worker token
// instead: lk gets one from the Public API (ProjectService.CreateToken) as the
// signed-in user and starts the agent with LIVEKIT_URL and LIVEKIT_TOKEN, and
// no LIVEKIT_API_KEY or LIVEKIT_API_SECRET. The worker registers with the
// token, and its jobs get their tokens from the server. Before the token
// expires, lk gets a new one and sends it to the agent over the dev channel
// (AgentDevMessage.Credentials). The agent's SDK says in ServerInfo that it
// supports token mode; when it does not, lk asks for an SDK upgrade, and says
// how to supply a key instead.

const (
	// workerTokenTTL is how long a worker token is valid.
	workerTokenTTL = time.Hour
	// workerTokenRetry is the wait before lk tries again after a failed
	// refresh.
	workerTokenRetry = 30 * time.Second

	// The first releases of the agent SDKs that support token mode.
	// TODO: placeholders; set them when the SDKs are released.
	tokenModeMinPython = "X.Y.Z"
	tokenModeMinNode   = "X.Y.Z"
)

// workerTokenGrants are the grants of a worker token: what the worker uses
// itself (registration, LiveKit Inference, telemetry). Jobs get their own
// grants from the server (CreateJobTokenRequest).
func workerTokenGrants() *auth.ClaimGrants {
	return &auth.ClaimGrants{
		Video:         &auth.VideoGrant{Agent: true},
		Inference:     &auth.InferenceGrant{Perform: true},
		Observability: &auth.ObservabilityGrant{Write: true},
	}
}

// workerToken is the worker token an agent runs on in token mode. lk keeps it
// fresh (keepFresh). Safe for concurrent use.
type workerToken struct {
	source tokenauth.TokenSource
	url    string
	// now and after are time.Now and time.After, replaced in tests.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newWorkerToken(source tokenauth.TokenSource, url string) *workerToken {
	return &workerToken{source: source, url: url, now: time.Now, after: time.After}
}

// fetch gets a new token from source.
func (w *workerToken) fetch(ctx context.Context) error {
	token, expiresAt, err := w.source.Fetch(ctx, workerTokenGrants(), workerTokenTTL)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.token, w.expiresAt = token, expiresAt
	w.mu.Unlock()
	return nil
}

// current returns the current token.
func (w *workerToken) current() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.token
}

// credentials returns the current token as a dev-channel message.
func (w *workerToken) credentials() *agent.AgentDevMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return &agent.AgentDevMessage{Message: &agent.AgentDevMessage_Credentials{
		Credentials: &agent.Credentials{Url: w.url, Token: w.token, ExpiresAt: w.expiresAt.Unix()},
	}}
}

// refreshDelay is the wait until the next refresh: half of the current
// token's remaining lifetime, which leaves the other half for retries.
func (w *workerToken) refreshDelay() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return max(w.expiresAt.Sub(w.now())/2, 0)
}

// keepFresh gets a new token at half of the current one's lifetime, and
// gives each new one to push, until ctx ends. A failed refresh warns and
// tries again after workerTokenRetry; the agent keeps running on the current
// token.
func (w *workerToken) keepFresh(ctx context.Context, push func()) {
	wait := w.refreshDelay()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.after(wait):
		}
		if err := w.fetch(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.mu.Lock()
			expiresAt := w.expiresAt
			w.mu.Unlock()
			out.Warnf("Couldn't refresh the agent's worker token, which expires at %s; trying again in %s: %v",
				expiresAt.Format(time.Kitchen), workerTokenRetry, err)
			wait = workerTokenRetry
			continue
		}
		push()
		wait = w.refreshDelay()
	}
}

// workerTokenCredentials returns token-mode credentials for session project
// pc: its URL and a worker token, fetched as the signed-in user.
func workerTokenCredentials(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig) (agentCredentials, error) {
	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return agentCredentials{}, err
	}
	// Not the session's caching source: each refresh needs a new token.
	wt := newWorkerToken(&publicAPITokenSource{client: client, projectID: pc.ProjectId}, pc.URL)
	if err := wt.fetch(ctx); err != nil {
		return agentCredentials{}, err
	}
	out.Statusf("Running the agent without an API key, on a worker token for project [%s] that lk refreshes", util.Accented(pc.Name))
	return agentCredentials{url: pc.URL, workerToken: wt}, nil
}

// devTokenMode is the token-mode side of the dev channel: it gives the agent
// the current worker token, and checks that the agent's SDK supports token
// mode.
type devTokenMode struct {
	token *workerToken
	// noKey is the error the credential chain ends in without token mode.
	noKey       error
	projectType agentfs.ProjectType

	mu sync.Mutex
	// session is the latest session whose agent supports token mode: the one
	// a refreshed token goes to.
	session *devSession
	// reported records that an agent sent ServerInfo.
	reported bool
}

// onServerInfo handles an agent's ServerInfo on session s: an agent that
// supports token mode gets the current token at once, since its environment
// can hold an older one; for one that does not, lk says how to fix it.
func (t *devTokenMode) onServerInfo(s *devSession, supportsTokenMode bool) {
	t.mu.Lock()
	t.reported = true
	if supportsTokenMode {
		t.session = s
	}
	t.mu.Unlock()
	if !supportsTokenMode {
		out.Warnf("%s", upperFirst(t.unsupportedMessage(true)))
		return
	}
	if err := s.write(t.token.credentials()); err != nil {
		out.Warnf("Couldn't send the worker token to the agent: %v", err)
	}
}

// push sends the current token to the agent that supports token mode.
func (t *devTokenMode) push() {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s == nil {
		return
	}
	if err := s.write(t.token.credentials()); err != nil {
		out.Warnf("Couldn't send the refreshed worker token to the agent: %v", err)
	}
}

// exitError returns an error when the agent exited without ever reporting
// over the dev channel: an SDK without token mode can fail on the missing API
// key before it connects. It returns nil when an agent reported (an SDK
// without support was already reported in onServerInfo).
func (t *devTokenMode) exitError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reported {
		return nil
	}
	return fmt.Errorf("%s", t.unsupportedMessage(false))
}

// unsupportedMessage asks for an SDK upgrade, for an agent whose SDK does not
// support token mode (sure), or may not (the agent exited before it said),
// and ends in the no-key error, which says how to supply a key instead.
func (t *devTokenMode) unsupportedMessage(sure bool) string {
	upgrade := "livekit-agents (Python) to " + tokenModeMinPython
	if t.projectType.IsNode() {
		upgrade = "@livekit/agents (Node.js) to " + tokenModeMinNode
	}
	why := "its SDK release doesn't support that"
	if !sure {
		why = "it exited before it reported that its SDK supports that. " +
			"If its output says that an API key or secret is required, the SDK doesn't"
	}
	return fmt.Sprintf("upgrade %s or later, which can run without an API key (token mode): "+
		"lk started the agent on a worker token, and %s.\n\n%s", upgrade, why, upperFirst(t.noKey.Error()))
}
