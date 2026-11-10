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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/server-sdk-go/v2/signalling"
)

// API keys for apps and agents.
//
// The CLI's own calls don't need an API key under --user-auth (see
// session_auth.go), but an app or agent runs on its own and does. Every
// command that hands a key to an app or agent gets it from one chain, in both
// auth modes:
//
//  1. --api-key/--api-secret, or LIVEKIT_API_KEY/LIVEKIT_API_SECRET, as given
//     (never checked, saved, or replaced: they're the user's override). Half
//     a pair is ignored with a warning: a key works only with its own secret.
//  2. The agent's own env file (the Taskfile's env_file, .env.local, .env),
//     unless --project or --subdomain names a project the key isn't for.
//  3. A key for the project in the CLI config's projects, where `lk cloud
//     auth`, `lk project add`, and steps 4 and 5 keep keys.
//  4. Under --user-auth only: a new service-account key, created as the
//     signed-in user. A user whose role can't create keys (below Admin) goes
//     on to step 5: that's an expected step, not a failure.
//  5. In a terminal: a key the user types in, checked with LiveKit before
//     it's used (what `lk project add` asks for).
//
// Without a terminal, the chain ends in an error that says exactly how to
// supply a key, before any agent starts. agent console and the text-mode
// agent debugger run the agent on this machine, where an agent that brings
// its own providers needs no LiveKit key: for them a key is optional, and the
// chain stops after step 4 with a warning that says the same, and the agent
// starts without one. A key LiveKit rejects (deleted, or
// for another project) at steps 2 and 3 is skipped with a warning, and the
// next step replaces it.
//
// The URL the agent will connect to, when something names one (--url, else
// LIVEKIT_URL in the agent's env file, with or without a key), picks the
// project for steps 3-5, so a key is never paired with another project's URL.
// An env file's URL that lk knows no project for ends the chain rather than
// fall back to the default project; a --url it knows none for (a custom
// domain, a tunnel) means the project lk would pick, reached at that URL.
//
// When lk creates a key: only at step 4, so only under --user-auth, only in
// the commands that hand a key to an app or agent (agent dev, start, console,
// debugger and init; app create and env), and only when steps 1-3 found no
// usable key; agent simulate text and audio, for the agent they start, also
// create one at step 4 (simulationAgentCredentials). Commands that only call
// LiveKit themselves (room, sip, egress, simulate view and export, ...) use
// session tokens and never create one.
//
// Where lk keeps it: a key from steps 4 and 5 is saved to the CLI config
// (~/.livekit, outside any repository), so other agents and later runs on
// this machine reuse it rather than create another, and lk passes it to the
// agent in its environment. Running an agent doesn't write its env files: a
// secret written into the agent's directory can be committed. lk writes one
// only when asked: agent init, app create, and app env --write. The templates
// load .env.local without overriding variables already set, so the key lk
// passes wins over a stale one there.
//
// In API-key mode the CLI never uses a signed-in session on its own; when no
// API-key project is configured, an interactive run offers the signed-in
// account as one of the ways forward, and a non-interactive one names
// --user-auth in its error.

// agentEnvFiles are the env files an app or agent keeps its credentials in, in
// priority order: the templates write .env.local, and .env is common too.
var agentEnvFiles = []string{".env.local", ".env"}

// agentEnvFileNames returns the env files of the agent in dir, in priority
// order: the env_file its Taskfile names (the one `lk agent init` wrote), then
// agentEnvFiles.
func agentEnvFileNames(dir string) []string {
	return appEnvFiles(taskfileEnvFile(dir))
}

// taskfileEnvFile returns the env_file var of the Taskfile in dir, or "". It
// reads the YAML itself rather than loading the Taskfile, which can reach
// out to remote includes.
func taskfileEnvFile(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, bootstrap.TaskFile))
	if err != nil {
		return ""
	}
	var tf struct {
		Vars map[string]any `yaml:"vars"`
	}
	if err := yaml.Unmarshal(data, &tf); err != nil {
		logger.Debugw("couldn't read Taskfile", "dir", dir, "error", err)
		return ""
	}
	f, _ := tf.Vars["env_file"].(string)
	return f
}

// envFileCredentials returns the LiveKit credentials from the first of files in
// dir that has both an API key and secret, along with that file's name. It
// returns zero credentials when none does.
func envFileCredentials(dir string, files ...string) (agentCredentials, string, error) {
	if dir == "" {
		return agentCredentials{}, "", nil
	}
	for _, f := range files {
		env, err := bootstrap.ReadDotEnv(dir, f)
		if err != nil {
			return agentCredentials{}, "", fmt.Errorf("reading %s: %w", filepath.Join(dir, f), err)
		}
		if env["LIVEKIT_API_KEY"] != "" && env["LIVEKIT_API_SECRET"] != "" {
			return agentCredentials{
				url:       env["LIVEKIT_URL"],
				apiKey:    env["LIVEKIT_API_KEY"],
				apiSecret: env["LIVEKIT_API_SECRET"],
			}, f, nil
		}
	}
	return agentCredentials{}, "", nil
}

