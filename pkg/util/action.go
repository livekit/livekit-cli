// Copyright 2025 LiveKit, Inc.
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
	"context"

	"github.com/charmbracelet/huh/spinner"
)

// Call an action and show a spinner while waiting for it to finish.
func Await(title string, ctx context.Context, action func(ctx context.Context) error) error {
	return spinner.New().
		Title(" " + title).
		ActionWithErr(action).
		Type(spinner.Pulse).
		Style(Theme.Focused.Title).
		Context(ctx).
		Run()
}
