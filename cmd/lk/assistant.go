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
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/overlay"
)

var AssistantCommands = []*cli.Command{
	{
		Name:   "assistant",
		Usage:  "Talk to the LiveKit voice assistant (preview)",
		Hidden: true,
		Description: `Opens the assistant overlay over the desktop and starts a voice conversation
with the LiveKit assistant, using your microphone and speakers. The assistant
answers from the LiveKit docs and shows code on screen, with a button to copy
it.

The assistant is a LiveKit agent that runs on your machine and uses LiveKit
Inference through your LiveKit Cloud project. The first run installs it with
uv.

Press Esc to close the overlay. With --demo, the overlay plays a scripted
conversation instead; press R to replay it.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "demo",
				Usage: "Play a scripted conversation instead of starting the assistant",
			},
			&cli.StringFlag{
				Name:   "agent-dir",
				Usage:  "Run the assistant agent from this project directory instead of the built-in one",
				Hidden: true,
			},
			&cli.BoolFlag{
				Name:  "windowed",
				Usage: "Open in a window instead of covering the screen",
			},
			&cli.BoolFlag{
				Name:  "inspect",
				Usage: "Enable the Safari Web Inspector for the overlay page",
			},
		},
		Action: runAssistant,
	},
}

func runAssistant(ctx context.Context, cmd *cli.Command) error {
	ov := overlay.New(overlay.Options{
		Windowed:    cmd.Bool("windowed"),
		Inspectable: cmd.Bool("inspect"),
	})
	if cmd.Bool("demo") {
		demo := &assistantDemo{ov: ov, snippets: map[string]string{}}
		go demo.run(ctx)
		return ov.Run(ctx)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	session := newAssistantSession(ov, cmd)
	sessionErr := make(chan error, 1)
	go func() { sessionErr <- session.run(sessionCtx) }()

	if err := ov.Run(ctx); err != nil {
		return err
	}
	cancel()
	if dir, err := os.Getwd(); err == nil {
		if path, err := session.log.save(dir); err == nil && path != "" {
			out.Statusf("Saved this conversation to %s", path)
		}
	}
	// Give the session time to stop the agent, but always exit.
	select {
	case err := <-sessionErr:
		return err
	case <-time.After(assistantShutdownTimeout):
		return nil
	}
}

// assistantShutdownTimeout bounds cleanup after the overlay closes. Stopping
// the agent takes up to about 3 seconds.
const assistantShutdownTimeout = 5 * time.Second

// Messages sent to the overlay page. See the protocol in package overlay.
type (
	overlayState struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	overlayBands struct {
		Type  string    `json:"type"`
		Bands []float64 `json:"bands"`
	}
	overlayTranscript struct {
		Type    string `json:"type"`
		Turn    string `json:"turn"`
		Role    string `json:"role"`
		Segment int    `json:"segment"`
		Text    string `json:"text"`
	}
	overlaySnippet struct {
		Type   string `json:"type"`
		Turn   string `json:"turn"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Lang   string `json:"lang"`
		Code   string `json:"code"`
		Copied bool   `json:"copied"`
	}
	overlayCopied struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	overlayTool struct {
		Type   string `json:"type"`
		Turn   string `json:"turn"`
		Role   string `json:"role,omitempty"` // turn role; agent if empty
		ID     string `json:"id"`
		Label  string `json:"label,omitempty"`
		Detail string `json:"detail,omitempty"`
		Status string `json:"status"` // running, done, or error
	}
	overlayNotice struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Sticky bool   `json:"sticky,omitempty"`
	}
)

// demoSnippet is a code card in an agent reply.
type demoSnippet struct {
	id, title, lang, code string
}

// demoTurn is one exchange. Each agent part is a string (a paragraph of
// speech) or a demoSnippet.
type demoTurn struct {
	user  string
	agent []any
}

