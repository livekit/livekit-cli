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
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	"github.com/livekit/livekit-cli/v2/pkg/config"
)

// TestCredentialResolution runs lk with any mix of auth-mode flags,
// --project, --subdomain, --dev, inline credentials (each from a flag, its
// LIVEKIT_* env var, or both), configured projects, default project,
// livekit.toml, signed-in users and their projects, default user, --user and
// the Public API URL, through one of three commands (credentialCommands), and
// checks what reached the fake LiveKit Cloud against rules that hold whatever
// lk's precedence picks:
//
//   - no request names a project the user didn't pass, set as default, give
//     credentials for, or hold as their only one (namedProjects);
//   - no access token from a key the fake knows is signed with the wrong
//     secret;
//   - a configured project's key only goes to that project's server, an
//     inline or --dev key only to the inline URL's host, and a token minted
//     for one project never goes to another project's server;
//   - under --user-auth, project servers only see minted tokens and session
//     tokens only go to the Public API; in API-key mode, neither a session
//     nor the Public API is used;
//   - when no user can act (no unexpired session, an unknown --user, or a
//     Public API nobody is signed in to), or the command can't run in this
//     mode, lk fails without naming a project.
//
// Which project or user lk picks among those allowed is left to the scripts
// in testdata/script, so this test doesn't restate lk's resolution order.
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
		home, err := os.MkdirTemp("", "lk-credentials")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(home)
		if err := c.writeConfig(home); err != nil {
			t.Fatal(err)
		}
		conf, err := os.ReadFile(filepath.Join(home, ".livekit", "cli-config.yaml"))
		if err != nil {
			t.Fatal(err)
		}

		cloud := newFakeCloud()
		defer cloud.Close()
		cloud.mint = true
		for _, u := range c.users {
			if !u.expired {
				cloud.sessions[u.token] = true
			}
		}
		respond := func(target string, status int, body string) {
			if err := cloud.respond(target, status, body); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"one", "two", "flag", "env"} {
			respond("POST http://"+name+".test/twirp/livekit.RoomService/ListRooms", http.StatusOK, "")
			respond("POST http://"+name+".test/twirp/livekit.Egress/ListEgress", http.StatusOK, "")
		}
		for _, id := range []string{"p_1", "p_2", "two", "gone"} {
			respond("GET "+publicAPI+"/v0/projects/"+id+"/keys", http.StatusOK, `{"keys":[]}`)
		}
		// Inline keys and secrets can come from different sources, so only
		// configured projects' pairs are checked.
		for _, name := range []string{"one", "two"} {
			cloud.secrets["API"+name] = "secret" + name
		}
		respond("GET "+publicAPI+"/v0/projects", http.StatusOK, c.userProjectList())

		ctx, cancel := context.WithTimeout(context.Background(), lkTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, lk, c.args()...)
		cmd.Dir = home
		cmd.Env = append(c.env(), "HOME="+home, "USERPROFILE="+home, "HTTP_PROXY="+cloud.URL, "HTTPS_PROXY="+cloud.URL,
			"GORACE=atexit_sleep_ms=0") // under -race, lk would otherwise sleep a second before each clean exit
		if root, ok := os.LookupEnv("SYSTEMROOT"); ok {
			cmd.Env = append(cmd.Env, "SYSTEMROOT="+root) // Windows networking needs it
		}
		output, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("lk did not exit within %s: %s", lkTimeout, output)
		}
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		requests := cloud.requests()
		t.Logf("$ %s lk %s\ncli-config.yaml:\n%s\nrequests:\n%s\noutput:\n%s",
			strings.Join(c.env(), " "), strings.Join(c.args(), " "), conf, strings.Join(requests, "\n"), output)

		allowed := c.namedProjects()
		if sole := c.soleProject(); !c.userAuth && c.command.fallback && sole != "" {
			// Known bug: when resolving what the user named fails, room list
			// falls back to the only configured project (mistyped-project.txtar).
			allowed = append(allowed, sole)
		}
		named := false
		for _, line := range requests {
			if strings.Contains(line, " refused: ") {
				t.Fatalf("lk sent a request outside the fake LiveKit Cloud: %s", line)
			}
			if strings.Contains(line, " badsig") {
				t.Fatalf("request's access token is signed with the wrong secret for its key: %s", line)
			}
			project, ok := requestProject(line)
			if ok {
				named = true
				if !slices.Contains(allowed, project) {
					t.Fatalf("request names project %q, but only %q were named: %s", project, allowed, line)
				}
				if minted, ok := mintedFor(line); ok && minted != project {
					t.Fatalf("a token minted for project %q went to project %q's server: %s", minted, project, line)
				}
				switch key := apiKeyOf(line); key {
				case "APIone", "APItwo":
					if key != "API"+project {
						t.Fatalf("configured key %s went to project %q's server: %s", key, project, line)
					}
				case "APIflag", "APIenv", "devkey":
					// Inline and --dev keys only go to the inline URL's host.
					if url, _, _ := c.inlineCredentials(); url == "" || hostOf(url) != project {
						t.Fatalf("inline key %s went to project %q's server: %s", key, project, line)
					}
				}
			}
			target := strings.Fields(line)[1]
			switch {
			case c.userAuth && strings.Contains(line, " apikey=") && !strings.Contains(line, " apikey="+mintKey("")):
				t.Fatalf("under --user-auth, a project server got a token not minted by the Public API: %s", line)
			case c.userAuth && strings.Contains(line, " bearer=") && !strings.HasPrefix(target, publicAPI+"/"):
				t.Fatalf("a session token went to a server it wasn't signed in to: %s", line)
			case !c.userAuth && (strings.Contains(line, " bearer=") || strings.Contains(target, "publicapi.test") || strings.Contains(target, "other.test")):
				t.Fatalf("API-key mode used a session or the Public API: %s", line)
			}
		}

		if c.mustFail() && (exitErr == nil || named) {
			t.Fatalf("want lk to fail without naming a project (exited 0: %v, named one: %v)", exitErr == nil, named)
		}
	})
}

