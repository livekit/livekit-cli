// Copyright 2024 LiveKit, Inc.
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

package util

import (
	"fmt"
	"image/color"
	"os"
	"sync"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// ThemeName identifies a color theme. The active theme is selected once at startup
// (see SetTheme) from the value persisted in the CLI config.
type ThemeName string

const (
	// ThemeDefault uses only ANSI palette colors, so it adapts to the user's terminal
	// color scheme. This is the original look.
	ThemeDefault ThemeName = "default"
	// ThemeLiveKit uses the LiveKit brand palette (truecolor hex with light/dark variants).
	ThemeLiveKit ThemeName = "livekit"
)

// ValidThemes lists the selectable theme names, for validation and help text.
var ValidThemes = []ThemeName{ThemeDefault, ThemeLiveKit}

// palette holds a theme's semantic colors. Each is adaptive (light/dark) so it renders
// legibly on either terminal background.
type palette struct {
	Brand   color.Color
	Accent  color.Color
	Success color.Color
	Warning color.Color
	Error   color.Color
}

// adaptive is a color that resolves to its light or dark variant depending on the
// terminal background. Lip Gloss v2 dropped AdaptiveColor in favour of plain
// color.Color values plus a background flag the caller supplies; resolving in
// RGBA keeps that flag out of every style definition, and lets the styles be
// built before the background is known (see DetectBackground).
type adaptive struct{ light, dark color.Color }

func (a adaptive) RGBA() (r, g, b, al uint32) {
	if hasDarkBackground() {
		return a.dark.RGBA()
	}
	return a.light.RGBA()
}

func adaptiveColor(light, dark string) color.Color {
	return adaptive{light: lipgloss.Color(light), dark: lipgloss.Color(dark)}
}

// hasDarkBackground reports whether the terminal has a dark background, querying
// it once per process. It defaults to dark when the query fails or the terminal
// isn't interactive, matching Lip Gloss v1's behaviour.
var hasDarkBackground = sync.OnceValue(func() bool {
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
})

// DetectBackground resolves the terminal background now, so no later render has
// to. The query puts stdin in raw mode and waits for the terminal to answer,
// which must not happen once a Bubble Tea program or a huh form owns the input;
// running it before any command does keeps it out of their way. It's a no-op on
// a non-interactive terminal.
func DetectBackground() {
	hasDarkBackground()
}

// palettes defines the semantic colors per theme. In adaptiveColor(light, dark) the
// first shade is used on light terminals, the second on dark ones.
var palettes = map[ThemeName]palette{
	// Default: ANSI only (normal on light, bright on dark). Adapts to the terminal palette.
	ThemeDefault: {
		Brand:   lipgloss.Color("6"), // cyan
		Accent:  lipgloss.Color("5"), // magenta
		Success: lipgloss.Color("2"), // green
		Warning: lipgloss.Color("3"), // yellow
		Error:   lipgloss.Color("1"), // red
	},
	// LiveKit: brand truecolor palette.
	ThemeLiveKit: {
		Brand:   adaptiveColor("#002CF2", "#1FD5F9"),
		Accent:  adaptiveColor("#7A15A2", "#DC85FF"),
		Success: adaptiveColor("#00753B", "#23DE6B"),
		Warning: adaptiveColor("#9D4D06", "#FFB752"),
		Error:   adaptiveColor("#B32909", "#FF7566"),
	},
}

// Active theme state. Populated by applyTheme; switched once at startup via SetTheme. These
// are package-level so existing call sites (util.Theme, util.Accented, util.Fg, …) keep
// working; they are read at render time, which always happens after the theme is selected.
var (
	activeTheme   = ThemeDefault
	activePalette = palettes[ThemeDefault]

	// Theme holds the huh form styles for the active color theme. Pass it to a form
	// with FormTheme(); read it directly for individual styles.
	Theme *huh.Styles

	Fg              color.Color
	FormBaseStyle   lipgloss.Style
	FormHeaderStyle lipgloss.Style
)

func init() {
	applyTheme(ThemeDefault)
}

// SetTheme selects the active theme by name. An empty name resolves to the default. It
// returns an error for any other unrecognized name (used to validate `lk set-theme`).
func SetTheme(name string) error {
	tn := ThemeName(name)
	if name == "" {
		tn = ThemeDefault
	}
	if _, ok := palettes[tn]; !ok {
		return fmt.Errorf("unknown theme %q (valid: %s, %s)", name, ThemeDefault, ThemeLiveKit)
	}
	applyTheme(tn)
	return nil
}

// applyTheme installs a theme's huh form theme and derived styles into the package vars.
func applyTheme(tn ThemeName) {
	activeTheme = tn
	activePalette = palettes[tn]
	Fg = adaptiveColor("235", "252")
	Theme = buildHuhTheme(tn, activePalette)
	FormBaseStyle = Theme.Form.Base.Foreground(Fg).Padding(0, 1)
	FormHeaderStyle = FormBaseStyle.Bold(true)
}

// buildHuhTheme constructs the huh form theme. The default theme reproduces the original
// ANSI look; the livekit theme styles selection/title/cursor with the brand color.
func buildHuhTheme(tn ThemeName, p palette) *huh.Styles {
	// huh v2 themes take the terminal's background brightness. Both bases we
	// build on use only ANSI palette colors and ignore the flag, and the colors
	// we layer on top adapt on their own, so the value passed here is moot —
	// which is what lets the theme be built at init, before detection runs.
	t := huh.ThemeBase(true)
	switch tn {
	case ThemeLiveKit:
		// Selected action uses the brand color with black text, mirroring the LiveKit tag.
		t.Focused.Title = t.Focused.Title.Foreground(p.Brand).Bold(true)
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("0")).Background(p.Brand).Bold(true)
	case ThemeDefault:
		fallthrough
	default:
		t = huh.ThemeBase16(true)
		// ANSI: white text on a blue selection, base16 defaults elsewhere.
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("4"))
	}

	// The inactive (blurred) confirm button gets a transparent background so the
	// active button — which carries a solid fill — is unambiguously the selected
	// one. Without this it defaults to a filled block that reads as also-active.
	transparentButton := func(s lipgloss.Style) lipgloss.Style {
		return s.Background(lipgloss.NoColor{}).Bold(false)
	}
	t.Focused.BlurredButton = transparentButton(t.Focused.BlurredButton)
	t.Blurred.BlurredButton = transparentButton(t.Blurred.BlurredButton)
	t.Blurred.FocusedButton = transparentButton(t.Blurred.FocusedButton)

	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(p.Accent).SetString("▶︎ ")
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(p.Accent)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(p.Accent).SetString("[x] ")
	t.Focused.UnselectedPrefix = t.Focused.UnselectedPrefix.SetString("[ ] ")
	t.Focused.MultiSelectSelector = t.Focused.SelectSelector
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(p.Accent).SetString("▶︎")
	t.Form.Base = t.Form.Base.BorderForeground(Fg)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(p.Error).SetString(" ×")
	t.Focused.ErrorMessage = t.Focused.ErrorIndicator

	t.Blurred.SelectSelector = t.Focused.SelectSelector.SetString("  ")
	t.Blurred.SelectedOption = t.Focused.SelectedOption
	t.Blurred.SelectedPrefix = t.Focused.SelectedPrefix
	t.Blurred.UnselectedPrefix = t.Focused.UnselectedPrefix
	t.Blurred.MultiSelectSelector = t.Focused.MultiSelectSelector.SetString("  ")
	t.Blurred.TextInput.Prompt = t.Focused.TextInput.Prompt.SetString(" ")
	t.Blurred.ErrorIndicator = t.Focused.ErrorIndicator
	t.Blurred.ErrorMessage = t.Focused.ErrorMessage

	return t
}

