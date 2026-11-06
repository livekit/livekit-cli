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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/livekit/livekit-cli/v2/pkg/bootstrap"
	"github.com/livekit/livekit-cli/v2/pkg/config"
	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// API keys for apps and agents.
//
// The CLI's own calls don't need an API key under --user-auth (see
// session_auth.go), but an app or agent runs on its own and does. Every
// command that hands a key to an app or agent gets it from one chain, in both
// auth modes:
//
//  1. --api-key/--api-secret, or LIVEKIT_API_KEY/LIVEKIT_API_SECRET, as given
//     (never checked, saved, or replaced: they're the user's override).
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
// supply a key, before any agent starts. A key LiveKit rejects (deleted, or
// for another project) at steps 2 and 3 is skipped with a warning, and the
// next step replaces it.
//
// When lk creates a key: only at step 4, so only under --user-auth, only in
// the commands that hand a key to an app or agent (agent dev, start, console,
// debugger and init; app create and env), and only when steps 1-3 found no
// usable key. Commands that only call LiveKit themselves (room, sip, egress,
// ...) use session tokens and never create one. agent simulate stays
// API-key only for now; once its simulation calls move to the Public API, the
// agent it spawns can get its key from projectKey too.
//
// Where lk keeps it: a key from steps 3-5 is saved to the CLI config (so other
// agents and later runs on this machine reuse it rather than create another)
// and written to the agent's env file, so the agent also runs without lk
// (`python agent.py console`, an IDE, docker compose). An env file lk writes
// to is added to .gitignore when the agent is in a git repository.
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

// keyRequest describes what a key is wanted for, for the prompt, the error
// when there's none, and where to write it.
type keyRequest struct {
	// dir is the agent's or app's directory, or "" when there is none.
	dir string
	// files are dir's env files, in priority order.
	files []string
	// purpose finishes "no LiveKit API key ...", e.g. "to run the agent with".
	purpose string
}

// resolveAgentCredentials returns the credentials the agent in dir runs with,
// from the chain at the top of this file, and saves a key the chain found or
// made (see saveProjectKey, writeAgentEnvKey).
func resolveAgentCredentials(ctx context.Context, cmd *cli.Command, dir string) (agentCredentials, error) {
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

	req := keyRequest{dir: dir, files: agentEnvFileNames(dir), purpose: "to run the agent with"}
	fileCreds, file, err := envFileCredentials(dir, req.files...)
	if err != nil {
		return agentCredentials{}, err
	}

	// A key and secret given without a URL only need the project's URL.
	if explicit.apiKey != "" && explicit.apiSecret != "" {
		if fileCreds.url != "" && !namedProject(cmd) {
			return mergeCredentials(explicit, fileCreds), nil
		}
		pc, err := resolveAgentProject(ctx, cmd, req)
		if err != nil {
			return agentCredentials{}, err
		}
		return mergeCredentials(explicit, credentialsOf(pc)), nil
	}

	// The agent's own key needs no project lookup, unless a project was named
	// (the key must be for it) or the file has no URL.
	if fileCreds.apiKey != "" && !namedProject(cmd) {
		creds := mergeCredentials(explicit, fileCreds)
		if creds.url == "" {
			pc, err := resolveAgentProject(ctx, cmd, req)
			if err != nil {
				return agentCredentials{}, err
			}
			creds.url = pc.URL
		}
		if keyAccepted(ctx, creds, filepath.Join(dir, file)) {
			out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(filepath.Join(dir, file)))
			return creds, nil
		}
		fileCreds = agentCredentials{}
	}

	pc, err := resolveAgentProject(ctx, cmd, req)
	if err != nil {
		return agentCredentials{}, err
	}
	if fileCreds.apiKey != "" {
		// --project or --subdomain named pc.
		path := filepath.Join(dir, file)
		if fileCreds.url != "" && !sameProjectURL(fileCreds.url, pc.URL) {
			out.Statusf("Not using the API key in %s: it's for %s, not project [%s]", util.Accented(path), fileCreds.url, util.Accented(pc.Name))
		} else {
			creds := mergeCredentials(explicit, agentCredentials{url: pc.URL, apiKey: fileCreds.apiKey, apiSecret: fileCreds.apiSecret})
			if keyAccepted(ctx, creds, path) {
				out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(path))
				return creds, nil
			}
		}
	}

	key, err := projectKey(ctx, cmd, pc, req)
	if err != nil {
		return agentCredentials{}, err
	}
	writeAgentEnvKey(dir, req.files, key)
	return mergeCredentials(explicit, key), nil
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
	if SkipPrompts(cmd) {
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
	if SkipPrompts(cmd) {
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
	b.WriteString("\n\nTo supply one, do one of:\n")

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
	} else {
		b.WriteString("  - re-run in a terminal and enter a key when asked (find or create one under Settings › API keys at " + dashboardURL + ", or ask a project admin)\n")
	}
	if req.dir != "" {
		file := req.files[0]
		fmt.Fprintf(&b, "  - add LIVEKIT_URL=%s, LIVEKIT_API_KEY and LIVEKIT_API_SECRET to %s\n", url, filepath.Join(req.dir, file))
	}
	fmt.Fprintf(&b, "  - save a key for lk to use: lk project add %s --url %s --api-key KEY --api-secret SECRET\n", name, url)
	b.WriteString("  - pass --api-key and --api-secret, or set LIVEKIT_API_KEY and LIVEKIT_API_SECRET")
	return errors.New(b.String())
}

