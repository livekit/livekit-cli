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
// own. lk's package-level state (flags, the resolved project, the output
// Printer) and anything written straight to os.Stdout stay in that process.
//
// Every script gets a fake LiveKit Cloud: one HTTP server, set as lk's proxy,
// that answers for any http://NAME.test host. Cases point lk at it by name,
// e.g. api_url: http://publicapi.test on a signed-in user, or
// --server-url http://cloudapi.test, so no expected output holds a port.
// Anything else lk tries to reach, such as the real Public API over https, is
// refused and logged. Two script commands drive it:
//
//	respond METHOD URL STATUS [BODY]
//		Answer METHOD URL (any query) with STATUS and BODY. A request with
//		no answer gets 501.
//	requests
//		Print every request the fake received, in order, with the
//		credential it carried (a session token as bearer=, or the API key an
//		access token is signed with as apikey=) and the project it named.
//
// To regenerate expected output after an intended change, run
//
//	UPDATE_LK_SCRIPTS=1 go test ./cmd/lk -run TestScripts -count=1
//
// and review the diff. Every `cmp` whose expected file is in the archive is
// rewritten with what the run produced; regexp checks (stdout, stderr) are not.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"lk": main})
}

func TestScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Every Windows file reports mode 0666, so lk warns on each run that
		// the config file should be 0600 and no expected stderr would match.
		t.Skip("lk warns about the config file's permissions on every Windows run")
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

	// lk's first-run offer to install coding agent skills (#1056) prints a
	// tip whenever it finds an agent on this machine. This is its opt-out.
	env.Setenv("LK_NO_SKILLS_PROMPT", "1")

	cloud := newFakeCloud()
	env.Defer(cloud.Close)
	env.Values[fakeCloudKey{}] = cloud
	env.Setenv("HTTP_PROXY", cloud.URL)
	env.Setenv("HTTPS_PROXY", cloud.URL)
	return nil
}

// fakeCloud is an HTTP proxy that serves canned responses for any .test host
// and refuses everything else. It logs every request it receives.
type fakeCloud struct {
	*httptest.Server

	mu        sync.Mutex
	responses map[string]cannedResponse // by "METHOD URL", without the query
	log       []string
}

type cannedResponse struct {
	status int
	body   string
}

func newFakeCloud() *fakeCloud {
	f := &fakeCloud{responses: map[string]cannedResponse{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// projectInPath finds the project id in a Public API path such as
// /v0/projects/p_1/keys or /v0/workspaces/w_1/projects/p_1.
var projectInPath = regexp.MustCompile(`/projects/([^/:]+)`)

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
		if apiKey, ok := jwtIssuer(token); ok {
			line += " apikey=" + apiKey
		} else {
			line += " bearer=" + token
		}
	}
	if m := projectInPath.FindStringSubmatch(r.URL.Path); m != nil {
		line += " project=" + m[1]
	}
	f.log = append(f.log, line)

	res, ok := f.responses[target]
	if !ok {
		res = cannedResponse{http.StatusNotImplemented, `{"code":"unimplemented","message":"fake cloud: no response for ` + target + `"}`}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.status)
	fmt.Fprint(w, res.body)
}

// jwtIssuer returns the issuer of a LiveKit access token, which is the API
// key that signed it. The token's other claims change on every run.
func jwtIssuer(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer == "" {
		return "", false
	}
	return claims.Issuer, true
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
	res := cannedResponse{status: status}
	if len(args) == 4 {
		res.body = args[3]
	}
	ts.Value(fakeCloudKey{}).(*fakeCloud).respond(args[0]+" "+args[1], res.status, res.body)
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

// respond answers target ("METHOD URL") with status and body.
func (f *fakeCloud) respond(target string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[target] = cannedResponse{status, body}
}

// requests returns the log of every request the fake received.
func (f *fakeCloud) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}
