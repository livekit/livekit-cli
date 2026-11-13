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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
	"github.com/livekit/livekit-cli/v2/pkg/console"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const (
	// replayCallTimeout bounds how long the agent may take to make its next
	// recorded LLM request: a tool call between two requests runs for real.
	replayCallTimeout = 60 * time.Second
	// replayQuiet is how long the agent must stay idle before the next user
	// turn is sent, and, after the last recorded request, before the replay
	// passes: a request made in that window fails it.
	replayQuiet = 2 * time.Second
	// replayIngestWait is how long after a session ends its traces are taken to
	// be complete: spans land out of order, minutes after the agent exports them,
	// and a replay of a session missing its last requests would pass on a prefix.
	replayIngestWait = 3 * time.Minute
)

var simulateReplayCommand = &cli.Command{
	Name:  "replay",
	Usage: "Rerun a session against your local agent with the session's own LLM responses (requires --user-auth)",
	Description: "Runs the agent locally in console mode and replays the session's user turns to it as text. " +
		"Every LLM request the agent makes is answered with the response the same request got in the session, " +
		"read from the session's traces; no LLM provider is called. Tools run for real.\n\n" +
		"The replay fails at the first LLM request that differs from the session's, or that comes out of order, " +
		"and shows what differs. It passes once the agent has made every request the session made, and no more.",
	UsageText: "lk agent simulate replay SESSION_ID [entrypoint] --user-auth",
	ArgsUsage: "SESSION_ID [entrypoint]",
	Hidden:    true,
	Metadata:  userAuthOnly,
	Action:    runSimulateReplay,
}

func runSimulateReplay(ctx context.Context, cmd *cli.Command) error {
	sessionID := cmd.Args().First()
	if sessionID == "" {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("session ID is required")
	}
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	projectDir, projectType, err := agentfs.DetectProjectRoot(".")
	if err != nil {
		return err
	}
	entrypoint, err := findEntrypoint(projectDir, cmd.Args().Get(1), projectType)
	if err != nil {
		return err
	}

	recorded, _, err := client.GetSession(ctx, projectID, sessionID)
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "session", projectReadAccess)
	}
	if recorded.EndedAt == nil {
		return fmt.Errorf("session %s is still running; replay it once it has ended", sessionID)
	}
	if wait := time.Until(recorded.EndedAt.Add(replayIngestWait)); wait > 0 {
		return fmt.Errorf("session %s ended %s ago, and its traces can take %s to arrive; replay it in %s",
			sessionID, time.Since(*recorded.EndedAt).Round(time.Second), replayIngestWait, wait.Round(time.Second))
	}
	spans, err := readSessionSpans(ctx, client, projectID, sessionID)
	if err != nil {
		return sessionReadError(err, projectID, sessionID, "trace spans", projectAdminAccess)
	}
	calls, err := replayCallsFromSpans(spans)
	if err != nil {
		return fmt.Errorf("session %s: %w", sessionID, err)
	}
	turns := replayUserTurns(calls)

	r, err := startReplayServer(calls)
	if err != nil {
		return err
	}
	defer r.close()

	userTurns := 0
	for _, t := range turns {
		userTurns += len(t)
	}
	out.Statusf("Replaying session %s: %d LLM requests, %d user turns, against %s",
		util.Accented(sessionID), len(calls), userTurns, util.Accented(entrypoint))

	tcp, err := console.NewTCPServer("127.0.0.1:0")
	if err != nil {
		return err
	}
	defer tcp.Close()
	agentProc, err := startAgent(AgentStartConfig{
		Dir:         projectDir,
		Entrypoint:  entrypoint,
		ProjectType: projectType,
		CLIArgs:     buildConsoleArgs(tcp.Addr().String(), false),
		Env:         []string{"LIVEKIT_LLM_REPLAY_URL=" + r.url},
		FailSignals: consoleCrashSignals,
	})
	if err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}
	defer agentProc.Kill()

	session, err := acceptReplayAgent(ctx, tcp, agentProc)
	if err != nil {
		return err
	}
	if err := session.SetTextMode(ctx); err != nil {
		return fmt.Errorf("failed to switch the agent to text mode: %w", err)
	}

	go watchReplayAgent(r, session, agentProc)
	d := &replayDriver{r: r, session: session}
	if err := d.run(ctx, calls, turns); err != nil {
		return err
	}
	out.Resultf("%s all %d LLM requests matched the session, in order\n", util.Accented("Replay passed:"), len(calls))
	return nil
}