// Semantic color accessors. They read the active palette at call time, so they reflect the
// selected theme even when used to build styles lazily.
func Brand() color.Color   { return activePalette.Brand }
func Accent() color.Color  { return activePalette.Accent }
func Success() color.Color { return activePalette.Success }
func Warning() color.Color { return activePalette.Warning }
func Error() color.Color   { return activePalette.Error }

// FormTheme adapts the active theme to huh v2's Theme interface, which asks for a
// func(isDark bool) *Styles. Our styles already resolve light/dark per color (see
// adaptive), so the flag is ignored.
func FormTheme() huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles { return Theme })
}

// Accented renders text in the active theme's title style (brand color under livekit).
func Accented(text string) string {
	return Theme.Focused.Title.Render(text)
}

// Dimmed renders text in the active theme's muted/description style.
func Dimmed(text string) string {
	return Theme.Focused.Description.Render(text)
}

// Warn renders text in the active theme's Warning style
func Warn(text string) string {
	return lipgloss.NewStyle().Foreground(activePalette.Warning).Render(text)
}

// Warn renders text in the active theme's Error style
func Err(text string) string {
	return lipgloss.NewStyle().Foreground(activePalette.Error).Render(text)
}

// Hyperlink wraps label in an OSC 8 terminal hyperlink pointing at url. Terminals
// that support OSC 8 render label as a clickable link; others ignore the escape
// and show label unchanged. Gate calls on an interactive terminal (see
// Printer.Interactive) so the escape never leaks into piped/redirected output.
func Hyperlink(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// Confirm is a yes/no prompt styled by the active theme. It uses huh's built-in
// confirm field, which supports y/n quick entry (Accept: y/Y, Reject: n/N) and
// renders both choices as side-by-side buttons.
func Confirm() *huh.Confirm {
	return huh.NewConfirm()
}