// lkTimeout bounds each lk run, so a hung lk fails its case rather than the
// whole package's test timeout.
const lkTimeout = 30 * time.Second

// apiKeyOf returns the API key that issued a logged request's access token, or
// "".
func apiKeyOf(line string) string {
	_, rest, ok := strings.Cut(line, " apikey=")
	if !ok {
		return ""
	}
	key, _, _ := strings.Cut(rest, " ")
	return key
}

// mintedFor returns the project whose minted token a logged request carries.
func mintedFor(line string) (string, bool) {
	_, rest, ok := strings.Cut(line, " apikey="+mintKey(""))
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, " ")
	for name, pid := range projectIDs {
		if pid == id {
			return name, true
		}
	}
	return id, true
}

// credentialCase is one way of invoking lk. The values come from small fixed
// sets, so that cases collide on the same project and user names.
type credentialCase struct {
	command              credentialCommand
	userAuth, legacyAuth bool
	userAuthFlag         string // --user-auth, or its old name --experimental-auth
	project              string // --project, or ""
	subdomain            string // --subdomain, or ""
	dev                  bool   // --dev
	// Where each inline credential (URL, API key, API secret) comes from: "",
	// "flag", "env", or "both", where the flag wins.
	urlFrom, keyFrom, secretFrom string

	apiKeyProjects []string // configured API-key projects, from one and two
	defaultProject string   // default_project, or ""
	toml           string   // the project subdomain in ./livekit.toml, or "" for no file

	users        []sessionUser // signed in to http://publicapi.test
	userProjects []string      // the projects every signed-in user can reach
	defaultUser  string        // default_user, or ""
	user         string        // --user, or ""
	apiURLEnv    string        // LIVEKIT_API_URL, or ""
	apiURLFlag   string        // --public-api-url, or ""
}

// credentialCommand is an lk command the property runs. Each loads its project
// a different way.
type credentialCommand struct {
	args []string
	// fallback is set when, in API-key mode, a failure to resolve a project
	// falls back to the only configured one (requireProjectWithOpts);
	// loadProjectDetails fails instead.
	fallback bool
	// publicAPI is set for a command that calls the Public API directly with
	// the session (and only runs under --user-auth), instead of using a
	// session-minted token against the project's server.
	publicAPI bool
}

var credentialCommands = []credentialCommand{
	{args: []string{"room", "list"}, fallback: true},
	{args: []string{"egress", "list"}},
	{args: []string{"project", "key", "list"}, publicAPI: true},
}

// sessionUser is a user signed in to http://publicapi.test.
type sessionUser struct {
	id, email, token string
	expired          bool
}

const publicAPI = "http://publicapi.test"

