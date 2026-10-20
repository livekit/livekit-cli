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
	"cmp"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

// TestCredentialResolution checks that for any mix of auth-mode flags,
// --project, inline credentials (flags or LIVEKIT_* env vars), configured
// projects, default project, signed-in session and LIVEKIT_API_URL, the request
// `lk room list` sends carries the credential and project lk's documented
// precedence picks, or lk fails without naming a project. No request may name
// a project the user didn't pass, configure as default, or give credentials
// for.
//
// Each case runs lk as its own process (the test binary, installed as lk by
// TestMain) against a fakeCloud. rapid shrinks a failing case to a minimal one
// and logs its draws, the lk command line, its env and its CLI config; rerun
// it with the -rapid.failfile or -rapid.seed flag it prints.
func TestCredentialResolution(t *testing.T) {
	lk, err := exec.LookPath("lk")
	if err != nil {
		t.Fatalf("lk is not on PATH; TestMain installs it: %v", err)
	}
	rapid.Check(t, func(t *rapid.T) {
		c := drawCredentialCase(t)
		if bug, ok := c.knownBug(); ok {
			t.Skip(bug.message())
		}

		home, err := os.MkdirTemp("", "lk-credentials")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(home)
		if err := c.writeConfig(home); err != nil {
			t.Fatal(err)
		}
		conf, _ := os.ReadFile(filepath.Join(home, ".livekit", "cli-config.yaml"))

		cloud := newFakeCloud()
		defer cloud.Close()
		for _, name := range []string{"one", "two", "flag", "env"} {
			cloud.respond("POST http://"+name+".test/twirp/livekit.RoomService/ListRooms", http.StatusOK, "")
		}
		cloud.respond("GET http://publicapi.test/v0/projects", http.StatusOK,
			`{"items":[{"id":"p_1","name":"one","url":"ws://one.test"},{"id":"p_2","name":"two","url":"ws://two.test"}]}`)

		cmd := exec.Command(lk, c.args()...)
		cmd.Dir = home
		cmd.Env = append(c.env(), "HOME="+home, "USERPROFILE="+home, "HTTP_PROXY="+cloud.URL, "HTTPS_PROXY="+cloud.URL, "LK_NO_SKILLS_PROMPT=1")
		if root, ok := os.LookupEnv("SYSTEMROOT"); ok {
			cmd.Env = append(cmd.Env, "SYSTEMROOT="+root) // Windows networking needs it
		}
		output, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		requests := cloud.requests()
		t.Logf("$ %s lk %s\ncli-config.yaml:\n%s\nrequests:\n%s\noutput:\n%s",
			strings.Join(c.env(), " "), strings.Join(c.args(), " "), conf, strings.Join(requests, "\n"), output)

		allowed := c.namedProjects()
		var first string
		for _, line := range requests {
			project, ok := requestProject(line)
			if !ok {
				continue
			}
			if !slices.Contains(allowed, project) {
				t.Fatalf("request names project %q, but only %q were named: %s", project, allowed, line)
			}
			if first == "" {
				first = line
			}
		}

		want, ok := c.want()
		switch {
		case !ok && first != "":
			t.Fatalf("want lk to fail without naming a project, got request: %s", first)
		case !ok && exitErr == nil:
			t.Fatalf("want lk to fail, but it exited 0")
		case ok && first != want:
			t.Fatalf("want first project request %q, got %q", want, first)
		}
	})
}

// credentialCase is one way of invoking lk. The values come from small fixed
// sets, so that cases collide on the same project and user names.
type credentialCase struct {
	userAuth, legacyAuth bool
	userAuthFlag         string // --user-auth, or its old name --experimental-auth
	project              string // --project, or ""
	flagCredentials      bool   // --url, --api-key and --api-secret
	envCredentials       bool   // LIVEKIT_URL, LIVEKIT_API_KEY and LIVEKIT_API_SECRET

	apiKeyProjects []string // configured API-key projects, from one and two
	defaultProject string   // default_project, or ""

	signedIn       bool   // u_1 holds a session on http://publicapi.test, which can reach one and two
	sessionExpired bool   // and it has expired
	defaultUser    string // default_user, or ""
	apiURL         string // LIVEKIT_API_URL, or ""
}

