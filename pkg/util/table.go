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
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

func CreateTable() *table.Table {
	styleFunc := func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return FormHeaderStyle
		}
		return FormBaseStyle
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(Theme.Form.Foreground(Fg)).
		StyleFunc(styleFunc)

	return t
}