func readSessionSpans(ctx context.Context, client *public.Client, projectID, sessionID string) ([]oapi.LivekitPublicapiObservabilityV1Span, error) {
	var spans []oapi.LivekitPublicapiObservabilityV1Span
	cursor := ""
	for {
		page, err := client.GetSessionTraces(ctx, projectID, sessionID, public.PageOptions{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		spans = append(spans, page.Spans...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			return spans, nil
		}
		cursor = page.NextCursor
	}
}

func acceptReplayAgent(ctx context.Context, tcp *console.TCPServer, agentProc *AgentProcess) (*agentSession, error) {
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := tcp.Accept(); err == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		return newAgentSession(conn, nil, nil), nil
	case <-agentProc.Done():
		return nil, fmt.Errorf("the agent exited before connecting.\n\n%s", agentExitDetail(agentProc))
	case <-agentProc.Failed():
		time.Sleep(500 * time.Millisecond)
		return nil, fmt.Errorf("the agent job crashed before connecting.\n\n%s", agentExitDetail(agentProc))
	case <-time.After(agentConnectTimeout):
		return nil, fmt.Errorf("timed out waiting for the agent to connect.\n\n%s", agentExitDetail(agentProc))
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// replayServer answers the agent's LLM requests with the session's recorded
// responses, strictly in the session's order. The first request that differs
// from the next recorded one fails the replay; every later request is refused.
type replayServer struct {
	calls []replayCall
	url   string
	http  *http.Server

	mu      sync.Mutex
	served  int
	failure error
	changed chan struct{} // closed and replaced whenever served or failure changes
}

func startReplayServer(calls []replayCall) (*replayServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &replayServer{calls: calls, url: "http://" + ln.Addr().String() + "/llm", changed: make(chan struct{})}
	r.http = &http.Server{Handler: http.HandlerFunc(r.handle)}
	go func() { _ = r.http.Serve(ln) }()
	return r, nil
}

func (r *replayServer) close() { _ = r.http.Close() }

func (r *replayServer) handle(w http.ResponseWriter, req *http.Request) {
	var got replayRequest
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		writeReplayError(w, http.StatusBadRequest, fmt.Errorf("unreadable LLM request: %w", err))
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		writeReplayError(w, http.StatusConflict, r.failure)
		return
	}
	n := r.served
	if n >= len(r.calls) {
		r.failLocked(fmt.Errorf("LLM request %d: the session made only %d LLM requests, so the agent asked for one more than it did", n+1, len(r.calls)))
		writeReplayError(w, http.StatusConflict, r.failure)
		return
	}
	if diff := r.calls[n].diff(got); diff != "" {
		for i := n + 1; i < len(r.calls); i++ {
			if r.calls[i].diff(got) == "" {
				diff = fmt.Sprintf("came out of order: it is the session's request %d, and request %d was due\n%s", i+1, n+1, diff)
				break
			}
		}
		r.failLocked(fmt.Errorf("LLM request %d of %d diverged from the session: %s", n+1, len(r.calls), diff))
		writeReplayError(w, http.StatusConflict, r.failure)
		return
	}
	r.served++
	r.notifyLocked()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"output_messages": r.calls[n].output,
		"response_id":     r.calls[n].responseID,
	})
}

func writeReplayError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (r *replayServer) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(err)
}

func (r *replayServer) failLocked(err error) {
	if r.failure == nil {
		r.failure = err
		r.notifyLocked()
	}
}

func (r *replayServer) notifyLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

// state returns how many requests were served, a channel closed on the next
// change, and the failure if any.
func (r *replayServer) state() (int, <-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.served, r.changed, r.failure
}

