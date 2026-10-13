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

package util

import (
	"regexp"
	"strings"
)

// CommandArgs is the command line lk was invoked with (os.Args); run sets
// it. NextPageHint repeats it with the next page's cursor, so a hint names
// the whole command, filters and all, rather than just the flag to add.
var CommandArgs []string

// NextPageHint is the accented command that reads the page after a listing
// that stopped at cursor: the invocation in CommandArgs with its --cursor
// replaced. Without CommandArgs it is just the flag to add.
func NextPageHint(cursor string) string {
	if len(CommandArgs) == 0 {
		return Accented("--cursor " + cursor)
	}
	return Accented(NextPageCommand(CommandArgs, cursor))
}

// nextPageDropped are flags NextPageCommand leaves out, with their values:
// the prior cursor, which the new one replaces, and the API credentials, so a
// secret typed on the command line isn't echoed into a terminal or CI log.
// Cursor-paginated commands sign in as the user, so they never need them.
var nextPageDropped = []string{"cursor", "api-key", "api-secret"}

// NextPageCommand returns args, run as lk, with any --cursor and credential
// flags removed and --cursor set to cursor, quoted for a POSIX shell.
func NextPageCommand(args []string, cursor string) string {
	parts := []string{"lk"}
	if len(args) > 1 {
		rest := args[1:]
	args:
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			for _, name := range nextPageDropped {
				switch a {
				case "--" + name, "-" + name:
					i++ // and its value
					continue args
				}
				if strings.HasPrefix(a, "--"+name+"=") || strings.HasPrefix(a, "-"+name+"=") {
					continue args
				}
			}
			parts = append(parts, shellQuote(a))
		}
	}
	return strings.Join(append(parts, "--cursor", shellQuote(cursor)), " ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell when it holds anything but characters
// a shell takes literally.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
