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
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

// browserImageID is the Kitty image each browser frame replaces. Placeholder
// cells name it through their 24-bit foreground color.
const browserImageID = 0x4c4b01

// The browser viewport is the terminal grid at an assumed cell size; each frame
// is stretched over the whole grid, so only the ratio has to be right.
const (
	browserCellWidth  = 10
	browserCellHeight = 20
)

// kittyGraphicsSupported reports whether the terminal renders Kitty graphics
// Unicode placeholders. tmux does not pass the graphics protocol through.
func kittyGraphicsSupported() bool {
	if os.Getenv("TMUX") != "" {
		return false
	}
	return os.Getenv("KITTY_WINDOW_ID") != "" ||
		os.Getenv("TERM") == "xterm-kitty" ||
		os.Getenv("TERM_PROGRAM") == "ghostty"
}

// dashboardSessionToken returns the signed-in user's session from
// `lk cloud auth --experimental-auth`, which the dashboard accepts as its own
// browser session.
func dashboardSessionToken() string {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return ""
	}
	user := conf.GetUser(conf.DefaultUser)
	if !user.SessionValid() {
		return ""
	}
	return user.SessionToken
}

// dashboardSessionCookie is the dashboard's NextAuth session cookie, which is
// namespaced separately on staging.
func dashboardSessionCookie(host string) string {
	if strings.HasSuffix(host, ".staging.livekit.io") {
		return "__Secure-authjs.browser-session-token.staging"
	}
	return "__Secure-authjs.browser-session-token"
}

// dashboardBrowser is a headless Chrome tab signed in to the dashboard. frames
// holds at most the latest undrawn frame.
type dashboardBrowser struct {
	ctx    context.Context
	cancel context.CancelFunc
	frames chan image.Image
}

// openDashboardBrowser opens pageURL signed in with token and starts streaming
// its frames. A page that redirects elsewhere (e.g. to login) is an error.
func openDashboardBrowser(pageURL, token string, cols, rows int) (*dashboardBrowser, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	ctx, cancelTab := chromedp.NewContext(allocCtx)
	b := &dashboardBrowser{
		ctx:    ctx,
		cancel: func() { cancelTab(); cancelAlloc() },
		frames: make(chan image.Image, 1),
	}

	chromedp.ListenTarget(ctx, func(ev any) {
		frame, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		go func() {
			_ = chromedp.Run(ctx, page.ScreencastFrameAck(frame.SessionID))
		}()
		data, err := base64.StdEncoding.DecodeString(frame.Data)
		if err != nil {
			return
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		select {
		case <-b.frames:
		default:
		}
		b.frames <- img
	})

	var landed string
	if err := chromedp.Run(ctx,
		network.SetCookie(dashboardSessionCookie(u.Hostname()), token).
			WithDomain(u.Hostname()).WithPath("/").WithSecure(true).WithHTTPOnly(true),
		b.viewport(cols, rows),
		chromedp.Navigate(pageURL),
		chromedp.Location(&landed),
	); err != nil {
		b.close()
		return nil, err
	}
	if l, err := url.Parse(landed); err != nil || l.Path != u.Path {
		b.close()
		return nil, fmt.Errorf("dashboard redirected to %s", landed)
	}
	if err := chromedp.Run(ctx, page.StartScreencast().WithFormat(page.ScreencastFormatJpeg).WithQuality(80)); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

// viewport sizes the page to the grid by sizing the window around it: the
// screencast captures the window, not an emulated viewport.
func (b *dashboardBrowser) viewport(cols, rows int) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var frame [2]int64
		if err := chromedp.Evaluate(`[outerWidth - innerWidth, outerHeight - innerHeight]`, &frame).Do(ctx); err != nil {
			return err
		}
		id, _, err := browser.GetWindowForTarget().Do(ctx)
		if err != nil {
			return err
		}
		return browser.SetWindowBounds(id, &browser.Bounds{
			Width:  int64(cols*browserCellWidth) + frame[0],
			Height: int64(rows*browserCellHeight) + frame[1],
		}).Do(ctx)
	})
}

func (b *dashboardBrowser) close() {
	b.cancel()
}

// do runs actions off the UI loop; the next frame shows their effect.
func (b *dashboardBrowser) do(actions ...chromedp.Action) tea.Cmd {
	return func() tea.Msg {
		_ = chromedp.Run(b.ctx, actions...)
		return nil
	}
}