func drawCredentialCase(t *rapid.T) credentialCase {
	// Weighted towards user auth and a usable session, which need more draws
	// to line up than API-key mode does.
	authFlags := rapid.SampledFrom([][]string{
		nil, {"--legacy-auth"}, {"--user-auth"}, {"--user-auth"}, {"--experimental-auth"},
		{"--user-auth", "--legacy-auth"}, {"--experimental-auth", "--legacy-auth"},
	}).Draw(t, "auth-mode flags")
	userAuthFlag := ""
	for _, f := range authFlags {
		if f != "--legacy-auth" {
			userAuthFlag = f
		}
	}
	session := rapid.SampledFrom([]string{"valid", "valid", "valid", "expired", "none"}).Draw(t, "session")
	projects := []string{"", "one", "two", "gone"}
	return credentialCase{
		userAuth:        userAuthFlag != "",
		userAuthFlag:    userAuthFlag,
		legacyAuth:      slices.Contains(authFlags, "--legacy-auth"),
		project:         rapid.SampledFrom(projects).Draw(t, "--project"),
		flagCredentials: rapid.Bool().Draw(t, "credential flags"),
		envCredentials:  rapid.Bool().Draw(t, "credential env vars"),
		apiKeyProjects:  rapid.SampledFrom([][]string{nil, {"one"}, {"two"}, {"one", "two"}}).Draw(t, "API-key projects"),
		defaultProject:  rapid.SampledFrom(projects).Draw(t, "default_project"),
		signedIn:        session != "none",
		sessionExpired:  session == "expired",
		defaultUser:     rapid.SampledFrom([]string{"", "u_1", "u_1", "gone@example.com"}).Draw(t, "default_user"),
		apiURL:          rapid.SampledFrom([]string{"", "", "http://publicapi.test", "http://other.test"}).Draw(t, "LIVEKIT_API_URL"),
	}
}

func (c credentialCase) args() []string {
	var args []string
	if c.userAuth {
		args = append(args, c.userAuthFlag)
	}
	if c.legacyAuth {
		args = append(args, "--legacy-auth")
	}
	if c.project != "" {
		args = append(args, "--project", c.project)
	}
	if c.flagCredentials {
		args = append(args, "--url", "ws://flag.test", "--api-key", "APIflag", "--api-secret", "secretflag")
	}
	return append(args, "room", "list")
}

func (c credentialCase) env() []string {
	var env []string
	if c.envCredentials {
		env = append(env, "LIVEKIT_URL=ws://env.test", "LIVEKIT_API_KEY=APIenv", "LIVEKIT_API_SECRET=secretenv")
	}
	if c.apiURL != "" {
		env = append(env, "LIVEKIT_API_URL="+c.apiURL)
	}
	return env
}

// projectIDs are the ids of the projects the cases name.
var projectIDs = map[string]string{"one": "p_1", "two": "p_2"}

func (c credentialCase) writeConfig(home string) error {
	conf := config.CLIConfig{DefaultProject: c.defaultProject, DefaultUser: c.defaultUser}
	for _, name := range c.apiKeyProjects {
		conf.Projects = append(conf.Projects, config.ProjectConfig{
			Name: name, ProjectId: projectIDs[name], URL: "ws://" + name + ".test",
			APIKey: "API" + name, APISecret: "secret" + name,
		})
	}
	if c.signedIn {
		user := config.UserConfig{Id: "u_1", Email: "dev@example.com", SessionToken: "st_1", APIURL: "http://publicapi.test"}
		if c.sessionExpired {
			user.SessionExpiry = 1
		}
		for _, name := range []string{"one", "two"} {
			user.Projects = append(user.Projects, config.UserProjectConfig{ProjectId: projectIDs[name], Name: name, URL: "ws://" + name + ".test"})
		}
		conf.Users = append(conf.Users, user)
	}
	data, err := yaml.Marshal(conf)
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".livekit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "cli-config.yaml"), data, 0o600)
}