func drawCredentialCase(t *rapid.T) credentialCase {
	// rapid draws earlier entries of SampledFrom more often, and a repeated
	// entry more often still, so each list puts the values that need the
	// most draws to line up first: user auth (which also needs a usable
	// session), then API-key mode; both mode flags at once is a one-line
	// refusal, so it's last.
	mode := rapid.SampledFrom([]string{"user", "user", "api-key", "legacy", "user", "api-key", "legacy", "both"}).Draw(t, "auth mode")
	userAuthFlag := ""
	if mode == "user" || mode == "both" {
		userAuthFlag = rapid.SampledFrom([]string{"--user-auth", "--user-auth", "--experimental-auth"}).Draw(t, "user-auth spelling")
	}
	projects := []string{"", "one", "one", "two", "gone"}
	source := rapid.SampledFrom([]string{"", "", "flag", "env", "both"})
	sometimes := rapid.SampledFrom([]bool{false, false, false, true})
	c := credentialCase{
		command:        rapid.SampledFrom(credentialCommands).Draw(t, "command"),
		userAuth:       userAuthFlag != "",
		userAuthFlag:   userAuthFlag,
		legacyAuth:     mode == "legacy" || mode == "both",
		project:        rapid.SampledFrom(projects).Draw(t, "--project"),
		subdomain:      rapid.SampledFrom([]string{"", "", "", "one", "gone"}).Draw(t, "--subdomain"),
		dev:            sometimes.Draw(t, "--dev"),
		urlFrom:        source.Draw(t, "URL from"),
		keyFrom:        source.Draw(t, "API key from"),
		secretFrom:     source.Draw(t, "API secret from"),
		apiKeyProjects: rapid.SampledFrom([][]string{nil, {"one"}, {"two"}, {"one", "two"}}).Draw(t, "API-key projects"),
		defaultProject: rapid.SampledFrom(projects).Draw(t, "default_project"),
		toml:           rapid.SampledFrom([]string{"", "", "", "one", "gone"}).Draw(t, "livekit.toml subdomain"),
		userProjects:   rapid.SampledFrom([][]string{{"one", "two"}, {"one"}}).Draw(t, "user projects"),
		defaultUser:    rapid.SampledFrom([]string{"", "u_1", "u_1", "u_1", "ops@example.com", "gone@example.com"}).Draw(t, "default_user"),
		user:           rapid.SampledFrom([]string{"", "", "", "", "u_1", "dev@example.com", "u_3", "gone@example.com"}).Draw(t, "--user"),
		apiURLEnv:      rapid.SampledFrom([]string{"", "", "", publicAPI, "http://other.test"}).Draw(t, "LIVEKIT_API_URL"),
		apiURLFlag:     rapid.SampledFrom([]string{"", "", "", "", "", publicAPI, "http://other.test"}).Draw(t, "--public-api-url"),
	}
	switch rapid.SampledFrom([]string{"valid", "valid", "expired", "valid", "none"}).Draw(t, "u_1 session") {
	case "valid":
		c.users = append(c.users, sessionUser{id: "u_1", email: "dev@example.com", token: "st_1"})
	case "expired":
		c.users = append(c.users, sessionUser{id: "u_1", email: "dev@example.com", token: "st_1", expired: true})
	}
	if sometimes.Draw(t, "u_3 signed in too") {
		c.users = append(c.users, sessionUser{id: "u_3", email: "ops@example.com", token: "st_3"})
	}
	return c
}

func fromFlag(source string) bool { return source == "flag" || source == "both" }
func fromEnv(source string) bool  { return source == "env" || source == "both" }

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
	if c.subdomain != "" {
		args = append(args, "--subdomain", c.subdomain)
	}
	if c.dev {
		args = append(args, "--dev")
	}
	if c.user != "" {
		args = append(args, "--user", c.user)
	}
	if c.apiURLFlag != "" {
		args = append(args, "--public-api-url", c.apiURLFlag)
	}
	if fromFlag(c.urlFrom) {
		args = append(args, "--url", "ws://flag.test")
	}
	if fromFlag(c.keyFrom) {
		args = append(args, "--api-key", "APIflag")
	}
	if fromFlag(c.secretFrom) {
		args = append(args, "--api-secret", "secretflag")
	}
	return append(args, c.command.args...)
}

