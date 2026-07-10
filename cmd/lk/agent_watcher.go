// Copyright 2021-2024 LiveKit, Inc.
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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// skipDirs are directories to never watch.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"__pycache__": true, ".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	".venv": true, "venv": true, "env": true,
	"node_modules": true, ".next": true, "dist": true, "build": true,
}

// watchExtensions returns file extensions to watch for a project type.
func watchExtensions(pt agentfs.ProjectType) map[string]bool {
	if pt.IsPython() {
		return map[string]bool{".py": true}
	}
	return map[string]bool{".js": true, ".ts": true, ".mjs": true, ".mts": true}
}

// firstPythonCLIAddrVersion is the first Python livekit-agents release that
// accepts --cli-addr; releases up to and including 1.6.5 only accept the legacy
// --reload-addr.
const firstPythonCLIAddrVersion = "1.6.6"

// firstNodeCLIAddrVersion is the first @livekit/agents release that accepts
// --cli-addr; releases up to and including 1.5.0 reject unknown options, so
// older SDKs must not be passed any dev-channel flag.
const firstNodeCLIAddrVersion = "1.5.1"

// devChannelAddrFlag picks the CLI flag used to hand the agent the dev-channel
// address, or "" when the installed SDK predates any flag it would accept. The
// installed version is resolved via the project's interpreter so symlinked/
// workspace deps and loose constraints report what will actually run.
func devChannelAddrFlag(config AgentStartConfig) string {
	return devChannelAddrFlagFor(config.ProjectType, agentfs.ResolveInstalledSDKVersion(config.Dir, config.Entrypoint, config.ProjectType))
}

// devChannelAddrFlagFor implements the flag choice. The flag was renamed
// --reload-addr -> --cli-addr (it now carries more than reloads), but an SDK
// must not be handed a flag it predates, so anything not positively new
// enough — including an undetermined version — falls back to what released
// SDKs accept: the legacy --reload-addr for Python, no flag at all for Node
// (whose CLI hard-fails on unknown options).
func devChannelAddrFlagFor(projectType agentfs.ProjectType, installedVersion string) string {
	newEnough := func(minVersion string) bool {
		if installedVersion == "" {
			return false
		}
		ok, err := agentfs.IsVersionSatisfied(installedVersion, minVersion)
		return err == nil && ok
	}
	switch {
	case projectType.IsPython():
		if newEnough(firstPythonCLIAddrVersion) {
			return "--cli-addr"
		}
		return "--reload-addr"
	case projectType.IsNode():
		if newEnough(firstNodeCLIAddrVersion) {
			return "--cli-addr"
		}
		return ""
	}
	return ""
}

// agentWatcher watches for file changes and restarts an agent subprocess.
type agentWatcher struct {
	config    AgentStartConfig
	exts      map[string]bool
	debounce  time.Duration
	watcher   *fsnotify.Watcher
	agent     *AgentProcess
	restartCh chan struct{}

	devSrv  *devServer
	session *devSession
}

func newAgentWatcher(config AgentStartConfig) (*agentWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Walk directory tree and add all non-skip directories
	err = filepath.Walk(config.Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		name := info.Name()
		if skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".") {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("failed to setup file watcher: %w", err)
	}

	// The dev server backs two things over one channel: the ServerInfo the agent
	// reports on connect (e.g. for the Cloud console link) and the reload protocol
	// (capture running jobs from the old process, restore them in the new one). It
	// is created for every agent type so ServerInfo works; the job capture/restore
	// is Python-only and gated in restart() (Node reloads are a plain kill+respawn).
	rs, err := newDevServer()
	if err != nil {
		w.Close()
		return nil, err
	}
	rs.onServerInfo = config.OnServerInfo
	// The agent connects back to this address over the dev channel.
	if addrFlag := devChannelAddrFlag(config); addrFlag != "" {
		config.CLIArgs = append(config.CLIArgs, addrFlag, rs.addr())
	}

	return &agentWatcher{
		config:    config,
		exts:      watchExtensions(config.ProjectType),
		debounce:  500 * time.Millisecond,
		watcher:   w,
		restartCh: make(chan struct{}, 1),
		devSrv:    rs,
	}, nil
}

