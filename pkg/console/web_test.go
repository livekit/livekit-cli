package console

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	agent "github.com/livekit/protocol/livekit/agent"
)

const webTestTimeout = 3 * time.Second

type webTestSession struct {
	page      *url.URL
	agent     net.Conn
	done      chan error
	cancel    context.CancelFunc
	initialIO *agent.SessionRequest_UpdateIO
}

func startWebTest(t *testing.T, textMode bool) webTestSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cli, fakeAgent := net.Pipe()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- RunWeb(ctx, cli, textMode, func(url string) { ready <- url }, func() []string { return []string{"started"} })
	}()
	t.Cleanup(func() {
		cancel()
		fakeAgent.Close()
		select {
		case <-stopped:
		case <-time.After(webTestTimeout):
			t.Error("web server did not stop")
		}
	})
	s := webTestSession{agent: fakeAgent, done: done, cancel: cancel}
	if textMode {
		// The fake TCP agent must be able to read the request before the URL
		// is even printed, not just before the browser sends a typed turn.
		s.initialIO = s.readAgent(t).GetRequest().GetUpdateIo()
	}
	select {
	case address := <-ready:
		page, err := url.Parse(address)
		require.NoError(t, err)
		s.page = page
		return s
	case err := <-done:
		t.Fatalf("web server exited before listening: %v", err)
	case <-time.After(webTestTimeout):
		t.Fatal("web server did not start")
	}
	return webTestSession{}
}

func (s webTestSession) dial(t *testing.T, origin, key string, wantStatus int, text ...bool) *websocket.Conn {
	t.Helper()
	u := *s.page
	u.Scheme, u.Path = "ws", "/ws"
	query := url.Values{"k": {key}}
	if len(text) != 0 && text[0] {
		query.Set("text", "1")
	}
	u.RawQuery = query.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: webTestTimeout}
	ws, res, err := dialer.Dial(u.String(), http.Header{"Origin": {origin}})
	if wantStatus != http.StatusSwitchingProtocols {
		require.Error(t, err)
		require.NotNil(t, res)
		defer res.Body.Close()
		require.Equal(t, wantStatus, res.StatusCode)
		return nil
	}
	require.NoError(t, err)
	t.Cleanup(func() { ws.Close() })
	return ws
}

func (s webTestSession) origin() string { return s.page.Scheme + "://" + s.page.Host }
func (s webTestSession) key() string    { return s.page.Query().Get("k") }

func (s webTestSession) writeAgent(t *testing.T, msg *agent.AgentSessionMessage) {
	t.Helper()
	require.NoError(t, s.agent.SetWriteDeadline(time.Now().Add(webTestTimeout)))
	require.NoError(t, WriteSessionMessage(s.agent, msg))
}

func (s webTestSession) readAgent(t *testing.T) *agent.AgentSessionMessage {
	t.Helper()
	require.NoError(t, s.agent.SetReadDeadline(time.Now().Add(webTestTimeout)))
	msg, err := ReadSessionMessage(s.agent)
	require.NoError(t, err)
	return msg
}

func readWebEvent(t *testing.T, ws *websocket.Conn, kind string) webEvent {
	t.Helper()
	var message webEvent
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(webTestTimeout)))
	require.NoError(t, ws.ReadJSON(&message))
	require.Equal(t, kind, message.Type)
	return message
}

func TestWebConsoleAccess(t *testing.T) {
	s := startWebTest(t, false)
	client := http.Client{Timeout: webTestTimeout}
	for _, tc := range []struct {
		path   string
		host   string
		status int
	}{
		{"/", "", http.StatusForbidden},
		{"/?k=" + s.key(), "", http.StatusOK},
		{"/?k=" + s.key(), "another.example", http.StatusForbidden},
		{"/static/app.js", "", http.StatusOK},
		{"/static/index.html", "", http.StatusNotFound},
		{"/logs", "", http.StatusForbidden},
		{"/logs?k=" + s.key(), "", http.StatusOK},
	} {
		req, err := http.NewRequest(http.MethodGet, s.origin()+tc.path, nil)
		require.NoError(t, err)
		if tc.host != "" {
			req.Host = tc.host
		}
		res, err := client.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, tc.status, res.StatusCode, tc.path)
	}

	s.dial(t, s.origin(), "wrong-key", http.StatusForbidden)
	s.dial(t, "https://another.example", s.key(), http.StatusForbidden)
	s.dial(t, "", s.key(), http.StatusForbidden)
	s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols)
	s.dial(t, s.origin(), s.key(), http.StatusConflict)
}