// The agent code is from the Voice AI quickstart
// (docs.livekit.io/agents/start/voice-ai-quickstart).
var demoScript = []demoTurn{
	{
		user: "How do I build a voice agent in Python?",
		agent: []any{
			"Start from the Python starter template. This command creates the project and adds your LiveKit Cloud credentials to it.",
			demoSnippet{id: "init", title: "Terminal", lang: "shell", code: "lk agent init my-agent --template agent-starter-python\ncd my-agent"},
			"Then run lk agent dev and talk to it from the Agent Console.",
		},
	},
	{
		user: "Where do I change the voice?",
		agent: []any{
			"In agent.py. The agent session sets the speech-to-text, language, and text-to-speech models. Change the TTS model or voice there.",
			demoSnippet{id: "agent", title: "agent.py", lang: "python", code: `@server.rtc_session(agent_name="my-agent")
async def my_agent(ctx: agents.JobContext):
    session = AgentSession(
        stt=inference.STT(model="assemblyai/universal-3-6-pro", language="en"),
        llm=inference.LLM(model="google/gemma-4-31b-it"),
        tts=inference.TTS(
            model="fishaudio/s2.1-pro",
            voice="fa4c9eb3dccc4806b382b40d61c6b10a",
        ),
        turn_handling=TurnHandlingOptions(
            turn_detection=inference.TurnDetector(),
        ),
    )

    await session.start(room=ctx.room, agent=Assistant())`},
			"Copy it from the card to use it in your project.",
		},
	},
	{
		user: "And how do I deploy it?",
		agent: []any{
			"Run this from the project directory. It registers the agent and deploys it to LiveKit Cloud.",
			demoSnippet{id: "deploy", title: "Terminal", lang: "shell", code: "lk agent create"},
		},
	},
}

const demoFrame = time.Second / 60

// assistantDemo plays a scripted conversation into the overlay. It stands in
// for the real agent session until the overlay is wired to a LiveKit room.
type assistantDemo struct {
	ov       *overlay.Overlay
	voice    fakeVoice
	snippets map[string]string
}