// keyRequest describes what a key is wanted for, for the prompt and the error
// when there's none.
type keyRequest struct {
	// dir is the agent's or app's directory, or "" when there is none.
	dir string
	// files are dir's env files, in priority order.
	files []string
	// purpose finishes "no LiveKit API key ...", e.g. "to run the agent with".
	purpose string
	// optional is set when the agent can run without a key: nothing is asked
	// for, and the chain ends in a noKeyError that the caller warns with.
	optional bool
}

// noKeyError is the end of the chain when no key was found: it says how to
// supply one.
type noKeyError struct{ msg string }

func (e *noKeyError) Error() string { return e.msg }

// resolveAgentCredentials returns the credentials the agent in dir runs with,
// from the chain at the top of this file, and saves a key the chain made or
// was given (see saveProjectKey).
func resolveAgentCredentials(ctx context.Context, cmd *cli.Command, dir string) (agentCredentials, error) {
	return agentCredentialsChain(ctx, cmd, dir, false)
}

// resolveOptionalAgentCredentials is resolveAgentCredentials for an agent that
// can run without a LiveKit key (agent console, the text-mode debugger): when
// the chain finds none without asking, it warns how to supply one and
// returns no credentials, and the agent starts with whatever its own env file
// holds.
func resolveOptionalAgentCredentials(ctx context.Context, cmd *cli.Command, dir string) (agentCredentials, error) {
	creds, err := agentCredentialsChain(ctx, cmd, dir, true)
	var noKey *noKeyError
	if errors.As(err, &noKey) {
		out.Warnf("%s\n\nStarting the agent without one: what it asks of LiveKit, such as LiveKit Inference, will fail.", noKey.msg)
		return agentCredentials{}, nil
	}
	return creds, err
}

func agentCredentialsChain(ctx context.Context, cmd *cli.Command, dir string, optional bool) (agentCredentials, error) {
	explicit := explicitCredentials(cmd)
	// The agent gets LIVEKIT_* credentials from the environment even under
	// --user-auth, so they aren't ignored here.
	envCredentialsUsed = true

	// An explicitly named project always wins, mirroring resolveProject's
	// priority order. Without this, ambient LIVEKIT_* shell variables (which
	// count as explicit above) would override — or worse, mix into — the
	// project the user asked for by name.
	if namedProject(cmd) {
		explicit = agentCredentials{}
	}

	if cmd.Bool("dev") {
		// --dev with an API key or secret is an error, as in resolveProject.
		if explicit.apiKey != "" || explicit.apiSecret != "" {
			return agentCredentials{}, devCredentialsError(cmd)
		}
		pc, err := loadProjectDetails(cmd, ignoreURL)
		if err != nil {
			return agentCredentials{}, err
		}
		return mergeCredentials(explicit, credentialsOf(pc)), nil
	}
	if explicit.complete() {
		return explicit, nil
	}
	explicit = wholeKeyPair(explicit)

	req := keyRequest{dir: dir, files: agentEnvFileNames(dir), purpose: "to run the agent with", optional: optional}
	fileCreds, file, err := envFileCredentials(dir, req.files...)
	if err != nil {
		return agentCredentials{}, err
	}

	// The URL the agent will connect to, when something names one: --url,
	// else the agent's env file (whether or not it has a key). It picks the
	// project, so a key is never paired with another project's URL.
	target, fromFile := explicit.url, ""
	if target == "" && !namedProject(cmd) {
		if fileCreds.url != "" {
			target, fromFile = fileCreds.url, file
		} else if u, f := envFileURL(dir, req.files...); u != "" {
			target, fromFile = u, f
		}
	}

	// A key and secret given without a URL only need the project's URL.
	if explicit.apiKey != "" {
		if target != "" {
			return agentCredentials{url: target, apiKey: explicit.apiKey, apiSecret: explicit.apiSecret}, nil
		}
		pc, err := resolveAgentProject(ctx, cmd, req)
		if err != nil {
			return agentCredentials{}, err
		}
		return agentCredentials{url: pc.URL, apiKey: explicit.apiKey, apiSecret: explicit.apiSecret}, nil
	}

	// The agent's own key needs no project lookup, unless a project was named
	// (the key must be for it) or nothing names a URL.
	if fileCreds.apiKey != "" && !namedProject(cmd) && target != "" {
		creds := agentCredentials{url: target, apiKey: fileCreds.apiKey, apiSecret: fileCreds.apiSecret}
		path := filepath.Join(dir, file)
		if keyAccepted(ctx, creds, path) {
			out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(path))
			return creds, nil
		}
		fileCreds = agentCredentials{}
	}

	var (
		pc       *config.ProjectConfig
		override string
	)
	switch {
	case target != "" && fromFile != "":
		pc, err = projectAtFileURL(ctx, cmd, req, target, filepath.Join(dir, fromFile))
	case target != "":
		pc, override, err = projectAtFlagURL(ctx, cmd, req, target)
	default:
		pc, err = resolveAgentProject(ctx, cmd, req)
	}
	if err != nil {
		return agentCredentials{}, err
	}
	key, err := agentKeyForProject(ctx, cmd, pc, req, fileCreds, file)
	if err != nil {
		return agentCredentials{}, err
	}
	if override != "" {
		// The key is looked up, checked and saved at the project's own URL;
		// only the agent connects at --url.
		key.url = override
	}
	return key, nil
}

