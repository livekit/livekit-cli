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

// Package overlay shows the assistant's full-screen overlay: a native window
// that blurs the desktop behind a transparent web view running ui/index.html.
//
// Go drives the page with JSON messages (Send) and receives the page's
// messages as Events. The page protocol:
//
//	Go → page   {"type":"state","state":"connecting|idle|listening|thinking|speaking","label":"optional"}
//	            {"type":"bands","bands":[0..1, ...]}
//	            {"type":"transcript","turn":"...","role":"user|agent","segment":0,"text":"..."}
//	            {"type":"snippet","turn":"...","id":"...","title":"...","lang":"python|shell","code":"...","copied":bool}
//	            {"type":"copied","id":"..."}
//	            {"type":"tool","turn":"...","id":"...","label":"...","detail":"...","status":"running|done|error"}
//	            {"type":"notice","text":"...","sticky":bool}
//	            {"type":"error","title":"...","detail":"...","fix":"command"}
//	            {"type":"speaker","name":"...","guest":bool}
//	            {"type":"pipeline",...} {"type":"eot",...} {"type":"metrics",...} {"type":"usage",...} (X-ray)
//	            {"type":"dismiss"}
//	            {"type":"clear"}
//	            {"type":"confirm","turn":"...","id":"...","heading":"...","title":"...","options":[{"id":"...","name":"..."}]}
//	            {"type":"confirm_done","id":"...","option":"..."}
//	page → Go   {"type":"ready"} {"type":"copy","id":"..."} {"type":"replay"} {"type":"close"}
//	            {"type":"confirm","id":"...","option":"..."} {"type":"copy_text","text":"..."}
//	            {"type":"back"} {"type":"mute","on":bool}
//
// A transcript message carries the full text so far of one segment (a
// paragraph) of a turn. Snippets are added to their turn after the segments
// before them. The page also sends {"type":"peek","on":bool}, which the
// overlay handles itself by fading the desktop blur.
//
// Only macOS is supported for now.
package overlay

import (
	_ "embed"
	"errors"
	"sync"
)

//go:embed ui/index.html
var pageHTML string

// ErrUnsupported is returned by Run on platforms without an overlay.
var ErrUnsupported = errors.New("the assistant overlay is only supported on macOS")

// Options configure the overlay window.
type Options struct {
	// Windowed opens a regular floating window instead of covering the screen.
	Windowed bool
	// Inspectable enables the Safari Web Inspector for the page.
	Inspectable bool
}

// Event is a message from the overlay page.
type Event struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	On   bool   `json:"on,omitempty"`
	// Option is the chosen option of a confirm event.
	Option string `json:"option,omitempty"`
	// Text is the text of a copy_text event.
	Text string `json:"text,omitempty"`
}

// Overlay is a single overlay window. A process can run one at a time.
type Overlay struct {
	opts      Options
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
}

// New creates an overlay. Call Run to show it.
func New(opts Options) *Overlay {
	return &Overlay{
		opts:   opts,
		events: make(chan Event, 64),
		done:   make(chan struct{}),
	}
}

// Events returns messages from the page. Messages are dropped if nobody reads
// them.
func (o *Overlay) Events() <-chan Event {
	return o.events
}

// Done is closed after the window closes and Run returns.
func (o *Overlay) Done() <-chan struct{} {
	return o.done
}

func (o *Overlay) emit(ev Event) {
	select {
	case o.events <- ev:
	default:
	}
}
