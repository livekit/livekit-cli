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

package tokenauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/livekit/protocol/auth"
)

// CheckGrants reports an error if a minted token lacks any claim that was
// requested. The Public API drops claims it doesn't know (e.g. a grant newer
// than the server), which would otherwise surface later as a confusing
// permission error from LiveKit. The token's signature isn't checked; only
// the issuer could, and the caller is the one asking.
func CheckGrants(token string, want *auth.ClaimGrants) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("minted token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decoding minted token: %w", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		return fmt.Errorf("decoding minted token: %w", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return err
	}
	var wantMap map[string]any
	if err := json.Unmarshal(wantJSON, &wantMap); err != nil {
		return err
	}
	if missing := missingClaims("", wantMap, got); len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("the server did not grant %s; it may not support them yet", strings.Join(missing, ", "))
	}
	return nil
}

// missingClaims lists the paths in want whose values are absent from, or
// differ in, got.
func missingClaims(prefix string, want, got map[string]any) []string {
	var out []string
	for k, wv := range want {
		path := prefix + k
		gv, ok := got[k]
		if wm, isMap := wv.(map[string]any); isMap {
			gm, _ := gv.(map[string]any)
			out = append(out, missingClaims(path+".", wm, gm)...)
			continue
		}
		if !ok || !reflect.DeepEqual(wv, gv) {
			out = append(out, path)
		}
	}
	return out
}