// keyAccepted checks creds with LiveKit, warning (naming where the key came
// from) when it's rejected. Anything short of a rejection counts as accepted.
func keyAccepted(ctx context.Context, creds agentCredentials, from string) bool {
	if err := checkAPIKey(ctx, creds.url, creds.apiKey, creds.apiSecret); err != nil {
		out.Warnf("LiveKit rejected API key [%s] from %s (deleted, or for another project); not using it", creds.apiKey, from)
		return false
	}
	return true
}

// errKeyRejected is checkAPIKey's answer for a key the server refuses.
var errKeyRejected = errors.New("API key rejected")

// checkAPIKey asks the server at url whether it accepts the key, with one
// ListRooms call. Only a definite refusal returns errKeyRejected: when the
// server can't be asked (offline, a timeout, anything else) the key is given
// the benefit of the doubt and the agent reports what it finds.
var checkAPIKey = func(ctx context.Context, url, apiKey, apiSecret string) error {
	if url == "" || apiKey == "" || apiSecret == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := lksdk.NewRoomServiceClient(url, apiKey, apiSecret).ListRooms(ctx, &livekit.ListRoomsRequest{})
	var terr twirp.Error
	if errors.As(err, &terr) && (terr.Code() == twirp.Unauthenticated || terr.Code() == twirp.PermissionDenied) {
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
	if p := findProjectEntry(conf, target); p != nil {
		if p.APIKey == creds.apiKey && p.APISecret == creds.apiSecret {
			return
		}
		p.APIKey, p.APISecret = creds.apiKey, creds.apiSecret
		if p.ProjectId == "" {
			p.ProjectId = pc.ProjectId
		}
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
	out.Statusf("Saved API key [%s] to the CLI config as project [%s]", util.Accented(creds.apiKey), util.Accented(name))
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

// writeAgentEnvKey writes creds into the agent's env file in dir: the first of
// files that exists, else the first. It leaves alone a file whose LIVEKIT_URL
// is for another project, and adds the file it writes to .gitignore.
func writeAgentEnvKey(dir string, files []string, creds agentCredentials) {
	if dir == "" || len(files) == 0 {
		return
	}
	file := files[0]
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			file = f
			break
		}
	}
	path := filepath.Join(dir, file)
	env, err := bootstrap.ReadDotEnv(dir, file)
	if err != nil {
		out.Warnf("Couldn't save API key [%s] to %s: %v", creds.apiKey, path, err)
		return
	}
	if u := env["LIVEKIT_URL"]; u != "" && !sameProjectURL(u, creds.url) {
		out.Warnf("Not saving API key [%s] to %s: its LIVEKIT_URL is for another project (%s)", creds.apiKey, path, u)
		return
	}
	if env["LIVEKIT_API_KEY"] == creds.apiKey && env["LIVEKIT_API_SECRET"] == creds.apiSecret && env["LIVEKIT_URL"] != "" {
		return
	}
	if err := bootstrap.WriteDotEnv(dir, file, map[string]string{
		"LIVEKIT_URL":        creds.url,
		"LIVEKIT_API_KEY":    creds.apiKey,
		"LIVEKIT_API_SECRET": creds.apiSecret,
	}, false); err != nil {
		out.Warnf("Couldn't save API key [%s] to %s: %v", creds.apiKey, path, err)
		return
	}
	out.Statusf("Saved API key [%s] to %s", util.Accented(creds.apiKey), util.Accented(path))
	gitIgnore(dir, file)
}

// gitIgnore adds file to dir's .gitignore when dir is in a git repository that
// doesn't ignore it already, so a key lk wrote isn't committed. Without git, or
// outside a repository, it does nothing.
func gitIgnore(dir, file string) {
	err := exec.Command("git", "-C", dir, "check-ignore", "-q", "--", file).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		// 0: ignored already; 128 or no git: not a repository
		return
	}
	path := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		out.Warnf("Couldn't add %s to %s: %v", file, path, err)
		return
	}
	line := file + "\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		line = "\n" + line
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString(line)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		out.Warnf("Couldn't add %s to %s: %v", file, path, err)
		return
	}
	out.Statusf("Added %s to %s so the key isn't committed", util.Accented(file), util.Accented(path))
}

// ensureProjectAPIKey gives the session project real API credentials, for
// commands that write them into an app in dir: a key for the same project
// already in one of dir's envFiles, else the rest of the chain (projectKey).
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
		if creds.apiKey != "" && (creds.url == "" || sameProjectURL(creds.url, project.URL)) {
			project = withAPIKey(project, creds.apiKey, creds.apiSecret)
			out.Statusf("Using API key [%s] from %s", util.Accented(creds.apiKey), util.Accented(filepath.Join(dir, file)))
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