// wholeKeyPair drops half a key pair: an API key works only with its own
// secret, and paired with the other half of another key it fails to
// authenticate. Half a pair usually comes from a stray LIVEKIT_API_KEY or
// LIVEKIT_API_SECRET in the shell.
func wholeKeyPair(c agentCredentials) agentCredentials {
	switch {
	case c.apiKey != "" && c.apiSecret == "":
		out.Warnf("Ignoring the API key [%s] from --api-key or LIVEKIT_API_KEY: it has no secret (--api-secret or LIVEKIT_API_SECRET)", c.apiKey)
	case c.apiKey == "" && c.apiSecret != "":
		out.Warnf("Ignoring the API secret from --api-secret or LIVEKIT_API_SECRET: it has no API key (--api-key or LIVEKIT_API_KEY)")
	default:
		return c
	}
	return agentCredentials{url: c.url}
}

// envFileURL returns the first LIVEKIT_URL in dir's files, and the file it's
// in, whether or not that file has a key.
func envFileURL(dir string, files ...string) (string, string) {
	if dir == "" {
		return "", ""
	}
	for _, f := range files {
		if env, err := bootstrap.ReadDotEnv(dir, f); err == nil && env["LIVEKIT_URL"] != "" {
			return env["LIVEKIT_URL"], f
		}
	}
	return "", ""
}

// agentKeyForProject returns a key for pc from steps 2-5: the agent's env file
// (fileCreds, read from file), when it's for pc and LiveKit accepts it, else
// projectKey.
func agentKeyForProject(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig, req keyRequest, fileCreds agentCredentials, file string) (agentCredentials, error) {
	if fileCreds.apiKey != "" {
		path := filepath.Join(req.dir, file)
		if fileCreds.url != "" && !sameProjectURL(fileCreds.url, pc.URL) {
			out.Statusf("Not using the API key in %s: it's for %s, not project [%s]", util.Accented(path), fileCreds.url, util.Accented(pc.Name))
		} else {
			creds := agentCredentials{url: pc.URL, apiKey: fileCreds.apiKey, apiSecret: fileCreds.apiSecret}
			if keyAccepted(ctx, creds, path) {
				out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(path))
				return creds, nil
			}
		}
	}

	return projectKey(ctx, cmd, pc, req)
}

// findProjectAtURL returns the project whose URL is url: under --user-auth
// one of the signed-in user's projects (a session project), otherwise one in
// the CLI config. When there's none, it returns nil and where it looked.
func findProjectAtURL(ctx context.Context, cmd *cli.Command, url string) (*config.ProjectConfig, string, error) {
	if userAuthEnabled(cmd) {
		_, conf, user, err := newCloudAPIClient(cmd)
		if err != nil {
			return nil, "", err
		}
		find := func() string {
			for _, p := range user.Projects {
				if p.URL != "" && sameProjectURL(p.URL, url) {
					return p.ProjectId
				}
			}
			return ""
		}
		id := find()
		if id == "" {
			if _, err := refreshUserProjects(ctx, conf, user); err != nil {
				return nil, "", cloudAPIError(err)
			}
			id = find()
		}
		if id == "" {
			return nil, "a project " + userLabel(user) + " belongs to", nil
		}
		pc, err := loadSessionProjectRef(ctx, cmd, id)
		return pc, "", err
	}
	if _, err := loadProjectConfig(ctx, cmd); err != nil {
		return nil, "", err
	}
	if cliConfig != nil {
		for _, p := range cliConfig.Projects {
			if sameProjectURL(p.URL, url) {
				p := p
				out.Statusf("Using project [%s]", util.Accented(p.Name))
				return &p, "", nil
			}
		}
	}
	return nil, "a project in the CLI config", nil
}

