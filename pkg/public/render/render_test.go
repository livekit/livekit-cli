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

package render

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestWebhookEvents(t *testing.T) {
	for _, tc := range []struct {
		name string
		fp   *oapi.LivekitFilterParams
		want string
	}{
		{"no filter", nil, "all"},
		{"empty filter", &oapi.LivekitFilterParams{}, "all"},
		{"include", &oapi.LivekitFilterParams{IncludeEvents: &[]string{"room_started", "room_finished"}}, "room_started, room_finished"},
		{"exclude", &oapi.LivekitFilterParams{ExcludeEvents: &[]string{"track_published"}}, "all except track_published"},
		{"both", &oapi.LivekitFilterParams{
			IncludeEvents: &[]string{"room_started"},
			ExcludeEvents: &[]string{"room_finished"},
		}, "room_started except room_finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, webhookEvents(tc.fp))
		})
	}
}
