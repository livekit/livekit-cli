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

package main

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var (
	normalFg      = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	normalBg      = lipgloss.AdaptiveColor{Light: "20", Dark: "0"}
	dimFg         = lipgloss.AdaptiveColor{Light: "", Dark: "243"}
	placeholderFg = lipgloss.AdaptiveColor{Light: "248", Dark: "238"}
	cyan          = lipgloss.AdaptiveColor{Light: "#06B7DB", Dark: "#1FD5F9"}
	red           = lipgloss.AdaptiveColor{Light: "#CE4A3B", Dark: "#FF6352"}
	yellow        = lipgloss.AdaptiveColor{Light: "#DB9406", Dark: "#F9B11F"}
	green         = lipgloss.AdaptiveColor{Light: "#036D26", Dark: "#06DB4D"}

	theme = func() *huh.Theme {
		t := huh.ThemeBase16()
		return t
	}()
	themeBranded = func() *huh.Theme {
		t := huh.ThemeBase()

		t.Focused.Base = t.Focused.Base.BorderForeground(lipgloss.Color("238"))
		t.Focused.Title = t.Focused.Title.Foreground(cyan).Bold(true)
		t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(cyan).Bold(true).MarginBottom(1)
		t.Focused.Directory = t.Focused.Directory.Foreground(cyan)
		t.Focused.Description = t.Focused.Description.Foreground(dimFg)
		t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(red)
		t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(red)
		t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(yellow)
		t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(yellow)
		t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(yellow)
		t.Focused.Option = t.Focused.Option.Foreground(normalFg)
		t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(yellow)
		t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(green)
		t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(green).SetString("✓ ")
		t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(dimFg).SetString("• ")
		t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(normalFg)
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(normalBg).Background(cyan)
		t.Focused.Next = t.Focused.FocusedButton
		t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(normalFg).Background(lipgloss.AdaptiveColor{Light: "252", Dark: "237"})

		t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(placeholderFg)
		t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(yellow)

		t.Blurred = t.Focused
		t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()

		return t
	}()
)
