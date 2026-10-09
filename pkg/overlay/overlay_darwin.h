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

#ifndef LK_OVERLAY_DARWIN_H
#define LK_OVERLAY_DARWIN_H

// lk_overlay_run opens the overlay window and runs the Cocoa event loop until
// lk_overlay_close is called. It must be called on the process's main thread.
void lk_overlay_run(const char *html, int windowed, int inspectable);

// lk_overlay_send passes a JSON message to window.lk.receive in the overlay
// page. Safe from any thread.
void lk_overlay_send(const char *json);

// lk_overlay_close fades the window out and stops the event loop. Safe from
// any thread.
void lk_overlay_close(void);

// lk_overlay_set_peek fades the desktop blur out (on) or back in (off), so the
// user can see the apps behind the overlay. Safe from any thread.
void lk_overlay_set_peek(int on);

// lk_overlay_is_main_thread reports whether the caller is on the main thread.
int lk_overlay_is_main_thread(void);

#endif