func (d *assistantDemo) run(ctx context.Context) {
	start := make(chan struct{}, 1)
	var (
		mu         sync.Mutex
		cancelPlay context.CancelFunc
	)
	go func() {
		for {
			select {
			case ev := <-d.ov.Events():
				switch ev.Type {
				case "ready", "replay":
					mu.Lock()
					if cancelPlay != nil {
						cancelPlay()
					}
					mu.Unlock()
					select {
					case start <- struct{}{}:
					default:
					}
				case "copy":
					d.copy(ev.ID)
				}
			case <-d.ov.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-start:
		case <-ctx.Done():
			return
		case <-d.ov.Done():
			return
		}
		playCtx, cancel := context.WithCancel(ctx)
		mu.Lock()
		cancelPlay = cancel
		mu.Unlock()

		d.ov.Send(map[string]string{"type": "clear"})
		d.ov.Send(overlayState{"state", "idle"})
		d.play(playCtx)
		cancel()
	}
}

// play runs the script once. It returns false if it was stopped early.
func (d *assistantDemo) play(ctx context.Context) bool {
	// Let the intro finish: the mark shows large, then docks.
	if !d.wait(ctx, 1800*time.Millisecond, nil) {
		return false
	}
	for i, turn := range demoScript {
		userTurn, agentTurn := fmt.Sprintf("u%d", i), fmt.Sprintf("a%d", i)

		d.ov.Send(overlayState{"state", "listening"})
		if !d.speak(ctx, userTurn, "user", 0, turn.user, 270*time.Millisecond, 0.7) {
			return false
		}
		if !d.wait(ctx, 400*time.Millisecond, nil) {
			return false
		}

		d.ov.Send(overlayState{"state", "thinking"})
		if !d.wait(ctx, 1300*time.Millisecond, nil) {
			return false
		}

		d.ov.Send(overlayState{"state", "speaking"})
		segment := 0
		for _, part := range turn.agent {
			switch p := part.(type) {
			case string:
				if !d.speak(ctx, agentTurn, "agent", segment, p, 190*time.Millisecond, 1) {
					return false
				}
				segment++
			case demoSnippet:
				d.snippet(agentTurn, p)
				if !d.wait(ctx, 600*time.Millisecond, nil) {
					return false
				}
			}
		}

		d.ov.Send(overlayState{"state", "idle"})
		if !d.wait(ctx, 1400*time.Millisecond, nil) {
			return false
		}
	}
	d.ov.Send(overlayState{"state", "listening"})
	return true
}

// speak streams one segment of a turn word by word, like a live transcript,
// while sending simulated voice levels.
func (d *assistantDemo) speak(ctx context.Context, turn, role string, segment int, text string, perWord time.Duration, gain float64) bool {
	words := strings.Fields(text)
	begin := time.Now()
	shown := 0
	return d.wait(ctx, time.Duration(len(words))*perWord+200*time.Millisecond, func(elapsed time.Duration) {
		d.ov.Send(overlayBands{"bands", d.voice.bands(time.Since(begin).Seconds(), gain)})
		if n := min(len(words), int(elapsed/perWord)+1); n != shown {
			shown = n
			d.ov.Send(overlayTranscript{"transcript", turn, role, segment, strings.Join(words[:n], " ")})
		}
	})
}

// wait blocks for dur, calling tick every frame. It returns false if the
// overlay closed or ctx ended.
func (d *assistantDemo) wait(ctx context.Context, dur time.Duration, tick func(elapsed time.Duration)) bool {
	ticker := time.NewTicker(demoFrame)
	defer ticker.Stop()
	begin := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-d.ov.Done():
			return false
		case now := <-ticker.C:
			elapsed := now.Sub(begin)
			if elapsed >= dur {
				return true
			}
			if tick != nil {
				tick(elapsed)
			}
		}
	}
}

func (d *assistantDemo) snippet(turn string, sn demoSnippet) {
	d.snippets[sn.id] = sn.code
	d.ov.Send(overlaySnippet{"snippet", turn, sn.id, sn.title, sn.lang, sn.code, false})
}

func (d *assistantDemo) copy(id string) {
	code, ok := d.snippets[id]
	if !ok || clipboard.WriteAll(code) != nil {
		return
	}
	d.ov.Send(overlayCopied{"copied", id})
}

// fakeVoice synthesizes speech-like frequency bands: syllable-rate pulses,
// pauses between phrases, and two moving formants over a falling spectrum.
type fakeVoice struct{}

const fakeVoiceBands = 24

func (fakeVoice) bands(t, gain float64) []float64 {
	syl := math.Pow(math.Max(0, math.Sin(2*math.Pi*4.3*t+0.8*math.Sin(2*math.Pi*0.7*t))), 0.7)
	gate := smoothstep(0.15, 0.35, 0.5+0.5*math.Sin(2*math.Pi*0.45*t+1.3))
	f1 := 4 + 3*math.Sin(2*math.Pi*1.1*t)
	f2 := 12 + 5*math.Sin(2*math.Pi*0.63*t+2)

	out := make([]float64, fakeVoiceBands)
	for i := range out {
		x := float64(i)
		s := 0.75*math.Exp(-x/9) + 0.55*math.Exp(-(x-f1)*(x-f1)/9.7) + 0.35*math.Exp(-(x-f2)*(x-f2)/24.5)
		v := syl * gate * gain * s * 1.25 * (0.85 + 0.3*rand.Float64())
		out[i] = math.Round(math.Min(1, v)*100) / 100
	}
	return out
}

func smoothstep(e0, e1, x float64) float64 {
	t := math.Max(0, math.Min(1, (x-e0)/(e1-e0)))
	return t * t * (3 - 2*t)
}
