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
	"net"
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

// agentWatcher watches for file changes and restarts an agent subprocess.
type agentWatcher struct {
	config    AgentStartConfig
	exts      map[string]bool
	debounce  time.Duration
	watcher   *fsnotify.Watcher
	agent     *AgentProcess
	restartCh chan struct{}

	reloadSrv *reloadServer
	conn      net.Conn
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

	rs, err := newReloadServer()
	if err != nil {
		w.Close()
		return nil, err
	}

	// Append --reload-addr to CLI args so the Python process connects back
	config.CLIArgs = append(config.CLIArgs, "--reload-addr", rs.addr())

	return &agentWatcher{
		config:    config,
		exts:      watchExtensions(config.ProjectType),
		debounce:  500 * time.Millisecond,
		watcher:   w,
		restartCh: make(chan struct{}, 1),
		reloadSrv: rs,
	}, nil
}

func (aw *agentWatcher) start() error {
	agent, err := startAgent(aw.config)
	if err != nil {
		return err
	}
	aw.agent = agent

	// Accept connection from new Python process in background
	go func() {
		conn, err := aw.reloadSrv.listener.Accept()
		if err != nil {
			return
		}
		aw.conn = conn
		// Serve the initial restore request (will be empty on first start)
		go aw.reloadSrv.serveNewProcess(conn)
	}()

	return nil
}

func (aw *agentWatcher) restart() error {
	// 1. Capture active jobs from the current process (best-effort)
	if aw.conn != nil {
		aw.reloadSrv.captureJobs(aw.conn)
		aw.conn.Close()
		aw.conn = nil
	}

	// 2. Kill old process
	if aw.agent != nil {
		aw.agent.Kill()
	}

	fmt.Fprintln(os.Stderr, "Reloading agent...")

	// 3. Start new process
	agent, err := startAgent(aw.config)
	if err != nil {
		return err
	}
	aw.agent = agent

	// 4. Accept new connection and serve restored jobs
	go func() {
		conn, err := aw.reloadSrv.listener.Accept()
		if err != nil {
			return
		}
		aw.conn = conn
		go aw.reloadSrv.serveNewProcess(conn)
	}()

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
		if aw.conn != nil {
			aw.conn.Close()
		}
		aw.reloadSrv.close()
		aw.watcher.Close()
	}()

	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time

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
			if err := aw.restart(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to restart agent: %v\n", err)
				fmt.Fprintln(os.Stderr, "Waiting for file changes...")
			}

		case err, ok := <-aw.watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)

		case <-aw.agent.exitCh:
			// Agent crashed — wait for file changes to restart
			fmt.Fprintln(os.Stderr, "Agent exited. Waiting for file changes to restart...")
			// Drain any pending debounce
			if debounceTimer != nil {
				debounceTimer.Stop()
				debounceTimer = nil
				debounceCh = nil
			}
		}
	}
}