func (c credentialCase) configured(project string) bool {
	return slices.Contains(c.apiKeyProjects, project)
}

// want is the documented precedence: the first request that names a project,
// as the fake logs it. ok is false when lk must fail before naming one.
func (c credentialCase) want() (request string, ok bool) {
	// The two auth-mode flags are mutually exclusive.
	if c.userAuth && c.legacyAuth {
		return "", false
	}
	if c.userAuth {
		return c.wantSession()
	}
	return c.wantAPIKey()
}

// wantAPIKey follows resolveProject: --project, then credentials from flags
// (each beating its env var), then the default project.
func (c credentialCase) wantAPIKey() (string, bool) {
	listRooms := func(name string) (string, bool) {
		return "POST http://" + name + ".test/twirp/livekit.RoomService/ListRooms apikey=API" + name, true
	}
	switch {
	case c.project != "":
		if c.configured(c.project) {
			return listRooms(c.project)
		}
		return "", false
	case c.flagCredentials:
		return listRooms("flag")
	case c.envCredentials:
		return listRooms("env")
	case c.configured(c.defaultProject):
		return listRooms(c.defaultProject)
	}
	return "", false
}

// wantSession follows resolveSessionUser and resolveProjectRef. The session
// is for the Public API in LIVEKIT_API_URL, or else the one the default user
// signed in to; u_1 acts if it's the default user or the only user signed in
// there. The project is --project, or else the default project, among those
// the session can reach.
func (c credentialCase) wantSession() (string, bool) {
	if !c.signedIn || c.sessionExpired {
		return "", false
	}
	api := c.apiURL
	if api == "" && c.defaultUser == "u_1" {
		api = "http://publicapi.test"
	}
	if api != "http://publicapi.test" {
		return "", false
	}
	project := cmp.Or(c.project, c.defaultProject)
	id, ok := projectIDs[project]
	if !ok {
		return "", false
	}
	return "POST http://publicapi.test/v0/projects/" + id + "/tokens bearer=st_1 project=" + id, true
}

// namedProjects are the projects lk may name: --project, the default project,
// and the hosts given with inline credentials.
func (c credentialCase) namedProjects() []string {
	var named []string
	for _, p := range []string{c.project, c.defaultProject} {
		if p != "" {
			named = append(named, p)
		}
	}
	if c.flagCredentials {
		named = append(named, "flag")
	}
	if c.envCredentials {
		named = append(named, "env")
	}
	return named
}

// requestProject returns the project a logged request names: the project of a
// Public API path, or the host of a request to a project's server. A request
// lk tried to send outside the fake names the host it tried.
func requestProject(line string) (string, bool) {
	if _, id, ok := strings.Cut(line, " project="); ok {
		for name, pid := range projectIDs {
			if pid == id {
				return name, true
			}
		}
		return id, true
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", false
	}
	if strings.Contains(line, "refused") {
		return fields[1], true
	}
	host, _, _ := strings.Cut(strings.TrimPrefix(fields[1], "http://"), "/")
	if host == "publicapi.test" {
		return "", false
	}
	return strings.TrimSuffix(host, ".test"), true
}

// The known bugs the property steps around. Each case that hits one is
// skipped with its message.
var (
	bugProjectFallback = knownBug{"a --project that names no configured project falls back to the only one", "no ticket yet; spec story 36"}
	bugSoleProject     = knownBug{"with no --project, inline credentials or default, API-key mode uses the only configured project", "needs a decision; spec story 52"}
)

type knownBug struct{ name, ref string }

func (b knownBug) message() string { return "known bug: " + b.name + " (" + b.ref + ")" }

func (c credentialCase) knownBug() (knownBug, bool) {
	if c.userAuth || len(c.apiKeyProjects) != 1 {
		return knownBug{}, false
	}
	switch {
	case c.project != "" && !c.configured(c.project):
		return bugProjectFallback, true
	case c.project == "" && !c.flagCredentials && !c.envCredentials && !c.configured(c.defaultProject):
		return bugSoleProject, true
	}
	return knownBug{}, false
}
