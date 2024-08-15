// Copyright 2024 LiveKit, Inc.
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

// This package defines the configuration file associated with LiveKit
// templates, which include instructions for package setup and development in
// both Playground mode and local dev.
package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh/spinner"
	"github.com/mattn/go-shellwords"
	"gopkg.in/yaml.v3"
)

const (
	BootstrapFile  = ".bootstrap.yaml"
	EnvExampleFile = ".env.example"
	EnvLocalFile   = ".env.local"
)

type Target string

const (
	WebTarget     = "web"
	PythonTarget  = "python"
	GoTarget      = "go"
	IOSTarget     = "ios"
	AndroidTarget = "android"
)

type BootstrapConfig struct {
	// The Target environment this component will run in.
	// Informs other configuration options and setup prompts.
	Target Target `yaml:"target,omitempty"`
	// These executables must be present on $PATH
	Requires []string `yaml:"requires,omitempty"`
	// These commands will be run once during setup
	Install []string `yaml:"install,omitempty"`
	// These commands will be run once during setup (Windows-specific)
	// If absent, falls back to `Install`
	InstallWin []string `yaml:"install_win,omitempty"`
	// These commands will be run during local development
	Dev []string `yaml:"dev,omitempty"`
	// These commands will be run during local development (Windows-specific)
	// If absent, falls back to `Install`
	DevWin []string `yaml:"dev_win,omitempty"`
	// This map includes subcomponents to be run recursively, with
	// keys representig directories to `cd` into before running.
	Components map[string]BootstrapConfig `yaml:"components,omitempty"`
}

var (
	DefaultWebBootstrapComponent = &BootstrapConfig{
		Target:   WebTarget,
		Requires: []string{"pnpm"},
		Install:  []string{"pnpm install"},
		Dev:      []string{"pnpm dev"},
	}
	DefaultPythonBootstrapComponent = &BootstrapConfig{
		Target:   PythonTarget,
		Requires: []string{"python3", "pip3"},
		Install: []string{
			"python3 -m venv .venv",
			"bash -c \"source .venv/bin/activate\"",
			"pip3 install -r requirements.txt",
		},
		InstallWin: []string{
			"python3 -m venv .venv",
			"powershell .\\.venv\\bin\\Activate.ps1",
			"pip3 install -r requirements.txt",
		},
		Dev: []string{"python3 agent.py start"},
	}
	DefaultNextAgentsBootstrapComponent = &BootstrapConfig{
		Components: map[string]BootstrapConfig{
			"client": *DefaultWebBootstrapComponent,
			"server": *DefaultPythonBootstrapComponent,
		},
	}
)

// Assert that all elements of `Requires` are present in the PATH.
// Does not recurse through child components.
func (b *BootstrapConfig) CheckRequirements() error {
	for _, reqStr := range b.Requires {
		if !CommandExists(reqStr) {
			return errors.New("could not locate `" + reqStr + "` in path")
		}
	}
	return nil
}

// Recursively execute a BootstrapConfig's `Install` instuctions.
func (b *BootstrapConfig) ExecuteInstall(ctx context.Context, componentName, componentDir string, verbose bool) error {
	//  1. Assert that all elements of `Requires` are present in the PATH.
	if err := b.CheckRequirements(); err != nil {
		return err
	}

	parser := shellwords.NewParser()

	installCommands := b.Install
	if runtime.GOOS == "windows" && len(b.InstallWin) > 0 {
		installCommands = b.InstallWin
	}

	//  2. Execute each element of `Install` in series, capturing stdout and stderr and
	//     printing them if verbose is specified.
	for _, cmdStr := range installCommands {
		parts, err := parser.Parse(cmdStr)
		if err != nil {
			return err
		}

		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		cmd.Dir = componentDir

		if verbose {
			// TODO: prefix each out/err statement with the command name, and pipe
			// the outputs to some static onscreen log
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		var cmdErr error
		if err := spinner.New().
			Title(componentName + ": " + cmdStr).
			Action(func() {
				cmdErr = cmd.Run()
			}).
			Run(); err != nil {
			return err
		}
		if cmdErr != nil {
			return cmdErr
		}
	}

	//  3. For each of `Components`, spawn a goroutine that executes this procedure in
	//     the child context, reporting any errors on dedicated chan.
	for childName, component := range b.Components {
		childDir := path.Join(componentDir, childName)
		childComponent := componentName + "/" + childName
		err := component.ExecuteInstall(ctx, childComponent, childDir, verbose)
		if err != nil {
			return err
		}
	}

	//  4. Report any errors on dedicated chan.
	return nil
}

// Recursively walk the BootstrapConfig's `Components`, reading in any .env.example file if present
// in that directory, replacing all `substitutions`, and writing to .env.local in that directory.
func (b *BootstrapConfig) WriteDotEnv(ctx context.Context, dirName string, substitutions map[string]string, verbose bool) error {
	envPath := path.Join(dirName, EnvLocalFile)
	examplePath := path.Join(dirName, EnvExampleFile)

	if stat, _ := os.Stat(examplePath); stat != nil {
		envData, err := os.ReadFile(examplePath)
		if err != nil {
			return err
		}

		envContents := string(envData)
		for key, value := range substitutions {
			envContents = strings.ReplaceAll(envContents, key, value)
		}

		if err := os.WriteFile(envPath, []byte(envContents), 0700); err != nil {
			return err
		}
	}

	for childName, childComponent := range b.Components {
		childDir := path.Join(dirName, childName)
		if err := childComponent.WriteDotEnv(ctx, childDir, substitutions, verbose); err != nil {
			return err
		}
	}

	return nil
}

// `cmd` is a binary in PATH or a known alias
func CommandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return (err == nil || CommandIsAlias(cmd))
}

// `cmd` is a known alias
func CommandIsAlias(cmd string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	out, err := exec.Command("alias", cmd).Output()
	if err != nil {
		return false
	}
	output := strings.TrimSpace(string(out))
	return strings.HasPrefix(output, cmd+"=")
}

// Attempt to parse a BootstrapConfig yaml file at `path`
func ParseBootstrapConfig(path string) (*BootstrapConfig, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &BootstrapConfig{}
	if err := yaml.Unmarshal(file, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
