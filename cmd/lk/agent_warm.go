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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A Python dev reload used to pay for a fresh interpreter importing the SDK
// and its dependencies (~0.5–1 s) although only the user's own files changed.
// Instead, a spare interpreter imports every installed module the last worker
// used, then waits. A reload activates it, so only the user's code is imported.

// pythonDevBootstrap stands in for `-m livekit.agents`. With LK_DEV_WARM=1 it
// preloads the modules recorded in LK_DEV_PRELOAD_FILE until lk writes a line to
// stdin, and exits if stdin closes first. It then runs livekit.agents as
// `python -m` would. Every run records its installed modules for the next spare.
const pythonDevBootstrap = `import importlib, os, sys, threading, runpy
def _lk_dev():
    path = os.environ.pop("LK_DEV_PRELOAD_FILE", "")
    warm = os.environ.pop("LK_DEV_WARM", "") == "1"
    project = os.path.realpath(os.getcwd())
    roots = tuple({os.path.join(os.path.realpath(p), "") for p in (sys.prefix, sys.base_prefix, sys.exec_prefix, sys.base_exec_prefix)})
    def ours(name):
        # A module the project itself provides must be imported fresh after a reload,
        # even when a stale copy of it is also installed.
        top = name.partition(".")[0]
        return any(os.path.exists(os.path.join(project, d, top + ext)) for d in ("", "src") for ext in ("", ".py"))
    if warm:
        go = threading.Event()
        def wait():
            if not sys.stdin.readline():
                os._exit(0)
            go.set()
        threading.Thread(target=wait, daemon=True).start()
        try:
            with open(path) as f:
                names = f.read().split()
        except OSError:
            names = []
        for name in ["livekit.agents"] + names:
            if go.is_set():
                break
            if name not in sys.modules and not ours(name):
                try:
                    importlib.import_module(name)
                except BaseException:
                    pass
        go.wait()
    def record():
        names = []
        for name, module in list(sys.modules.items()):
            file = getattr(module, "__file__", None)
            if file and not name.startswith("__") and os.path.realpath(file).startswith(roots) and not ours(name):
                names.append(name)
        try:
            tmp = "%s.%d" % (path, os.getpid())
            with open(tmp, "w") as f:
                f.write("\n".join(names))
            os.replace(tmp, path)
        except OSError:
            pass
    if path:
        timer = threading.Timer(3, record)
        timer.daemon = True
        timer.start()
        import atexit
        atexit.register(record)
_lk_dev()
del _lk_dev
sys.argv = ["livekit.agents"] + sys.argv[1:]
runpy.run_module("livekit.agents", run_name="__main__", alter_sys=True)
`

// pythonPreloadFile is where the project's dev workers record the installed
// modules they import, kept across sessions so the first reload is warm too.
func pythonPreloadFile(dir string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(abs))
	path := filepath.Join(cache, "livekit", "dev-preload", hex.EncodeToString(sum[:8])+".txt")
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return ""
	}
	return path
}

// pythonEnvFingerprint changes when the project's dependencies change, so a
// spare that preloaded the old ones is discarded instead of activated.
func pythonEnvFingerprint(dir string) string {
	paths := []string{"pyproject.toml", "uv.lock", "requirements.txt", "poetry.lock", "Pipfile.lock"}
	for _, pattern := range []string{".venv/lib/python*/site-packages", ".venv/Lib/site-packages"} {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
		paths = append(paths, matches...)
	}
	var b strings.Builder
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if info, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s=%d;", p, info.ModTime().UnixNano())
		}
	}
	return b.String()
}