// projectAtFileURL returns the project an env file's LIVEKIT_URL names. When
// lk knows none there, the chain ends: running the agent on the default
// project instead would send it somewhere the file didn't point it.
func projectAtFileURL(ctx context.Context, cmd *cli.Command, req keyRequest, url, path string) (*config.ProjectConfig, error) {
	pc, where, err := findProjectAtURL(ctx, cmd, url)
	if err != nil || pc != nil {
		return pc, err
	}
	return nil, &noKeyError{fmt.Sprintf("no LiveKit API key %s for %s, the LIVEKIT_URL in %s: it isn't %s\n\n"+
		"To supply one, do one of:\n"+
		"  - add LIVEKIT_API_KEY and LIVEKIT_API_SECRET for it to %s\n"+
		"  - remove LIVEKIT_URL from %s to use your default project, or pass --project\n"+
		"  - pass --api-key and --api-secret, or set LIVEKIT_API_KEY and LIVEKIT_API_SECRET",
		req.purpose, url, path, where, path, path)}
}

// projectAtFlagURL returns the project --url names. When lk knows none there
// (a local server, a tunnel, a custom domain), --url keeps its API-key mode
// meaning: the project lk would otherwise pick, reached at url. It's
// returned as override, for the agent only: the project's key is looked up,
// checked and saved at the project's own URL. Checking it at an unknown
// server (a local one answers 401) would replace a good key on every run,
// and saving it there would move the project to url in the CLI config.
func projectAtFlagURL(ctx context.Context, cmd *cli.Command, req keyRequest, url string) (pc *config.ProjectConfig, override string, err error) {
	pc, _, err = findProjectAtURL(ctx, cmd, url)
	if err != nil || pc != nil {
		return pc, "", err
	}
	pc, err = resolveAgentProject(ctx, cmd, req)
	if err != nil {
		return nil, "", err
	}
	return pc, url, nil
}

// simulationAgentCredentials returns what the agent `agent simulate` starts in
// dir registers with. It must be on pc, the project the simulation run is
// created on, so --url and the env file's LIVEKIT_URL can't move it
// elsewhere: in API-key mode it's pc's own key; under --user-auth, a key and
// secret given with --api-key/--api-secret (or LIVEKIT_API_KEY/SECRET) that
// LiveKit accepts for pc, else a key for pc from agentKeyForProject.
func simulationAgentCredentials(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig, dir string) (agentCredentials, error) {
	if !isSessionProject(pc) {
		return credentialsOf(pc), nil
	}
	if explicit := explicitCredentials(cmd); explicit.apiKey != "" && explicit.apiSecret != "" {
		envCredentialsUsed = true
		creds := agentCredentials{url: pc.URL, apiKey: explicit.apiKey, apiSecret: explicit.apiSecret}
		if keyAccepted(ctx, creds, "--api-key") {
			out.Statusf("Using API key [%s] from --api-key", util.Accented(creds.apiKey))
			return creds, nil
		}
	}
	req := keyRequest{dir: dir, files: agentEnvFileNames(dir), purpose: "to run the agent with"}
	fileCreds, file, err := envFileCredentials(dir, req.files...)
	if err != nil {
		return agentCredentials{}, err
	}
	return agentKeyForProject(ctx, cmd, pc, req, fileCreds, file)
}

// resolveAgentProject returns the project an agent runs against: under
// --user-auth the signed-in user's (a session project, with placeholder
// credentials), otherwise the API-key project resolveProject picks. When API-key
// mode finds none, chooseAgentProject asks the user how to go on.
func resolveAgentProject(ctx context.Context, cmd *cli.Command, req keyRequest) (*config.ProjectConfig, error) {
	if userAuthEnabled(cmd) {
		return loadSessionProject(ctx, cmd)
	}
	if _, err := loadProjectConfig(ctx, cmd); err != nil {
		return nil, err
	}
	rp, err := resolveProject(cmd, loadParams{})
	switch {
	case err == nil:
		rp.announce()
		return rp.project, nil
	case errors.Is(err, config.ErrInvalidConfig), namedProject(cmd):
		return nil, err
	}
	return chooseAgentProject(ctx, cmd, req)
}

// The ways chooseAgentProject offers to go on.
const (
	useSignedInUser = "user"
	signInBrowser   = "browser"
	enterKey        = "key"
)

