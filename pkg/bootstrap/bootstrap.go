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
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh/spinner"
	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/taskfile/ast"
	"github.com/mattn/go-shellwords"
	"gopkg.in/yaml.v3"
)

const (
	TaskFile       = "taskfile.yaml"
	LiveKitDir     = ".livekit"
	BootstrapFile  = "bootstrap.yaml"
	EnvExampleFile = ".env.example"
	EnvLocalFile   = ".env.local"
)

type Target string

const (
	TargetWeb     Target = "web"
	TargetPython  Target = "python"
	TargetGo      Target = "go"
	TargetIOS     Target = "ios"
	TargetAndroid Target = "android"
)

type BootstrapConfig struct {
	// The Target environment this component will run in.
	// Informs other configuration options and setup prompts.
	Target Target `yaml:"target,omitempty"`
	// TODO: aaa
	Env map[string]string `yaml:"env,omitempty"`
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
			// TODO: pipe the outputs to a scrolling onscreen log a la `tail -f`
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
		// TODO: should this be parallelized?
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

func BootstrapPath() string {
	return path.Join(LiveKitDir, BootstrapFile)
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

func ParseTaskfile(rootPath string) (*ast.Taskfile, error) {
	file, err := os.ReadFile(path.Join(rootPath, TaskFile))
	if err != nil {
		return nil, err
	}
	tf := &ast.Taskfile{}
	if err := yaml.Unmarshal(file, tf); err != nil {
		return nil, err
	}
	return tf, nil
}

func NewTaskExecutor(tf *ast.Taskfile, dir string, verbose bool) *task.Executor {
	var o io.Writer = io.Discard
	var e io.Writer = os.Stderr
	if verbose {
		o = os.Stdout
	}
	return &task.Executor{
		Taskfile:  tf,
		Dir:       dir,
		Force:     false,
		ForceAll:  false,
		Insecure:  false,
		Download:  false,
		Offline:   false,
		Watch:     false,
		Verbose:   true,
		Silent:    !verbose,
		AssumeYes: true,
		Dry:       false,
		Summary:   false,
		Parallel:  false,
		Color:     true,

		Stdin:  os.Stdin,
		Stdout: o,
		Stderr: e,
	}
}

func CreateInstallTask(ctx context.Context, tf *ast.Taskfile, dir string, verbose bool) (func() error, error) {
	exe := NewTaskExecutor(tf, dir, verbose)
	err := exe.Setup()
	if err != nil {
		return nil, err
	}

	return func() error {
		return exe.Run(ctx, &ast.Call{
			Task: "install",
		})

	}, nil
}

func ExecuteInstallTask(ctx context.Context, tf *ast.Taskfile, dir string, verbose bool) error {
	install, err := CreateInstallTask(ctx, tf, dir, verbose)
	if err != nil {
		return err
	}
	return install()
}

func ExecuteDevTask(ctx context.Context, tf *ast.Taskfile, dir string, verbose bool) error {
	exe := NewTaskExecutor(tf, dir, verbose)
	err := exe.Setup()
	if err != nil {
		return err
	}

	if verbose {
		return exe.Run(ctx, &ast.Call{
			Task: "dev",
		})
	} else {
		var cmdErr error
		if err := spinner.New().
			Title("Running...").
			Action(func() {
				cmdErr = exe.Run(ctx, &ast.Call{
					Task: "dev",
				})
			}).
			Run(); err != nil {
			return err
		}
		return cmdErr
	}
}
