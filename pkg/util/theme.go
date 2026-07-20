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

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
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
	Brand   lipgloss.TerminalColor
	Accent  lipgloss.TerminalColor
	Success lipgloss.TerminalColor
	Warning lipgloss.TerminalColor
	Error   lipgloss.TerminalColor
}

// palettes defines the semantic colors per theme. AdaptiveColor{Light, Dark}: Light is the
// shade used on light terminals, Dark on dark terminals.
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
		Brand:   lipgloss.AdaptiveColor{Light: "#002CF2", Dark: "#1FD5F9"},
		Accent:  lipgloss.AdaptiveColor{Light: "#7A15A2", Dark: "#DC85FF"},
		Success: lipgloss.AdaptiveColor{Light: "#00753B", Dark: "#23DE6B"},
		Warning: lipgloss.AdaptiveColor{Light: "#9D4D06", Dark: "#FFB752"},
		Error:   lipgloss.AdaptiveColor{Light: "#B32909", Dark: "#FF7566"},
	},
}

// Active theme state. Populated by applyTheme; switched once at startup via SetTheme. These
// are package-level so existing call sites (util.Theme, util.Accented, util.Fg, …) keep
// working; they are read at render time, which always happens after the theme is selected.
var (
	activeTheme   = ThemeDefault
	activePalette = palettes[ThemeDefault]

	// Theme is the huh form theme for the active color theme.
	Theme *huh.Theme

	Fg              lipgloss.AdaptiveColor
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
	Theme = buildHuhTheme(tn, activePalette)
	Fg = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	FormBaseStyle = Theme.Form.Base.Foreground(Fg).Padding(0, 1)
	FormHeaderStyle = FormBaseStyle.Bold(true)
}

// buildHuhTheme constructs the huh form theme. The default theme reproduces the original
// ANSI look; the livekit theme styles selection/title/cursor with the brand color.
func buildHuhTheme(tn ThemeName, p palette) *huh.Theme {
	t := huh.ThemeBase()
	switch tn {
	case ThemeLiveKit:
		// Selected action uses the brand color with black text, mirroring the LiveKit tag.
		t.Focused.Title = t.Focused.Title.Foreground(p.Brand).Bold(true)
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("0")).Background(p.Brand).Bold(true)
	case ThemeDefault:
		fallthrough
	default:
		t = huh.ThemeBase16()
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
func Brand() lipgloss.TerminalColor   { return activePalette.Brand }
func Accent() lipgloss.TerminalColor  { return activePalette.Accent }
func Success() lipgloss.TerminalColor { return activePalette.Success }
func Warning() lipgloss.TerminalColor { return activePalette.Warning }
func Error() lipgloss.TerminalColor   { return activePalette.Error }

// Accented renders text in the active theme's title style (brand color under livekit).
func Accented(text string) string {
	return Theme.Focused.Title.Render(text)
}

// Dimmed renders text in the active theme's muted/description style.
func Dimmed(text string) string {
	return Theme.Focused.Description.Render(text)
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