// chooseAgentProject finds a project when API-key mode has none configured: in
// a terminal it offers to use the signed-in user (as if --user-auth were
// given), sign in with `lk cloud auth`, or type in a URL and key; otherwise it
// returns an error listing those.
func chooseAgentProject(ctx context.Context, cmd *cli.Command, req keyRequest) (*config.ProjectConfig, error) {
	user := signedInUser(cmd)
	if SkipPrompts(cmd) || req.optional {
		return nil, missingKeyError(nil, req, "no LiveKit project is configured", user)
	}

	var options []huh.Option[string]
	if user != nil {
		options = append(options, huh.NewOption("Use your signed-in account ("+userLabel(user)+")", useSignedInUser))
	}
	options = append(options,
		huh.NewOption("Sign in to LiveKit Cloud in your browser", signInBrowser),
		huh.NewOption("Enter a server URL and API key", enterKey),
	)
	choice := options[0].Value
	if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().
		Title("No LiveKit project is configured. How do you want to connect?").
		Options(options...).
		Value(&choice).
		WithTheme(util.FormTheme()))).
		RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("no project selected: %w", err)
	}

	switch choice {
	case useSignedInUser:
		out.Statusf("Tip: pass %s to use your signed-in account without this question", util.Accented("--user-auth"))
		return loadSessionProject(ctx, cmd)
	case signInBrowser:
		initAuth(ctx, cmd)
		if err := tryAuthIfNeeded(ctx, cmd); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
		pc, err := config.LoadDefaultProject()
		if err != nil {
			return nil, err
		}
		(&resolvedProject{project: pc, source: sourceSelected}).announce()
		return pc, nil
	default:
		creds, err := promptForKey(ctx, cmd, nil, req, "")
		if err != nil {
			return nil, err
		}
		return &config.ProjectConfig{Name: util.ExtractSubdomain(creds.url), URL: creds.url, APIKey: creds.apiKey, APISecret: creds.apiSecret}, nil
	}
}

// signedInUser returns the default user signed in to the Public API in use,
// if their session hasn't expired, without calling anything. It's nil when no
// such user exists.
func signedInUser(cmd *cli.Command) *config.UserConfig {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return nil
	}
	user, err := resolveSessionUser(conf, cmd.String("user"), cmd.IsSet("public-api-url"))
	if err != nil || !user.SessionValid() {
		return nil
	}
	return user
}

// projectKey returns a working API key for pc, from steps 3-5 of the chain:
// pc's own key (an API-key project) or one saved for it in the CLI config
// (a session project), then a new one (under --user-auth), then one the user
// types in.
func projectKey(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig, req keyRequest) (agentCredentials, error) {
	if !isSessionProject(pc) {
		creds := credentialsOf(pc)
		if creds.apiKey != "" && keyAccepted(ctx, creds, "the CLI config") {
			return creds, nil
		}
		// API-key mode never creates keys.
		return promptForKey(ctx, cmd, pc, req, fmt.Sprintf("LiveKit rejected the API key for project [%s] in the CLI config", pc.Name))
	}

	if cached := savedProjectKey(pc); cached != nil {
		creds := credentialsOf(cached)
		creds.url = pc.URL
		if keyAccepted(ctx, creds, "the CLI config") {
			out.Statusf("Using API key [%s] for project [%s] from the CLI config", util.Accented(creds.apiKey), util.Accented(pc.Name))
			saveProjectKey(pc, creds) // only updates the URL, if the project moved
			return creds, nil
		}
	}

	creds, err := mintProjectKey(ctx, cmd, pc, req.dir)
	if err == nil {
		saveProjectKey(pc, creds)
		return creds, nil
	}
	var reason string
	switch {
	case public.IsUnauthenticated(err):
		// The session was revoked or expired: signing in again is the fix,
		// not a key.
		return agentCredentials{}, cloudAPIError(err)
	case public.IsPermissionDenied(err):
		reason = fmt.Sprintf("your role on project [%s] can't create API keys (that takes Admin)", pc.Name)
	default:
		reason = fmt.Sprintf("couldn't create an API key for project [%s]: %v", pc.Name, cloudAPIError(err))
	}
	return promptForKey(ctx, cmd, pc, req, reason)
}

