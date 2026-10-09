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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sessionLog records what the overlay shows, so the conversation can be saved
// as Markdown when it ends: the transcript, tool calls, code, prompts, and
// commands.
type sessionLog struct {
	next overlaySender

	mu      sync.Mutex
	started time.Time
	speaker string // who new agent turns are from
	order   []string
	turns   map[string]*logTurn
	items   map[string]*logItem // tool, card, and confirmation items by id
}

type logTurn struct {
	role    string // user or agent
	speaker string
	items   []*logItem
	texts   map[int]*logItem // transcript segments
}

type logItem struct {
	kind   string // text, tool, code, or command
	text   string
	label  string
	detail string
	lang   string
	status string
}

func newSessionLog(next overlaySender) *sessionLog {
	return &sessionLog{
		next:    next,
		started: time.Now(),
		speaker: "LK",
		turns:   map[string]*logTurn{},
		items:   map[string]*logItem{},
	}
}

func (l *sessionLog) Send(msg any) error {
	l.record(msg)
	return l.next.Send(msg)
}

func (l *sessionLog) turn(id, role string) *logTurn {
	t, ok := l.turns[id]
	if !ok {
		t = &logTurn{role: role, speaker: l.speaker, texts: map[int]*logItem{}}
		l.turns[id] = t
		l.order = append(l.order, id)
	}
	return t
}

func (l *sessionLog) record(msg any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch m := msg.(type) {
	case overlaySpeaker:
		l.speaker = m.Name
		if !m.Guest {
			l.speaker = "LK"
		}
	case overlayTranscript:
		t := l.turn(m.Turn, m.Role)
		item, ok := t.texts[m.Segment]
		if !ok {
			item = &logItem{kind: "text"}
			t.texts[m.Segment] = item
			t.items = append(t.items, item)
		}
		item.text = m.Text
	case overlayTool:
		item, ok := l.items[m.ID]
		if !ok {
			item = &logItem{kind: "tool"}
			l.items[m.ID] = item
			t := l.turn(m.Turn, "agent")
			t.items = append(t.items, item)
		}
		if m.Label != "" {
			item.label = m.Label
		}
		if m.Detail != "" {
			item.detail = m.Detail
		}
		item.status = m.Status
	case overlaySnippet:
		t := l.turn(m.Turn, "agent")
		t.items = append(t.items, &logItem{kind: "code", label: m.Title, lang: m.Lang, text: m.Code})
	case overlayConfirm:
		item := &logItem{kind: "command", text: m.Command, detail: m.Heading, status: "not answered"}
		l.items[m.ID] = item
		t := l.turn(m.Turn, "agent")
		t.items = append(t.items, item)
	case overlayConfirmStatus:
		if item, ok := l.items[m.ID]; ok {
			item.status = m.Status
			if m.Text != "" {
				item.status += ": " + m.Text
			}
		}
	}
}

// markdown renders the conversation, or "" if the user never spoke.
func (l *sessionLog) markdown(dir string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	userSpoke := false
	for _, t := range l.turns {
		if t.role == "user" {
			userSpoke = true
		}
	}
	if !userSpoke {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# LiveKit assistant, %s\n\nIn %s.\n", l.started.Format("January 2, 2006, 3:04 PM"), tildePath(dir))
	for _, id := range l.order {
		t := l.turns[id]
		who := t.speaker
		if t.role == "user" {
			who = "You"
		}
		fmt.Fprintf(&b, "\n**%s:**", who)
		// Text continues the speaker's line, or the text before it. After
		// a tool line, card, or command, it starts a new paragraph.
		afterText := true
		for _, it := range t.items {
			switch it.kind {
			case "text":
				if it.text == "" {
					continue
				}
				sep := " "
				if !afterText {
					sep = "\n\n"
				}
				b.WriteString(sep + it.text)
				afterText = true
				continue
			case "tool":
				line := it.label
				if it.detail != "" {
					line += ": " + it.detail
				}
				fmt.Fprintf(&b, "\n\n- %s", line)
			case "code":
				lang := it.lang
				if lang == "prompt" {
					lang = ""
				}
				fence := codeFence(it.text)
				fmt.Fprintf(&b, "\n\n%s:\n\n%s%s\n%s\n%s", it.label, fence, lang, it.text, fence)
			case "command":
				fmt.Fprintf(&b, "\n\n`%s` (%s): %s", it.text, strings.ToLower(it.detail), it.status)
			}
			afterText = false
		}
		b.WriteString("\n")
	}
	return b.String()
}

// codeFence returns a Markdown code fence longer than any run of backticks
// in code, so the code can't close it early.
func codeFence(code string) string {
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	return fence
}

// save writes the conversation to the assistant's sessions folder and returns
// the file's path, or "" if there was nothing to save.
func (l *sessionLog) save(dir string) (string, error) {
	md := l.markdown(dir)
	if md == "" {
		return "", nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	folder := filepath.Join(cache, "livekit", "assistant-sessions")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(folder, l.started.Format("2006-01-02-150405")+".md")
	return path, os.WriteFile(path, []byte(md), 0o644)
}