func TestWebConsoleText(t *testing.T) {
	s := startWebTest(t, true)
	client := http.Client{Timeout: webTestTimeout}
	res, err := client.Get(s.page.String())
	require.NoError(t, err)
	defer res.Body.Close()
	page, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Contains(t, string(page), `data-text-mode="true"`)
	ws := s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols)
	update := s.initialIO
	require.NotNil(t, update)
	require.NotNil(t, update.Input.AudioEnabled)
	require.False(t, update.Input.GetAudioEnabled())
	require.False(t, update.Output.GetAudioEnabled())
	require.False(t, update.Output.GetTranscriptionEnabled())

	s.writeAgent(t, &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Event{
		Event: &agent.AgentSessionEvent{Event: &agent.AgentSessionEvent_AgentStateChanged_{
			AgentStateChanged: &agent.AgentSessionEvent_AgentStateChanged{NewState: agent.AgentState_AS_LISTENING},
		}},
	}})
	readWebEvent(t, ws, "ready")
	require.JSONEq(t, `{"agent_state_changed":{"new_state":"AS_LISTENING"}}`, string(readWebEvent(t, ws, "event").Event))

	require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, []byte{1, 0}))
	require.NoError(t, ws.WriteJSON(map[string]any{"type": "run_input", "id": "run-1", "text": "مرحبا"}))
	request := s.readAgent(t).GetRequest()
	require.NotNil(t, request, "mic data should be ignored in text mode")
	require.Equal(t, "run-1", request.GetRequestId())
	require.Equal(t, "مرحبا", request.GetRunInput().GetText())

	s.writeAgent(t, &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Response{
		Response: &agent.SessionResponse{RequestId: "run-1"},
	}})
	require.JSONEq(t, `{"request_id":"run-1"}`, string(readWebEvent(t, ws, "response").Response))
	require.NoError(t, ws.WriteJSON(map[string]any{"type": "update_io", "id": "io-2", "audio": true}))
	update = s.readAgent(t).GetRequest().GetUpdateIo()
	require.True(t, update.Input.GetAudioEnabled())
	require.True(t, update.Output.GetAudioEnabled())
	require.True(t, update.Output.GetTranscriptionEnabled())
}

func TestWebConsoleTypingFallback(t *testing.T) {
	s := startWebTest(t, false)
	s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols, true)
	update := s.readAgent(t).GetRequest().GetUpdateIo()
	require.NotNil(t, update)
	require.False(t, update.GetInput().GetAudioEnabled())
	require.False(t, update.GetOutput().GetAudioEnabled())
}

func TestWebConsoleVoiceAndClose(t *testing.T) {
	s := startWebTest(t, false)
	pcm := []byte{0, 1, 0, 2}
	greeting := &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioOutput{
		AudioOutput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{Data: pcm},
	}}
	// net.Pipe has no buffer. A stalled write proves the bridge leaves the
	// opening audio unread until a browser has connected.
	require.NoError(t, s.agent.SetWriteDeadline(time.Now().Add(50*time.Millisecond)))
	err := WriteSessionMessage(s.agent, greeting)
	var netErr net.Error
	require.True(t, errors.As(err, &netErr) && netErr.Timeout())

	ws := s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols)
	s.writeAgent(t, greeting)
	readWebEvent(t, ws, "ready")
	kind, payload, err := ws.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, kind)
	require.Equal(t, pcm, payload)

	require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, pcm))
	frame := s.readAgent(t).GetAudioInput()
	require.Equal(t, pcm, frame.GetData())
	require.EqualValues(t, SampleRate, frame.GetSampleRate())
	require.EqualValues(t, Channels, frame.GetNumChannels())
	require.EqualValues(t, len(pcm)/2, frame.GetSamplesPerChannel())

	s.writeAgent(t, &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackFlush{
		AudioPlaybackFlush: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFlush{},
	}})
	readWebEvent(t, ws, "flush")
	require.NoError(t, ws.WriteJSON(map[string]string{"type": "playback_finished"}))
	require.NotNil(t, s.readAgent(t).GetAudioPlaybackFinished())

	s.writeAgent(t, &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackClear{
		AudioPlaybackClear: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackClear{},
	}})
	readWebEvent(t, ws, "clear")
	require.NoError(t, ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(webTestTimeout)))
	select {
	case err := <-s.done:
		require.NoError(t, err)
	case <-time.After(webTestTimeout):
		t.Fatal("closing the tab did not end the session")
	}
}

func TestWebConsoleCancellation(t *testing.T) {
	s := startWebTest(t, false)
	ws := s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols)
	s.cancel()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(webTestTimeout)))
	_, _, err := ws.ReadMessage()
	require.Error(t, err)
	select {
	case err := <-s.done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(webTestTimeout):
		t.Fatal("canceling did not end the session")
	}
}

func TestWebConsoleAgentDisconnect(t *testing.T) {
	s := startWebTest(t, false)
	ws := s.dial(t, s.origin(), s.key(), http.StatusSwitchingProtocols)
	require.NoError(t, s.agent.Close())
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(webTestTimeout)))
	_, _, err := ws.ReadMessage()
	require.Error(t, err)
	select {
	case err := <-s.done:
		require.ErrorIs(t, err, ErrWebAgentDisconnected)
		require.ErrorIs(t, err, io.EOF)
	case <-time.After(webTestTimeout):
		t.Fatal("losing the agent did not end the session")
	}
}