// replayDriver plays the session's user turns to the agent, each once the
// agent has made every request that preceded it in the session and gone idle,
// as the session's user had waited for it.
type replayDriver struct {
	r       *replayServer
	session *agentSession
}

func (d *replayDriver) run(ctx context.Context, calls []replayCall, turns [][]string) error {
	for i := range calls {
		for _, text := range turns[i] {
			if err := d.waitIdle(ctx); err != nil {
				return err
			}
			out.Resultf("  %s %s\n", util.Dimmed("user:"), text)
			go func() {
				if _, err := d.session.Say(ctx, text, func(turnEvent) {}); err != nil && ctx.Err() == nil {
					d.r.fail(fmt.Errorf("the agent failed the user turn %q: %w", text, err))
				}
			}()
		}
		if err := d.waitServed(ctx, i+1); err != nil {
			return err
		}
		out.Resultf("  %s request %d/%d %s\n", util.Accented("✓"), i+1, len(calls), replayOutputSummary(calls[i].output))
	}
	return d.waitIdle(ctx)
}

func (d *replayDriver) waitServed(ctx context.Context, n int) error {
	deadline := time.After(replayCallTimeout)
	for {
		served, changed, failure := d.r.state()
		if failure != nil {
			return failure
		}
		if served >= n {
			return nil
		}
		select {
		case <-changed:
		case <-deadline:
			return fmt.Errorf("LLM request %d of %d never came: the agent made %d of the session's LLM requests, then none for %s "+
				"(an agent on a livekit-agents release without replay support calls its LLM provider instead)",
				n, len(d.r.calls), served, replayCallTimeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitIdle returns once the agent has had no turn in flight and no busy state
// for replayQuiet, with no request served or failed meanwhile.
func (d *replayDriver) waitIdle(ctx context.Context) error {
	for {
		_, changed, failure := d.r.state()
		if failure != nil {
			return failure
		}
		quiet := time.After(replayQuiet)
		idle := true
		ticker := time.NewTicker(50 * time.Millisecond)
	watch:
		for {
			select {
			case <-ticker.C:
				if d.session.TurnInProgress() || isBusy(d.session.AgentState()) {
					idle = false
					break watch
				}
			case <-changed:
				idle = false
				break watch
			case <-quiet:
				break watch
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			}
		}
		ticker.Stop()
		if idle {
			_, _, failure := d.r.state()
			return failure
		}
	}
}

// watchReplayAgent fails the replay when the agent goes away before it ends.
func watchReplayAgent(r *replayServer, session *agentSession, agentProc *AgentProcess) {
	select {
	case <-agentProc.Done():
		r.fail(fmt.Errorf("the agent exited mid-replay.\n\n%s", agentExitDetail(agentProc)))
	case <-agentProc.Failed():
		time.Sleep(500 * time.Millisecond)
		r.fail(fmt.Errorf("the agent job crashed mid-replay.\n\n%s", agentExitDetail(agentProc)))
	case <-session.Done():
		r.fail(errors.New("the agent disconnected mid-replay"))
	}
}

// replayOutputSummary is what the agent was answered with: its text, or the
// tools it was told to call.
func replayOutputSummary(output []any) string {
	var text, tools []string
	for _, m := range output {
		msg, _ := m.(map[string]any)
		parts, _ := msg["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			switch part["type"] {
			case "text":
				s, _ := part["content"].(string)
				text = append(text, s)
			case "tool_call":
				name, _ := part["name"].(string)
				tools = append(tools, fmt.Sprintf("%s(%s)", name, compactJSON(part["arguments"])))
			}
		}
	}
	summary := ""
	if len(text) > 0 {
		summary = util.Dimmed("agent:") + " " + fmt.Sprint(text[0])
		for _, t := range text[1:] {
			summary += " " + t
		}
	}
	for _, t := range tools {
		if summary != "" {
			summary += " "
		}
		summary += util.Dimmed("tool call:") + " " + t
	}
	return summary
}
