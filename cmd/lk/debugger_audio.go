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
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/livekit/protocol/auth"

	"github.com/livekit/livekit-cli/v2/pkg/console"
)

// The console wire carries 48 kHz mono PCM both ways.
const (
	micFrameSamples = console.SampleRate / 50 // 20ms
	micFrameTime    = 20 * time.Millisecond
)

// micStream stands in for the user's microphone: the agent receives a
// continuous real-time stream, speech when some is queued and silence
// otherwise, so its VAD and endpointing see the same input as from a live mic.
type micStream struct {
	mu      sync.Mutex
	pending []int16
	waiters []chan struct{}
}

// push queues speech to be played after anything already queued.
func (m *micStream) push(samples []int16) {
	m.mu.Lock()
	m.pending = append(m.pending, samples...)
	m.mu.Unlock()
}

// drained returns a channel closed once everything queued so far has been sent.
func (m *micStream) drained() <-chan struct{} {
	ch := make(chan struct{})
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		close(ch)
	} else {
		m.waiters = append(m.waiters, ch)
	}
	return ch
}

// run sends one frame per micFrameTime until send fails or done closes.
func (m *micStream) run(done <-chan struct{}, send func([]int16) error) {
	next := time.Now()
	for {
		frame := make([]int16, micFrameSamples)
		m.mu.Lock()
		n := copy(frame, m.pending)
		m.pending = m.pending[n:]
		var woken []chan struct{}
		if len(m.pending) == 0 {
			woken, m.waiters = m.waiters, nil
		}
		m.mu.Unlock()

		if err := send(frame); err != nil {
			return
		}
		for _, ch := range woken {
			close(ch)
		}

		next = next.Add(micFrameTime)
		wait := time.Until(next)
		if wait < -micFrameTime {
			// Fell behind; resync rather than bursting to catch up, which would
			// compress the audio the agent hears.
			next = time.Now()
			wait = 0
		}
		select {
		case <-done:
			return
		case <-time.After(wait):
		}
	}
}

// playout stands in for the user's speaker: agent audio "plays" in real time
// with nothing audible, so a flush is acknowledged only once the audio before
// it would have finished, as a real speaker would.
type playout struct {
	mu     sync.Mutex
	end    time.Time     // when the buffered agent audio finishes playing
	cancel chan struct{} // closed to abandon the pending flush acknowledgement
}

func (p *playout) write(samples, sampleRate uint32) {
	if sampleRate == 0 {
		return
	}
	d := time.Duration(samples) * time.Second / time.Duration(sampleRate)
	p.mu.Lock()
	if now := time.Now(); p.end.Before(now) {
		p.end = now
	}
	p.end = p.end.Add(d)
	p.mu.Unlock()
}

// flush calls ack once the buffered audio has played, unless a clear or a
// newer flush comes first.
func (p *playout) flush(ack func()) {
	p.mu.Lock()
	if p.cancel != nil {
		close(p.cancel)
	}
	cancel := make(chan struct{})
	p.cancel = cancel
	wait := time.Until(p.end)
	p.mu.Unlock()

	go func() {
		select {
		case <-time.After(wait):
			ack()
		case <-cancel:
		}
	}()
}

// clear drops the buffered audio; the agent accounts for the interruption itself.
func (p *playout) clear() {
	p.mu.Lock()
	p.end = time.Time{}
	if p.cancel != nil {
		close(p.cancel)
		p.cancel = nil
	}
	p.mu.Unlock()
}

// speakFunc synthesizes text as 48 kHz mono PCM, handing chunks to onAudio as
// they arrive.
type speakFunc func(ctx context.Context, text string, onAudio func([]int16)) error

const (
	userTTSModel      = "cartesia/sonic-3"
	inferenceURL      = "https://agent-gateway.livekit.cloud/v1"
	inferenceStageURL = "https://agent-gateway.staging.livekit.cloud/v1"
)

// inferenceTTS speaks the user's lines through LiveKit Inference, using the
// same gateway protocol as the agents SDKs' inference.TTS.
type inferenceTTS struct {
	livekitURL, apiKey, apiSecret string
}

func (t inferenceTTS) gatewayURL() string {
	base := inferenceURL
	if strings.Contains(t.livekitURL, ".staging.livekit.cloud") {
		base = inferenceStageURL
	}
	return strings.Replace(base, "http", "ws", 1) + "/tts?" + url.Values{"model": {userTTSModel}}.Encode()
}

func (t inferenceTTS) speak(ctx context.Context, text string, onAudio func([]int16)) error {
	token, err := auth.NewAccessToken(t.apiKey, t.apiSecret).
		SetIdentity("lk-agent-debugger").
		SetInferenceGrant(&auth.InferenceGrant{Perform: true}).
		SetValidFor(10 * time.Minute).
		ToJWT()
	if err != nil {
		return fmt.Errorf("text-to-speech: %w", err)
	}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, t.gatewayURL(), http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		if resp != nil {
			return fmt.Errorf("text-to-speech: LiveKit Inference returned %s", resp.Status)
		}
		return fmt.Errorf("text-to-speech: %w", err)
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()

	for _, msg := range []map[string]any{
		{"type": "session.create", "sample_rate": strconv.Itoa(console.SampleRate), "encoding": "pcm_s16le", "model": userTTSModel, "extra": map[string]any{}},
		{"type": "input_transcript", "transcript": text + " ", "generation_config": map[string]any{"model": userTTSModel}, "extra": map[string]any{}},
		{"type": "session.flush"},
	} {
		if err := ws.WriteJSON(msg); err != nil {
			return fmt.Errorf("text-to-speech: %w", err)
		}
	}

	var odd []byte // a sample split across chunks
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("text-to-speech: %w", err)
		}
		var msg struct {
			Type  string `json:"type"`
			Audio string `json:"audio"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "output_audio":
			pcm, err := base64.StdEncoding.DecodeString(msg.Audio)
			if err != nil {
				return fmt.Errorf("text-to-speech: %w", err)
			}
			pcm = append(odd, pcm...)
			n := len(pcm) / 2
			samples := make([]int16, n)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(pcm[2*i:]))
			}
			odd = append([]byte(nil), pcm[2*n:]...)
			onAudio(samples)
		case "done":
			return nil
		case "error":
			return fmt.Errorf("text-to-speech: LiveKit Inference returned an error: %s", data)
		}
	}
}
