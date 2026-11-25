package console

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	agent "github.com/livekit/protocol/livekit/agent"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

//go:embed web/*
var webAssets embed.FS

var webIndex = template.Must(template.ParseFS(webAssets, "web/index.html"))

const webWriteTimeout = 5 * time.Second

// RunWeb serves a single browser session on loopback. It owns conn and doesn't
// read from it until the tab connects, so the opening greeting isn't discarded.
func RunWeb(ctx context.Context, conn net.Conn, textMode bool, onListen func(string), recentLogs func() []string) error {
	defer conn.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("browser console: %w", err)
	}
	defer listener.Close()

	key := rand.Text()
	host := listener.Addr().String()
	origin := "http://" + host
	query := url.Values{"k": {key}}

	upgrader := websocket.Upgrader{
		HandshakeTimeout: webWriteTimeout,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == origin
		},
	}
	var connected atomic.Bool
	tabDone := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		if !webKeyMatches(r, key) || r.Header.Get("Origin") != origin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !connected.CompareAndSwap(false, true) {
			http.Error(w, "a tab is already connected", http.StatusConflict)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connected.Store(false)
			return
		}
		tabDone <- exchangeWebMessages(ctx, ws, conn, textMode || r.URL.Query().Get("text") == "1")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !webKeyMatches(r, key) {
			http.Error(w, "Use the URL printed by lk.", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = webIndex.Execute(w, textMode)
	})
	mux.HandleFunc("GET /logs", func(w http.ResponseWriter, r *http.Request) {
		if !webKeyMatches(r, key) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(recentLogs())
	})
	for _, name := range []string{"app.js", "style.css", "worklet.js"} {
		mux.HandleFunc("GET /static/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, webAssets, "web/"+name)
		})
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self' ws://"+host+"; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
			mux.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: webWriteTimeout,
	}
	defer server.Close()

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	onListen(origin + "/?" + query.Encode())
	select {
	case err := <-tabDone:
		return err
	case err := <-serveDone:
		return fmt.Errorf("browser console: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func webKeyMatches(r *http.Request, key string) bool {
	return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("k")), []byte(key)) == 1
}

func exchangeWebMessages(ctx context.Context, ws *websocket.Conn, conn net.Conn, textMode bool) error {
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() {
		ws.Close()
		conn.Close()
	})
	defer stop()
	ws.SetReadLimit(1 << 20)
	if textMode {
		if err := writeWebAgentMessage(conn, webAudioRequest("initial-io", false)); err != nil {
			return err
		}
	}
	errs := make(chan error, 2)
	go func() { errs <- webAgentToBrowser(ws, conn) }()
	go func() { errs <- webBrowserToAgent(ws, conn, textMode) }()
	err := <-errs
	ws.Close()
	conn.Close()
	<-errs // wait for the other relay, which may have been blocked on I/O
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure) {
		return nil
	}
	return err
}

type webEvent struct {
	Type     string          `json:"type"`
	Event    json.RawMessage `json:"event,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

func webAgentToBrowser(ws *websocket.Conn, conn net.Conn) error {
	marshal := protojson.MarshalOptions{UseProtoNames: true}
	ready := false
	for {
		msg, err := ReadSessionMessage(conn)
		if err != nil {
			return err
		}
		_ = ws.SetWriteDeadline(time.Now().Add(webWriteTimeout))
		if !ready {
			if err := ws.WriteJSON(webEvent{Type: "ready"}); err != nil {
				return err
			}
			ready = true
		}
		switch m := msg.Message.(type) {
		case *agent.AgentSessionMessage_AudioOutput:
			if err := ws.WriteMessage(websocket.BinaryMessage, m.AudioOutput.GetData()); err != nil {
				return err
			}
		case *agent.AgentSessionMessage_AudioPlaybackFlush:
			err = ws.WriteJSON(webEvent{Type: "flush"})
		case *agent.AgentSessionMessage_AudioPlaybackClear:
			err = ws.WriteJSON(webEvent{Type: "clear"})
		case *agent.AgentSessionMessage_Event:
			var event json.RawMessage
			event, err = marshal.Marshal(m.Event)
			if err == nil {
				err = ws.WriteJSON(webEvent{Type: "event", Event: event})
			}
		case *agent.AgentSessionMessage_Response:
			var response json.RawMessage
			response, err = marshal.Marshal(m.Response)
			if err == nil {
				err = ws.WriteJSON(webEvent{Type: "response", Response: response})
			}
		}
		if err != nil {
			return err
		}
	}
}

func webBrowserToAgent(ws *websocket.Conn, conn net.Conn, textMode bool) error {
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var msg *agent.AgentSessionMessage
		switch kind {
		case websocket.BinaryMessage:
			if textMode || len(data) == 0 || len(data)%2 != 0 {
				continue
			}
			msg = &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioInput{
				AudioInput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{
					Data: data, SampleRate: SampleRate, NumChannels: Channels, SamplesPerChannel: uint32(len(data) / 2),
				},
			}}
		case websocket.TextMessage:
			var control struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Text  string `json:"text"`
				Audio *bool  `json:"audio"`
			}
			if err := json.Unmarshal(data, &control); err != nil {
				return fmt.Errorf("browser console control: %w", err)
			}
			switch control.Type {
			case "run_input":
				msg = webRequest(&agent.SessionRequest{
					RequestId: control.ID,
					Request:   &agent.SessionRequest_RunInput_{RunInput: &agent.SessionRequest_RunInput{Text: control.Text}},
				})
			case "update_io":
				if control.Audio == nil {
					continue
				}
				textMode = !*control.Audio
				msg = webAudioRequest(control.ID, *control.Audio)
			case "playback_finished":
				msg = &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_AudioPlaybackFinished{
					AudioPlaybackFinished: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFinished{},
				}}
			}
		}
		if msg != nil {
			if err := writeWebAgentMessage(conn, msg); err != nil {
				return err
			}
		}
	}
}

func writeWebAgentMessage(conn net.Conn, msg *agent.AgentSessionMessage) error {
	_ = conn.SetWriteDeadline(time.Now().Add(webWriteTimeout))
	return WriteSessionMessage(conn, msg)
}

func webRequest(req *agent.SessionRequest) *agent.AgentSessionMessage {
	return &agent.AgentSessionMessage{Message: &agent.AgentSessionMessage_Request{Request: req}}
}

func webAudioRequest(id string, enabled bool) *agent.AgentSessionMessage {
	return webRequest(&agent.SessionRequest{
		RequestId: id,
		Request: &agent.SessionRequest_UpdateIo{UpdateIo: &agent.SessionRequest_UpdateIO{
			Input:  &agent.SessionRequest_UpdateIO_Input{AudioEnabled: proto.Bool(enabled)},
			Output: &agent.SessionRequest_UpdateIO_Output{AudioEnabled: proto.Bool(enabled), TranscriptionEnabled: proto.Bool(enabled)},
		}},
	})
}
