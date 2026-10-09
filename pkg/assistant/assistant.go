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

// Package assistant holds the voice agent behind `lk assistant`. The agent's
// Python project is embedded in the CLI and installed into a cache directory
// the first time it runs.
package assistant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed agent/agent.py agent/pyproject.toml
var agentFiles embed.FS

// Entrypoint is the agent's entrypoint file, relative to its project directory.
const Entrypoint = "agent.py"

// Names of agent tools that lk acts on.
const (
	// ShowCodeTool shows code as a card in the overlay.
	ShowCodeTool = "show_code"
	// HandOffTaskTool shows a prompt for the user's coding agent as a card.
	HandOffTaskTool = "hand_off_task"
	// EndCallTool ends the conversation. lk closes the overlay after it.
	EndCallTool = "end_call"
)

// ErrNoUV means uv, which runs the agent, isn't installed.
var ErrNoUV = errors.New("lk assistant runs its agent with uv. Install uv (https://docs.astral.sh/uv/) and try again")

// stampFile records the hash of the agent files that were last installed.
const stampFile = ".lk-installed"

// DefaultDir is where the agent project is installed.
func DefaultDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "livekit", "lk-assistant-agent"), nil
}

// Prepare writes the agent project to dir and installs its dependencies with
// uv. Dependencies are installed again only when the agent files change.
// status is called with a short description before slow steps.
func Prepare(ctx context.Context, dir string, status func(string)) error {
	uv, err := exec.LookPath("uv")
	if err != nil {
		return ErrNoUV
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	sum := sha256.New()
	err = fs.WalkDir(agentFiles, "agent", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := agentFiles.ReadFile(path)
		if err != nil {
			return err
		}
		sum.Write([]byte(path))
		sum.Write(data)
		dst := filepath.Join(dir, filepath.Base(path))
		if existing, err := os.ReadFile(dst); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		return fmt.Errorf("writing the assistant agent: %w", err)
	}
	hash := hex.EncodeToString(sum.Sum(nil))

	stamp := filepath.Join(dir, stampFile)
	if installed, err := os.ReadFile(stamp); err == nil && string(installed) == hash {
		if _, err := os.Stat(filepath.Join(dir, ".venv")); err == nil {
			return nil
		}
	}

	status("Installing the assistant agent")
	cmd := exec.CommandContext(ctx, uv, "sync")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installing the assistant agent failed: %w\n%s", err, out)
	}
	return os.WriteFile(stamp, []byte(hash), 0o644)
}