// mintProjectKey creates a service-account key on session project pc, which
// outlives the user's membership in the project. It's named after the command
// and dir, so it can be told apart on the project's key list.
func mintProjectKey(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig, dir string) (agentCredentials, error) {
	client, _, _, err := newCloudAPIClient(cmd)
	if err != nil {
		return agentCredentials{}, err
	}
	description := "lk " + commandName(cmd)
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			description += " (" + filepath.Base(abs) + ")"
		}
	}
	key, err := client.GenerateKey(ctx, pc.ProjectId, description, true)
	if err != nil {
		return agentCredentials{}, err
	}
	if util.Deref(key.ApiKey) == "" || util.Deref(key.Secret) == "" {
		return agentCredentials{}, fmt.Errorf("the API returned no key for project [%s]", pc.Name)
	}
	out.Statusf("Created API key [%s] on project [%s]", util.Accented(*key.ApiKey), util.Accented(pc.Name))
	return agentCredentials{url: pc.URL, apiKey: *key.ApiKey, apiSecret: *key.Secret}, nil
}

// promptForKey asks for an API key and secret for pc (and, with pc nil, for a
// server URL too), checks them with LiveKit, and saves them to the CLI config.
// reason says why there's no key yet. Without a terminal it returns
// missingKeyError instead.
func promptForKey(ctx context.Context, cmd *cli.Command, pc *config.ProjectConfig, req keyRequest, reason string) (agentCredentials, error) {
	if SkipPrompts(cmd) || req.optional {
		return agentCredentials{}, missingKeyError(pc, req, reason, nil)
	}
	if reason != "" {
		out.Statusf("%s.", upperFirst(reason))
	}

	var creds agentCredentials
	title := "Enter an API key"
	if pc != nil {
		creds.url = pc.URL
		title = fmt.Sprintf("Enter an API key for project [%s]", pc.Name)
	}
	const attempts = 3
	for range attempts {
		var fields []huh.Field
		if pc == nil {
			fields = append(fields, huh.NewInput().
				Title("Server URL").
				Placeholder("wss://my-project.livekit.cloud").
				Prompt("").
				Validate(validateServerURL).
				Value(&creds.url))
		}
		fields = append(fields,
			huh.NewInput().
				Title(title).
				Description("Find or create one under Settings › API keys at "+dashboardURL+", or ask a project admin.").
				Placeholder("APIxxxxxxxxxxxx").
				Prompt("").
				Validate(requiredValue).
				Value(&creds.apiKey),
			huh.NewInput().
				Title("API secret").
				EchoMode(huh.EchoModePassword).
				Prompt("").
				Validate(requiredValue).
				Value(&creds.apiSecret),
		)
		if err := huh.NewForm(huh.NewGroup(fields...)).
			WithTheme(util.FormTheme()).
			RunWithContext(ctx); err != nil {
			return agentCredentials{}, err
		}
		creds.apiKey, creds.apiSecret = strings.TrimSpace(creds.apiKey), strings.TrimSpace(creds.apiSecret)
		if checkAPIKey(ctx, creds.url, creds.apiKey, creds.apiSecret) == nil {
			target := pc
			if target == nil {
				target = &config.ProjectConfig{URL: creds.url}
			}
			saveProjectKey(target, creds)
			return creds, nil
		}
		out.Warnf("LiveKit rejected API key [%s] for %s; check the key and secret and try again", creds.apiKey, creds.url)
		creds.apiSecret = ""
	}
	return agentCredentials{}, fmt.Errorf("LiveKit rejected the API key %d times", attempts)
}

