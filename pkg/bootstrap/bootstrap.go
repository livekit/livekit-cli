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
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/taskfile/ast"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const (
	EnvExampleFile    = ".env.example"
	EnvLocalFile      = ".env.local"
	TaskFile          = "taskfile.yaml"
	TemplateIndexFile = "templates.yaml"
	TemplateIndexURL  = "https://raw.githubusercontent.com/livekit-examples/index/main"
)

type KnownTask string

const (
	TaskInstall        = "install"
	TaskInstallSandbox = "install_sandbox"
	TaskDev            = "dev"
	TaskDevSandbox     = "dev_sandbox"
)

type Template struct {
	Name  string   `yaml:"name"`
	Desc  string   `yaml:"desc"`
	URL   string   `yaml:"url"`
	Docs  string   `yaml:"docs"`
	Image string   `yaml:"image"`
	Tags  []string `yaml:"tags"`
}

func FetchTemplates(ctx context.Context) ([]Template, error) {
	resp, err := http.Get(TemplateIndexURL + "/" + TemplateIndexFile)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var templates []Template
	if err := yaml.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return nil, err
	}
	return templates, nil
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

func NewTaskExecutor(dir string, verbose bool) *task.Executor {
	var o io.Writer = io.Discard
	var e io.Writer = os.Stderr
	if verbose {
		o = os.Stdout
	}
	return &task.Executor{
		Dir:       dir,
		Force:     false,
		ForceAll:  false,
		Insecure:  false,
		Download:  false,
		Offline:   false,
		Watch:     false,
		Verbose:   false,
		Silent:    !verbose,
		AssumeYes: false,
		Dry:       false,
		Summary:   false,
		Parallel:  false,
		Color:     true,

		Stdin:  os.Stdin,
		Stdout: o,
		Stderr: e,
	}
}

func NewTask(ctx context.Context, tf *ast.Taskfile, dir, taskName string, verbose bool) (func() error, error) {
	exe := NewTaskExecutor(dir, verbose)
	err := exe.Setup()
	if err != nil {
		return nil, err
	}

	return func() error {
		return exe.Run(ctx, &ast.Call{
			Task: taskName,
		})
	}, nil
}

type PromptFunc func(key string, value string) (string, error)

// Recursively walk the repo, reading in any .env.example file if present in
// that directory, replacing all `substitutions`, prompting for others, and
// writing to .env.local in that directory.
func InstantiateDotEnv(ctx context.Context, rootDir string, substitutions map[string]string, verbose bool, prompt PromptFunc) error {
	promptedVars := map[string]string{}

	return filepath.WalkDir(rootDir, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Name() == EnvExampleFile {
			envMap, err := godotenv.Read(filePath)
			if err != nil {
				return err
			}

			for key, oldValue := range envMap {
				// if key is a substitution, replace it
				if value, ok := substitutions[key]; ok {
					envMap[key] = value
					// if key was already promped, use that value
				} else if alreadyPromptedValue, ok := promptedVars[key]; ok {
					envMap[key] = alreadyPromptedValue
				} else {
					// prompt for value
					newValue, err := prompt(key, oldValue)
					if err != nil {
						return err
					}
					envMap[key] = newValue
					promptedVars[key] = newValue
				}
			}

			envContents, err := godotenv.Marshal(envMap)
			if err != nil {
				return err
			}

			envLocalPath := path.Join(path.Dir(filePath), EnvLocalFile)
			if err := os.WriteFile(envLocalPath, []byte(envContents), 0700); err != nil {
				return err
			}
		}

		return nil
	})
}

// Determine if `cmd` is a binary in PATH or a known alias
func CommandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return (err == nil || CommandIsAlias(cmd))
}

// Determine if `cmd` is a known alias
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
