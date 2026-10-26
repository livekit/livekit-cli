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
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// maxKeyConfirmations is how many times removeMember asks the user to confirm
// a key count: once, and once more if the count changes before the removal
// goes ahead.
const maxKeyConfirmations = 2

// removeMember removes userID with remove, which also deletes the API keys
// they own, and returns how many it deleted. remove is called with
// confirmedNumKeys 0 first. If the member owns keys, the Public API refuses
// with their number; once the user confirms it (or passed --yes), remove is
// called again with that number. A removal that would leave a project without
// an API key is refused without asking.
func removeMember(ctx context.Context, cmd *cli.Command, userID string, remove func(confirmedNumKeys uint32) (uint32, error)) (uint32, error) {
	var confirmed uint32
	for asked := 0; ; asked++ {
		numKeys, err := remove(confirmed)
		removal, ok := public.MemberRemovalNotConfirmed(err)
		if !ok {
			return numKeys, err
		}
		if removal.LeavesProjectWithoutKey() {
			return 0, lastKeyError(userID, removal)
		}
		if asked == maxKeyConfirmations {
			return 0, fmt.Errorf("%s's API keys changed again (they now own %d); nothing was removed, run the command again", userID, removal.NumKeys)
		}
		if err := confirmKeyDeletion(ctx, cmd, userID, removal.NumKeys, asked > 0); err != nil {
			return 0, err
		}
		confirmed = removal.NumKeys
	}
}

// confirmKeyDeletion asks the user to agree to deleting the numKeys API keys
// userID owns. --yes agrees; without it a non-interactive run fails, since the
// deletion can't be undone.
func confirmKeyDeletion(ctx context.Context, cmd *cli.Command, userID string, numKeys uint32, changed bool) error {
	if cmd.Bool("yes") {
		return nil
	}
	if !isInteractive() {
		return fmt.Errorf("removing %s deletes the %d API key(s) they own, and anything using them stops working; re-run with --yes to remove them", userID, numKeys)
	}
	title := fmt.Sprintf("Remove %s and delete the %d API key(s) they own?", userID, numKeys)
	if changed {
		title = fmt.Sprintf("%s's API keys changed. Remove %s and delete the %d API key(s) they own now?", userID, userID, numKeys)
	}
	confirmed := false
	if err := huh.NewForm(huh.NewGroup(util.Confirm().
		Title(title).
		Description("Anything using those keys stops working.").
		Value(&confirmed).
		WithTheme(util.FormTheme()))).
		RunWithContext(ctx); err != nil {
		return err
	}
	if !confirmed {
		return errors.New("aborted")
	}
	return nil
}

// lastKeyError says a removal is refused because it would leave projects
// without an API key, and what to do first.
func lastKeyError(userID string, removal *public.MemberRemoval) error {
	projects := make([]string, 0, len(removal.LastKeyProjectNames)+1)
	for _, name := range removal.LastKeyProjectNames {
		projects = append(projects, fmt.Sprintf("%q", name))
	}
	if removal.NumPrivateLastKeyProjects > 0 {
		projects = append(projects, fmt.Sprintf("%d private project(s)", removal.NumPrivateLastKeyProjects))
	}
	list := projects[len(projects)-1]
	if len(projects) > 1 {
		list = strings.Join(projects[:len(projects)-1], ", ") + " and " + list
	}
	return fmt.Errorf("can't remove %s: it would delete the last API key of %s; a project without an API key stops handling inbound SIP calls and webhooks, "+
		"so have a member who is staying create a replacement API key there first", userID, list)
}

// renderMemberRemoved reports a removal and the API keys it deleted.
func renderMemberRemoved(cmd *cli.Command, userID string, numKeys uint32) error {
	if cmd.Bool("json") {
		return util.PrintJSONTo(out.ResultWriter(), map[string]any{"userId": userID, "removed": true, "numKeys": numKeys})
	}
	if numKeys == 0 {
		out.Statusf("Removed member %s", util.Accented(userID))
		return nil
	}
	out.Statusf("Removed member %s and deleted the %d API key(s) they owned", util.Accented(userID), numKeys)
	return nil
}
