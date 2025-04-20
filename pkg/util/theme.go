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
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var (
	Theme = func() *huh.Theme {
		t := huh.ThemeBase16()
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("4"))
		t.Focused.TextInput.Cursor.Foreground(lipgloss.Color("4"))
		return t
	}()

	Accented = func(text string) string {
		return Theme.Focused.Title.Render(text)
	}
	Dimmed = func(text string) string {
		return Theme.Focused.Description.Render(text)
	}

	Fg              = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	FormBaseStyle   = Theme.Form.Base.Foreground(Fg).Padding(0, 1)
	FormHeaderStyle = FormBaseStyle.Bold(true)
)