func (aw *agentWatcher) start() error {
	agent, err := startAgent(aw.config)
	if err != nil {
		return err
	}
	aw.agent = agent

	aw.acceptSession()

	return nil
}

// acceptSession waits (in the background) for the next process to connect back on
// the reload channel and hands the connection to a devSession read loop.
func (aw *agentWatcher) acceptSession() {
	go func() {
		conn, err := aw.devSrv.listener.Accept()
		if err != nil {
			return
		}
		s := aw.devSrv.newSession(conn)
		aw.session = s
		go s.run()
	}()
}

func (aw *agentWatcher) restart() error {
	// 1. Capture active jobs from the current process (best-effort). Job
	// capture/restore is Python-only; Node reloads are a plain kill+respawn.
	if aw.session != nil {
		if aw.config.ProjectType.IsPython() {
			aw.devSrv.captureJobs(aw.session)
		}
		aw.session.close()
		aw.session = nil
	}

	// 2. Kill old process
	if aw.agent != nil {
		aw.agent.Kill()
	}

	out.Status("Reloading agent...")

	// 3. Start new process
	agent, err := startAgent(aw.config)
	if err != nil {
		return err
	}
	aw.agent = agent

	// 4. Accept new connection and serve restored jobs
	aw.acceptSession()

	return nil
}

// Run watches for file changes and restarts the agent. Blocks until done is closed.
func (aw *agentWatcher) Run(done <-chan struct{}) error {
	if err := aw.start(); err != nil {
		return err
	}
	defer func() {
		if aw.agent != nil {
			// If Shutdown() was already called by the signal forwarder,
			// just wait for exit. Otherwise send SIGINT ourselves.
			if !aw.agent.shutdownCalled {
				aw.agent.Shutdown()
			}
			select {
			case <-aw.agent.exitCh:
			case <-time.After(5 * time.Second):
				aw.agent.ForceKill()
			}
		}
		if aw.session != nil {
			aw.session.close()
		}
		aw.devSrv.close()
		aw.watcher.Close()
	}()

	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time
	exitCh := aw.agent.exitCh
	var changedFile string

	for {
		select {
		case <-done:
			return nil

		case event, ok := <-aw.watcher.Events:
			if !ok {
				return nil
			}
			// Only trigger on relevant file extensions
			if !aw.exts[filepath.Ext(event.Name)] {
				continue
			}
			// Only care about writes, creates, renames
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			// Add new directories to the watch list
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = aw.watcher.Add(event.Name)
				}
			}
			// Track the file that triggered the change
			if rel, err := filepath.Rel(aw.config.Dir, event.Name); err == nil {
				changedFile = rel
			} else {
				changedFile = event.Name
			}
			// Start or reset debounce timer
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(aw.debounce)
				debounceCh = debounceTimer.C
			} else {
				debounceTimer.Reset(aw.debounce)
			}

		case <-debounceCh:
			debounceTimer = nil
			debounceCh = nil
			out.Statusf("File changed: %s", changedFile)
			if err := aw.restart(); err != nil {
				out.Warnf("Failed to restart agent: %v", err)
				out.Status("Waiting for file changes...")
			} else {
				exitCh = aw.agent.exitCh
			}

		case err, ok := <-aw.watcher.Errors:
			if !ok {
				return nil
			}
			out.Warnf("Watcher error: %v", err)

		case <-exitCh:
			// Nil the channel so this case won't fire again (nil channels block forever)
			exitCh = nil
			// Drain any pending debounce - don't restart immediately
			if debounceTimer != nil {
				debounceTimer.Stop()
				debounceTimer = nil
				debounceCh = nil
			}
			out.Status("Agent exited. Waiting for file changes to restart...")
		}
	}
}