func requiredValue(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func validateServerURL(s string) error {
	s = strings.TrimSpace(s)
	for _, scheme := range []string{"wss://", "ws://", "https://", "http://"} {
		if strings.HasPrefix(s, scheme) && len(s) > len(scheme) {
			return nil
		}
	}
	return errors.New("enter a URL such as wss://my-project.livekit.cloud")
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// missingKeyError explains, for a run without a terminal, why there's no key
// for pc (nil when there's no project at all) and every way to supply one.
// user is the signed-in user API-key mode didn't use, if any.
func missingKeyError(pc *config.ProjectConfig, req keyRequest, reason string, user *config.UserConfig) error {
	var b strings.Builder
	b.WriteString("no LiveKit API key " + req.purpose)
	if pc != nil {
		fmt.Fprintf(&b, " for project [%s]", pc.Name)
	}
	if reason != "" {
		b.WriteString(": " + reason)
	}
	if req.optional && pc != nil {
		b.WriteString("\n\nTo supply one (find or create one under Settings › API keys at " + dashboardURL + ", or ask a project admin), do one of:\n")
	} else {
		b.WriteString("\n\nTo supply one, do one of:\n")
	}

	url, name := "URL", "NAME"
	if pc != nil {
		url = pc.URL
		name = uniqueProjectName(cliConfigOrEmpty(), pc.Name, pc.URL)
	}
	if pc == nil {
		b.WriteString("  - run `lk cloud auth` in a terminal to sign in and add a project\n")
		if user != nil {
			fmt.Fprintf(&b, "  - re-run with --user-auth to use your signed-in account (%s)\n", userLabel(user))
		}
	} else if !req.optional {
		b.WriteString("  - re-run in a terminal and enter a key when asked (find or create one under Settings › API keys at " + dashboardURL + ", or ask a project admin)\n")
	}
	if req.dir != "" {
		file := req.files[0]
		fmt.Fprintf(&b, "  - add LIVEKIT_URL=%s, LIVEKIT_API_KEY and LIVEKIT_API_SECRET to %s\n", url, filepath.Join(req.dir, file))
	}
	fmt.Fprintf(&b, "  - save a key for lk to use: lk project add %s --url %s --api-key KEY --api-secret SECRET\n", name, url)
	b.WriteString("  - pass --api-key and --api-secret, or set LIVEKIT_API_KEY and LIVEKIT_API_SECRET")
	return &noKeyError{b.String()}
}

// keyAccepted checks creds with LiveKit, warning (naming where the key came
// from) when it's rejected. Anything short of a rejection counts as accepted.
func keyAccepted(ctx context.Context, creds agentCredentials, from string) bool {
	if err := checkAPIKey(ctx, creds.url, creds.apiKey, creds.apiSecret); err != nil {
		hint := ""
		if strings.Contains(from, ".env") {
			// lk doesn't rewrite the file, so say how to stop this repeating.
			hint = "; remove its LIVEKIT_API_KEY and LIVEKIT_API_SECRET lines, or replace them with a working key"
		}
		out.Warnf("LiveKit rejected API key [%s] from %s (deleted, or for another project); not using it%s", creds.apiKey, from, hint)
		return false
	}
	return true
}

// errKeyRejected is checkAPIKey's answer for a key the server refuses.
var errKeyRejected = errors.New("API key rejected")

// checkAPIKey asks the server at url whether it accepts the key, with one
// ListRooms call. Only a 401 returns errKeyRejected. LiveKit sends it as
// plain text, not twirp JSON, so twirp can't tell it from a proxy's, and
// every 401 counts. Anything else (offline, a timeout, the 403 of a suspended
// project) gives the key the benefit of the doubt, and the agent reports what
// it finds. A refusal replaces the key (under --user-auth, with a new one), so
// the check's token has no nbf and a day's exp, and a clock that's off by
// more than the server's minute of leeway can't make a good key look bad.
var checkAPIKey = func(ctx context.Context, url, apiKey, apiSecret string) error {
	if url == "" || apiKey == "" || apiSecret == "" {
		return nil
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":   apiKey,
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
		"video": map[string]any{"roomList": true},
	}).SignedString([]byte(apiSecret))
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, err = twirp.WithHTTPRequestHeaders(ctx, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		return nil
	}
	client := livekit.NewRoomServiceProtobufClient(signalling.ToHttpURL(url), &http.Client{})
	_, err = client.ListRooms(ctx, &livekit.ListRoomsRequest{})
	var terr twirp.Error
	if errors.As(err, &terr) && terr.Code() == twirp.Unauthenticated {
		return errKeyRejected
	}
	if err != nil {
		logger.Debugw("couldn't check API key", "url", url, "api-key", apiKey, "error", err)
	}
	return nil
}

// savedProjectKey returns the CLI config's API-key project for session
// project pc, matched by project id or URL, or nil.
func savedProjectKey(pc *config.ProjectConfig) *config.ProjectConfig {
	conf, err := config.LoadOrCreate()
	if err != nil {
		return nil
	}
	if p := findProjectEntry(conf, pc); p != nil && !isSessionProject(p) && p.APIKey != "" && p.APISecret != "" {
		c := *p
		return &c
	}
	return nil
}

func findProjectEntry(conf *config.CLIConfig, pc *config.ProjectConfig) *config.ProjectConfig {
	for i := range conf.Projects {
		p := &conf.Projects[i]
		if pc.ProjectId != "" && p.ProjectId == pc.ProjectId {
			return p
		}
	}
	for i := range conf.Projects {
		p := &conf.Projects[i]
		if pc.URL != "" && sameProjectURL(p.URL, pc.URL) {
			return p
		}
	}
	return nil
}

// saveProjectKey saves creds as pc's key in the CLI config: it updates pc's
// entry (by project id or URL), or adds one, made the default project when it's
// the first. Failing to save only warns: the key still works for this run.
func saveProjectKey(pc *config.ProjectConfig, creds agentCredentials) {
	conf, err := config.LoadOrCreate()
	if err != nil {
		out.Warnf("Couldn't save API key [%s] to the CLI config: %v", creds.apiKey, err)
		return
	}
	target := &config.ProjectConfig{Name: pc.Name, ProjectId: pc.ProjectId, URL: cmpOr(pc.URL, creds.url)}
	name := ""
	saved := "Saved API key [%s] to the CLI config as project [%s]"
	if p := findProjectEntry(conf, target); p != nil {
		if p.ProjectId == "" {
			p.ProjectId = pc.ProjectId
		}
		// Matched by id: the project may have moved since it was saved.
		moved := p.ProjectId == pc.ProjectId && target.URL != "" && p.URL != target.URL
		if p.APIKey == creds.apiKey && p.APISecret == creds.apiSecret {
			if !moved {
				return
			}
			saved = "Updated the URL of API key [%s]'s project [%s] in the CLI config"
		}
		if moved {
			p.URL = target.URL
		}
		p.APIKey, p.APISecret = creds.apiKey, creds.apiSecret
		name = p.Name
	} else {
		name = uniqueProjectName(conf, pc.Name, target.URL)
		conf.Projects = append(conf.Projects, config.ProjectConfig{
			Name: name, ProjectId: pc.ProjectId, URL: target.URL, APIKey: creds.apiKey, APISecret: creds.apiSecret,
		})
		if conf.DefaultProject == "" && len(conf.Projects) == 1 {
			conf.DefaultProject = name
		}
	}
	if err := conf.PersistQuietly(); err != nil {
		out.Warnf("Couldn't save API key [%s] to the CLI config: %v", creds.apiKey, err)
		return
	}
	if cliConfig != nil {
		cliConfig = conf
	}
	out.Statusf(saved, util.Accented(creds.apiKey), util.Accented(name))
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func cliConfigOrEmpty() *config.CLIConfig {
	if cliConfig != nil {
		return cliConfig
	}
	if conf, err := config.LoadOrCreate(); err == nil {
		return conf
	}
	return &config.CLIConfig{}
}

// uniqueProjectName returns a name for a new CLI config project that `lk
// project add` would accept and no project has: name made safe, else the URL's
// subdomain, with a numeric suffix when taken.
func uniqueProjectName(conf *config.CLIConfig, name, url string) string {
	base := strings.Trim(nameUnsafe.ReplaceAllString(name, "-"), "-")
	if base == "" {
		base = util.ExtractSubdomain(url)
	}
	if base == "" {
		base = "project"
	}
	candidate := base
	for i := 2; conf.ProjectExists(candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

// ensureProjectAPIKey gives the session project real API credentials, for
// commands that write them into an app in dir: a key for the same project
// already in one of dir's envFiles that LiveKit accepts, else the rest of the
// chain (projectKey).
// A project loaded with an API key is left alone.
func ensureProjectAPIKey(ctx context.Context, cmd *cli.Command, dir string, envFiles ...string) error {
	if !isSessionProject(project) {
		return nil
	}
	if dir != "" {
		creds, file, err := envFileCredentials(dir, envFiles...)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, file)
		if creds.apiKey != "" && (creds.url == "" || sameProjectURL(creds.url, project.URL)) &&
			keyAccepted(ctx, agentCredentials{url: project.URL, apiKey: creds.apiKey, apiSecret: creds.apiSecret}, path) {
			project = withAPIKey(project, creds.apiKey, creds.apiSecret)
			out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(path))
			return nil
		}
	}
	creds, err := projectKey(ctx, cmd, project, keyRequest{dir: dir, files: envFiles, purpose: "for the app"})
	if err != nil {
		return err
	}
	project = withAPIKey(project, creds.apiKey, creds.apiSecret)
	return nil
}

// credentialsOf returns pc's connection details.
func credentialsOf(pc *config.ProjectConfig) agentCredentials {
	if pc == nil {
		return agentCredentials{}
	}
	return agentCredentials{url: pc.URL, apiKey: pc.APIKey, apiSecret: pc.APISecret}
}

// withAPIKey returns a copy of pc carrying a real API key, which ends its
// session-project status.
func withAPIKey(pc *config.ProjectConfig, apiKey, apiSecret string) *config.ProjectConfig {
	c := *pc
	c.APIKey, c.APISecret = apiKey, apiSecret
	return &c
}

func sameProjectURL(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// appEnvFiles lists the env files to look for existing credentials in: the
// destination file a command writes, then the usual ones.
func appEnvFiles(dest string) []string {
	files := []string{}
	if dest != "" {
		files = append(files, dest)
	}
	for _, f := range agentEnvFiles {
		if f != dest {
			files = append(files, f)
		}
	}
	return files
}

// nameUnsafe matches what `lk project add` doesn't allow in a project name.
var nameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_\-]+`)
