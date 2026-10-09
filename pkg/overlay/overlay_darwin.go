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

package overlay

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "overlay_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"unsafe"
)

// Cocoa's event loop must run on the process's main thread. Locking here, in
// init, keeps the main goroutine (and so every command's Action) on it.
func init() {
	runtime.LockOSThread()
}

var (
	activeMu sync.Mutex
	active   *Overlay
)

// Run shows the overlay and blocks until it closes, either from the page
// (Esc), Close, or ctx being done. It must be called from the main goroutine.
func (o *Overlay) Run(ctx context.Context) error {
	if C.lk_overlay_is_main_thread() == 0 {
		return errors.New("overlay: Run must be called on the main thread")
	}
	activeMu.Lock()
	if active != nil {
		activeMu.Unlock()
		return errors.New("overlay: already running")
	}
	active = o
	activeMu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			o.Close()
		case <-o.done:
		}
	}()

	html := C.CString(pageHTML)
	defer C.free(unsafe.Pointer(html))
	C.lk_overlay_run(html, cbool(o.opts.Windowed), cbool(o.opts.Inspectable))

	activeMu.Lock()
	active = nil
	activeMu.Unlock()
	close(o.done)
	return nil
}

// Send delivers a message to the page. Messages sent before the page's
// "ready" event are lost.
func (o *Overlay) Send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	cmsg := C.CString(string(b))
	defer C.free(unsafe.Pointer(cmsg))
	C.lk_overlay_send(cmsg)
	return nil
}

// Close fades the overlay out and makes Run return.
func (o *Overlay) Close() {
	o.closeOnce.Do(func() { C.lk_overlay_close() })
}

//export lkOverlayMessage
func lkOverlayMessage(msg *C.char) {
	var ev Event
	if err := json.Unmarshal([]byte(C.GoString(msg)), &ev); err != nil {
		return
	}
	activeMu.Lock()
	o := active
	activeMu.Unlock()
	if o == nil {
		return
	}
	switch ev.Type {
	case "close":
		o.Close()
	case "peek":
		C.lk_overlay_set_peek(cbool(ev.On))
	}
	o.emit(ev)
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