func (b *dashboardBrowser) resize(cols, rows int) tea.Cmd {
	return b.do(b.viewport(cols, rows))
}

// mouse forwards a terminal mouse event to the page, at the center of the cell.
func (b *dashboardBrowser) mouse(msg tea.MouseMsg) tea.Cmd {
	m := msg.Mouse()
	x := float64(m.X*browserCellWidth + browserCellWidth/2)
	y := float64(m.Y*browserCellHeight + browserCellHeight/2)
	button := map[tea.MouseButton]input.MouseButton{
		tea.MouseLeft:   input.Left,
		tea.MouseMiddle: input.Middle,
		tea.MouseRight:  input.Right,
	}[m.Button]
	if button == "" {
		button = input.None
	}
	switch msg.(type) {
	case tea.MouseClickMsg:
		return b.do(chromedp.MouseEvent(input.MousePressed, x, y, chromedp.ButtonType(button), chromedp.ClickCount(1)))
	case tea.MouseReleaseMsg:
		return b.do(chromedp.MouseEvent(input.MouseReleased, x, y, chromedp.ButtonType(button), chromedp.ClickCount(1)))
	case tea.MouseMotionMsg:
		return b.do(chromedp.MouseEvent(input.MouseMoved, x, y, chromedp.ButtonType(button)))
	case tea.MouseWheelMsg:
		d := browserWheel[m.Button]
		return b.do(input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(d[0]).WithDeltaY(d[1]))
	}
	return nil
}

var browserWheel = map[tea.MouseButton][2]float64{
	tea.MouseWheelUp:    {0, -100},
	tea.MouseWheelDown:  {0, 100},
	tea.MouseWheelLeft:  {-100, 0},
	tea.MouseWheelRight: {100, 0},
}

var browserKeys = map[rune]string{
	tea.KeyEnter:     kb.Enter,
	tea.KeyBackspace: kb.Backspace,
	tea.KeyTab:       kb.Tab,
	tea.KeyEscape:    kb.Escape,
	tea.KeyDelete:    kb.Delete,
	tea.KeyUp:        kb.ArrowUp,
	tea.KeyDown:      kb.ArrowDown,
	tea.KeyLeft:      kb.ArrowLeft,
	tea.KeyRight:     kb.ArrowRight,
	tea.KeyPgUp:      kb.PageUp,
	tea.KeyPgDown:    kb.PageDown,
	tea.KeyHome:      kb.Home,
	tea.KeyEnd:       kb.End,
}

// key types a terminal key press into the page.
func (b *dashboardBrowser) key(msg tea.KeyPressMsg) tea.Cmd {
	keys := msg.Text
	if keys == "" {
		keys = browserKeys[msg.Code]
	}
	if keys == "" {
		return nil
	}
	return b.do(chromedp.KeyEvent(keys))
}

// nextFrame waits for the page to draw something new.
func (b *dashboardBrowser) nextFrame() tea.Cmd {
	return func() tea.Msg {
		select {
		case img := <-b.frames:
			return browserFrameMsg{img: img}
		case <-b.ctx.Done():
			return nil
		}
	}
}

type browserOpenedMsg struct {
	browser *dashboardBrowser
	err     error
}

type browserFrameMsg struct {
	img image.Image
}

// kittyTransmit replaces the browser image with img, placed virtually over
// cols x rows cells.
func kittyTransmit(img image.Image, cols, rows int) (string, error) {
	var b strings.Builder
	err := kitty.EncodeGraphics(&b, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.RGB,
		Compression:      kitty.Zlib,
		ImageWidth:       img.Bounds().Dx(),
		ImageHeight:      img.Bounds().Dy(),
		ID:               browserImageID,
		PlacementID:      1,
		Columns:          cols,
		Rows:             rows,
		VirtualPlacement: true,
		Quiet:            2,
		Chunk:            true,
	})
	return b.String(), err
}

// kittyPlaceholder draws the cols x rows cells of the virtual placement. Only a
// row's first cell carries its column; the terminal infers the rest from the
// cell to their left.
func kittyPlaceholder(cols, rows int) string {
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", browserImageID>>16&0xff, browserImageID>>8&0xff, browserImageID&0xff)
	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		b.WriteString(color)
		for c := range cols {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(r))
			if c == 0 {
				b.WriteRune(kitty.Diacritic(0))
			}
		}
		b.WriteString("\x1b[39m")
		lines[r] = b.String()
	}
	return strings.Join(lines, "\n")
}