func (c credentialCase) env() []string {
	var env []string
	if fromEnv(c.urlFrom) {
		env = append(env, "LIVEKIT_URL=ws://env.test")
	}
	if fromEnv(c.keyFrom) {
		env = append(env, "LIVEKIT_API_KEY=APIenv")
	}
	if fromEnv(c.secretFrom) {
		env = append(env, "LIVEKIT_API_SECRET=secretenv")
	}
	if c.apiURLEnv != "" {
		env = append(env, "LIVEKIT_API_URL="+c.apiURLEnv)
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
	for _, u := range c.users {
		user := config.UserConfig{Id: u.id, Email: u.email, SessionToken: u.token, APIURL: publicAPI}
		if u.expired {
			user.SessionExpiry = 1
		}
		for _, name := range c.userProjects {
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
	if err := os.WriteFile(filepath.Join(dir, "cli-config.yaml"), data, 0o600); err != nil {
		return err
	}
	if c.toml == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(home, "livekit.toml"), []byte("[project]\nsubdomain = \""+c.toml+"\"\n"), 0o600)
}

// userProjectList is the Public API's answer to listing the signed-in user's
// projects.
func (c credentialCase) userProjectList() string {
	var items []string
	for _, name := range c.userProjects {
		items = append(items, `{"id":"`+projectIDs[name]+`","name":"`+name+`","url":"ws://`+name+`.test"}`)
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

func (c credentialCase) configured(project string) bool {
	return slices.Contains(c.apiKeyProjects, project)
}

// soleProject is the only configured API-key project, or "". When nothing
// resolves, selectProject uses it without asking in non-interactive mode.
func (c credentialCase) soleProject() string {
	if len(c.apiKeyProjects) == 1 {
		return c.apiKeyProjects[0]
	}
	return ""
}

// inlineCredentials are the URL, API key and API secret lk reads from flags,
// each beating its env var.
func (c credentialCase) inlineCredentials() (url, key, secret string) {
	pick := func(source, flag, env string) string {
		switch {
		case fromFlag(source):
			return flag
		case fromEnv(source):
			return env
		}
		return ""
	}
	return pick(c.urlFrom, "ws://flag.test", "ws://env.test"),
		pick(c.keyFrom, "APIflag", "APIenv"),
		pick(c.secretFrom, "secretflag", "secretenv")
}

// hostOf is the fake host name in a ws://NAME.test URL.
func hostOf(url string) string {
	return strings.TrimSuffix(strings.TrimPrefix(url, "ws://"), ".test")
}

// namedProjects are the projects lk may name. In API-key mode: --project,
// --subdomain, livekit.toml's, the default project, the host given in an
// inline URL, and the only configured project when the user named none. Under
// user auth: --project, the default project, and the user's only project when
// --project isn't given.
func (c credentialCase) namedProjects() []string {
	var named []string
	add := func(ps ...string) {
		for _, p := range ps {
			if p != "" {
				named = append(named, p)
			}
		}
	}
	add(c.project, c.defaultProject)
	if c.userAuth {
		if c.project == "" && len(c.userProjects) == 1 {
			add(c.userProjects[0])
		}
		return named
	}
	add(c.subdomain, c.toml)
	if url, _, _ := c.inlineCredentials(); url != "" {
		add(hostOf(url))
	}
	if c.project == "" && c.subdomain == "" && c.toml == "" {
		add(c.soleProject())
	}
	return named
}

// requestProject returns the project a logged request names: the project of a
// Public API path, or the host of a request to a project's server.
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
	host, _, _ := strings.Cut(strings.TrimPrefix(fields[1], "http://"), "/")
	if host == "publicapi.test" {
		return "", false
	}
	return strings.TrimSuffix(host, ".test"), true
}

// mustFail is set when lk can't act: both mode flags at once, a Public API
// command outside --user-auth, or, under --user-auth, no user who can act (no
// unexpired session, a --user nobody signed in as, or a Public API URL nobody
// is signed in to). Every drawn user is signed in to publicAPI.
func (c credentialCase) mustFail() bool {
	switch {
	case c.userAuth && c.legacyAuth:
		return true
	case !c.userAuth:
		return c.command.publicAPI
	case !slices.ContainsFunc(c.users, func(u sessionUser) bool { return !u.expired }):
		return true
	case c.user != "" && !slices.ContainsFunc(c.users, func(u sessionUser) bool { return u.id == c.user || u.email == c.user }):
		return true
	}
	api := cmp.Or(c.apiURLFlag, c.apiURLEnv)
	return api != "" && api != publicAPI
}
