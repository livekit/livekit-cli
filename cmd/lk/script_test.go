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

// The scripts in testdata/script run lk the way a user types it. Each is a
// txtar archive: a testscript script (see
// https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) followed by
// the files the run starts with. $HOME is $WORK/home, so a case's CLI config
// is its home/.livekit/cli-config.yaml file.
//
// `exec lk` runs this test binary as lk, through main, in a process of its
// own: lk's flags are package-level and cli/v3 keeps their parse state, so
// two runs can't share a process.
//
// Every script gets a fake LiveKit Cloud: one HTTP server, set as lk's proxy,
// that answers for any http://NAME.test host. Cases point lk at it by name,
// e.g. api_url: http://publicapi.test on a signed-in user, or url:
// ws://one.test on a configured project, so no expected output holds a port.
// Anything else lk sends through the proxy, such as the real Public API over
// https, is refused and logged. Go never proxies localhost or loopback
// addresses, so a request to one of those bypasses the fake and isn't logged.
// Two script commands drive it:
//
//	respond METHOD URL STATUS [BODY]
//		Answer METHOD URL (any query) with STATUS and BODY. A request with
//		no answer gets 501. A Public API body (a /v0 path) must be what the
//		spec says that route sends: see checkPublicAPIBody.
//	requests
//		Print every request the fake received, in order, with the
//		credential it carried (a session token as bearer=, or as apikey= the
//		API key that issued an access token, read from its claims without
//		checking the signature) and the project it named.
//
// To regenerate expected output after an intended change, run
//
//	UPDATE_LK_SCRIPTS=1 go test ./cmd/lk -run TestScripts -count=1
//
// and review the diff. Every `cmp` whose expected file is in the archive is
// rewritten with what the run produced; regexp checks (stdout, stderr) are not.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/rogpeppe/go-internal/testscript"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"lk": main})
}

func TestScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Three things stop the scripts on Windows: Go reports mode 0666 for
		// every file there, so config.LoadOrCreate warns on each run that the
		// config file should be 0600; testscript sets the home directory
		// through USERPROFILE, which setupScript doesn't; and a Windows
		// checkout writes the .txtar files with CRLF endings.
		t.Skip("lk warns about the config file's permissions on every Windows run, and the scripts need USERPROFILE and LF checkouts")
	}
	testscript.Run(t, testscript.Params{
		Dir:   filepath.Join("testdata", "script"),
		Setup: setupScript,
		Cmds: map[string]func(*testscript.TestScript, bool, []string){
			"respond":  cmdRespond,
			"requests": cmdRequests,
		},
		UpdateScripts:       os.Getenv("UPDATE_LK_SCRIPTS") != "",
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// fakeCloudKey finds the script's fakeCloud in its testscript values.
type fakeCloudKey struct{}

func setupScript(env *testscript.Env) error {
	home := filepath.Join(env.WorkDir, "home")
	env.Setenv("HOME", home)
	// lk warns about a config file anyone else can read, and txtar files are
	// written 0644.
	err := os.Chmod(filepath.Join(home, ".livekit", "cli-config.yaml"), 0o600)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	cloud := newFakeCloud()
	env.Defer(cloud.Close)
	env.Values[fakeCloudKey{}] = cloud
	env.Setenv("HTTP_PROXY", cloud.URL)
	env.Setenv("HTTPS_PROXY", cloud.URL)
	// Under -race, lk would otherwise sleep a second before each clean exit.
	env.Setenv("GORACE", strings.TrimSpace(env.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return nil
}

// fakeCloud is an HTTP proxy that serves canned responses for any .test host
// and refuses everything else. It logs every request it receives.
type fakeCloud struct {
	*httptest.Server

	mu        sync.Mutex
	responses map[string]cannedResponse // by "METHOD URL", without the query
	log       []string

	// mint makes the fake answer a Public API token request it has no canned
	// response for as the Public API does: with an access token carrying the
	// requested grants, signed with mintKey(project). A request without one of
	// the sessions as its bearer gets 401.
	mint     bool
	sessions map[string]bool
	// secrets are the API secrets the fake knows, by API key. A request whose
	// access token is signed by a known key with the wrong secret is logged
	// with badsig.
	secrets map[string]string
}

type cannedResponse struct {
	status int
	body   string
}

func newFakeCloud() *fakeCloud {
	f := &fakeCloud{responses: map[string]cannedResponse{}, secrets: map[string]string{}, sessions: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// projectInPath finds the project id in a Public API path such as
// /v0/projects/p_1/keys or /v0/workspaces/w_1/projects/p_1.
var projectInPath = regexp.MustCompile(`/projects/([^/:]+)`)

// tokenRequestPath matches a Public API token request, capturing the project.
var tokenRequestPath = regexp.MustCompile(`^/v0/projects/([^/:]+)/tokens$`)

// mintKey is the API key the fake signs a project's minted tokens with.
func mintKey(projectID string) string { return "APImint_" + projectID }

const mintSecret = "secretmint"

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// lk reaches a .test host through the proxy as plain HTTP. An https URL
	// arrives as a CONNECT tunnel, which the fake never opens.
	if r.Method == http.MethodConnect || !strings.HasSuffix(r.URL.Hostname(), ".test") {
		f.log = append(f.log, fmt.Sprintf("%s %s refused: not a fake host", r.Method, r.Host))
		http.Error(w, "fake cloud: lk tried to reach "+r.Host, http.StatusForbidden)
		return
	}

	target := r.Method + " " + r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
	line := target
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if v, err := auth.ParseAPIToken(token); err == nil {
			line += " apikey=" + v.APIKey()
			if secret, known := f.secrets[v.APIKey()]; known {
				if _, _, err := v.Verify(secret); err != nil {
					line += " badsig"
				}
			}
		} else {
			line += " bearer=" + token
		}
	}
	if m := projectInPath.FindStringSubmatch(r.URL.Path); m != nil {
		line += " project=" + m[1]
	}
	f.log = append(f.log, line)

	res, ok := f.responses[target]
	if m := tokenRequestPath.FindStringSubmatch(r.URL.Path); !ok && f.mint && r.Method == http.MethodPost && m != nil {
		res, ok = f.mintToken(r, m[1]), true
	}
	if !ok {
		res = cannedResponse{http.StatusNotImplemented, `{"code":12,"message":"fake cloud: no response for ` + target + `"}`}
		if strings.HasPrefix(r.URL.Path, "/twirp/") {
			res.body = `{"code":"unimplemented","msg":"fake cloud: no response for ` + target + `"}`
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.status)
	fmt.Fprint(w, res.body)
}

// mintToken answers a token request for project as the Public API's
// CreateToken does: an access token with the requested grants, signed with
// mintKey(project). The caller holds f.mu.
func (f *fakeCloud) mintToken(r *http.Request, project string) cannedResponse {
	if bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); !f.sessions[bearer] {
		return cannedResponse{http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"authentication required"}}`}
	}
	var req struct {
		Grants auth.ClaimGrants `json:"grants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return cannedResponse{http.StatusBadRequest, `{"code":3,"message":"fake cloud: bad token request"}`}
	}
	at := auth.NewAccessToken(mintKey(project), mintSecret).SetValidFor(time.Hour)
	*at.GetGrants() = req.Grants
	token, err := at.ToJWT()
	if err != nil {
		return cannedResponse{http.StatusInternalServerError, `{"code":13,"message":"fake cloud: ` + err.Error() + `"}`}
	}
	f.secrets[mintKey(project)] = mintSecret
	expiresAt := time.Now().Add(time.Hour).UTC()
	body, err := json.Marshal(oapi.LivekitPublicapiProjectsV1CreateTokenResponse{Token: &token, ExpiresAt: &expiresAt})
	if err != nil {
		return cannedResponse{http.StatusInternalServerError, `{"code":13,"message":"fake cloud: ` + err.Error() + `"}`}
	}
	return cannedResponse{http.StatusOK, string(body)}
}

// cmdRespond implements `respond METHOD URL STATUS [BODY]`.
func cmdRespond(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! respond")
	}
	if len(args) != 3 && len(args) != 4 {
		ts.Fatalf("usage: respond METHOD URL STATUS [BODY]")
	}
	status, err := strconv.Atoi(args[2])
	ts.Check(err)
	body := ""
	if len(args) == 4 {
		body = args[3]
	}
	ts.Check(ts.Value(fakeCloudKey{}).(*fakeCloud).respond(args[0]+" "+args[1], status, body))
}

// cmdRequests implements `requests`.
func cmdRequests(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: requests")
	}
	// Taking Stdout even with nothing to print replaces the last command's
	// stdout, so `! stdout .` checks that no request arrived.
	stdout := ts.Stdout()
	for _, line := range ts.Value(fakeCloudKey{}).(*fakeCloud).requests() {
		fmt.Fprintln(stdout, line)
	}
}

// respond answers target ("METHOD URL") with status and body, once
// checkPublicAPIBody accepts the body.
func (f *fakeCloud) respond(target string, status int, body string) error {
	method, rawURL, _ := strings.Cut(target, " ")
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if err := checkPublicAPIBody(method, u.Path, status, body); err != nil {
		return fmt.Errorf("respond %s %d: %w", target, status, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[method+" "+u.Scheme+"://"+u.Host+u.Path] = cannedResponse{status, body}
	return nil
}

// publicAPIRoutes are the Public API routes the fake answers, each with the
// generated type of its 200 body.
var publicAPIRoutes = []struct {
	method string
	path   *regexp.Regexp
	body   func() any
}{
	{http.MethodGet, regexp.MustCompile(`^/v0/projects$`), func() any { return new(oapi.LivekitPublicapiProjectsV1ListProjectsResponse) }},
	{http.MethodPatch, regexp.MustCompile(`^/v0/projects/[^/]+$`), func() any { return new(oapi.LivekitPublicapiProjectsV1UpdateProjectResponse) }},
	{http.MethodGet, regexp.MustCompile(`^/v0/projects/[^/]+/keys$`), func() any { return new(oapi.LivekitPublicapiProjectsV1ListKeysResponse) }},
	{http.MethodPost, regexp.MustCompile(`^/v0/projects/[^/]+/keys$`), func() any { return new(oapi.LivekitPublicapiProjectsV1GenerateKeyResponse) }},
	{http.MethodDelete, regexp.MustCompile(`^/v0/projects/[^/]+/keys/[^/]+$`), func() any { return new(oapi.LivekitPublicapiProjectsV1DeleteKeyResponse) }},
	{http.MethodGet, regexp.MustCompile(`^/v0/projects/[^/]+/members$`), func() any { return new(oapi.LivekitPublicapiProjectsV1ListMembersResponse) }},
}

// middlewareError is the body the Public API's auth middleware and panic
// recovery send for a 401 or 500. The spec describes it only in prose, so
// it has no generated type; it goes away when they send a google.rpc.Status.
type middlewareError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// checkPublicAPIBody checks a canned body for a Public API route (a /v0
// path) against the client generated from the Public API's spec: a 2xx body
// must decode into the route's type in publicAPIRoutes, and an error body
// into a google.rpc.Status, or a middlewareError for a 401 or 500. Decoding
// is strict, so a field the spec doesn't have fails, and a regeneration that
// changes a type fails the scripts that send its old shape.
func checkPublicAPIBody(method, path string, status int, body string) error {
	if !strings.HasPrefix(path, "/v0/") {
		return nil
	}
	var probe struct {
		Error json.RawMessage `json:"error"`
	}
	var v any
	switch {
	case (status == http.StatusUnauthorized || status == http.StatusInternalServerError) && json.Unmarshal([]byte(body), &probe) == nil && probe.Error != nil:
		v = new(middlewareError)
	case status >= 400:
		v = new(oapi.GoogleRpcStatus)
	default:
		for _, r := range publicAPIRoutes {
			if r.method == method && r.path.MatchString(path) {
				v = r.body()
				break
			}
		}
		if v == nil {
			return fmt.Errorf("no generated type for %s %s; add the route to publicAPIRoutes", method, path)
		}
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("body doesn't match the spec's %T: %w", v, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("body has data after its JSON value")
	}
	return nil
}

// requests returns the log of every request the fake received.
func (f *fakeCloud) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}
