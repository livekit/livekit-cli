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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/experiments"
	"github.com/go-task/task/v3/taskfile/ast"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	authutil "github.com/livekit/livekit-cli/v2/pkg/auth"
)

const (
	TaskFile                = "taskfile.yaml"
	TemplateIndexFile       = "templates.yaml"
	TemplateIndexURL        = "https://raw.githubusercontent.com/livekit-examples/index/main"
	TemplateBaseURL         = "https://github.com/livekit-examples"
	SandboxDashboardURL     = "https://cloud.livekit.io/projects/p_/sandbox"
	SandboxTemplateEndpoint = "/api/sandbox/template"
	SandboxCreateEndpoint   = "/api/sandbox/create"
)

type KnownTask string

const (
	TaskPostCreate KnownTask = "post_create"
	TaskInstall    KnownTask = "install"
	TaskDev        KnownTask = "dev"
)

// Files to remove after cloning a template
var templateIgnoreFiles = []string{
	".git",
	".task",
	"renovate.json",
	"taskfile.yaml",
	"TEMPLATE.md",
	"LICENSE",
	"LICENSE.md",
	"NOTICE",
}

type Template struct {
	Name      string            `yaml:"name" json:"name"`
	Desc      string            `yaml:"desc" json:"description,omitempty"`
	URL       string            `yaml:"url" json:"url,omitempty"`
	Docs      string            `yaml:"docs" json:"docs_url,omitempty"`
	Image     string            `yaml:"image" json:"image_ref,omitempty"`
	Tags      []string          `yaml:"tags" json:"tags,omitempty"`
	Attrs     map[string]string `yaml:"attrs" json:"attrs,omitempty"`
	Requires  []string          `yaml:"requires" json:"requires,omitempty"`
	IsSandbox bool              `yaml:"is_sandbox" json:"is_sandbox,omitempty"`
	IsHidden  bool              `yaml:"is_hidden" json:"is_hidden,omitempty"`
}

type SandboxDetails struct {
	Name           string     `json:"name"`
	Template       Template   `json:"template"`
	ChildTemplates []Template `json:"childTemplates"`
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

func CreateSandbox(ctx context.Context, agentName, templateURL, token, serverURL string) (string, error) {
	type createSandboxRequest struct {
		TemplateURL string `json:"template_url"`
		AgentName   string `json:"agent_name,omitempty"`
	}

	type createSandboxResponse struct {
		SandboxID string `json:"sandbox_id"`
	}

	body := createSandboxRequest{
		TemplateURL: templateURL,
		AgentName:   agentName,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		panic(err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", serverURL+SandboxCreateEndpoint, buf)
	req.Header = authutil.NewHeaderWithToken(token)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", errors.New(resp.Status)
	}

	var response createSandboxResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	return response.SandboxID, nil
}

func FetchSandboxDetails(ctx context.Context, sid, token, serverURL string) (*SandboxDetails, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", serverURL+SandboxTemplateEndpoint, nil)
	req.Header = authutil.NewHeaderWithToken(token)
	query := req.URL.Query()
	query.Add("id", sid)
	req.URL.RawQuery = query.Encode()
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("sandbox not found: %s", sid)
	}
	if resp.StatusCode != 200 {
		return nil, errors.New(resp.Status)
	}

	var details SandboxDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, err
	}
	return &details, nil
}

func ParseTaskfile(rootPath string) (*ast.Taskfile, error) {
	os.Setenv("TASK_X_REMOTE_TASKFILES", "1")
	experiments.Parse(rootPath)

	taskfilePath := path.Join(rootPath, TaskFile)

	// taskfile.yaml is optional
	if _, err := os.Stat(taskfilePath); err != nil && errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	file, err := os.Open(taskfilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	tf := &ast.Taskfile{}
	if err := yaml.NewDecoder(file).Decode(tf); err != nil {
		return nil, err
	}
	return tf, nil
}

func NewTaskExecutor(dir string, verbose bool) *task.Executor {
	var o io.Writer = os.Stdout
	var e io.Writer = os.Stderr
	return &task.Executor{
		Dir:       dir,
		Force:     false,
		ForceAll:  false,
		Insecure:  false,
		Download:  true,
		Offline:   false,
		Watch:     false,
		Verbose:   false,
		Silent:    true,
		Dry:       false,
		Summary:   false,
		Parallel:  false,
		Color:     true,
		Timeout:   time.Second * 10,
		AssumeYes: true,

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

	return NewTaskWithExecutor(ctx, exe, taskName, verbose)
}

func NewTaskWithExecutor(ctx context.Context, exe *task.Executor, taskName string, verbose bool) (func() error, error) {
	_, ok := exe.Taskfile.Tasks.Get(taskName)
	if !ok {
		return nil, fmt.Errorf("task %q not found", taskName)
	}

	call := &task.Call{
		Task:   taskName,
		Silent: !verbose,
	}

	return func() error {
		return exe.Run(ctx, call)
	}, nil
}

type PromptFunc func(key string, value string) (string, error)

// Read .env.example file if present in rootDir, replacing all `substitutions`,
// prompting for others, and returning the result as a map.
func InstantiateDotEnv(ctx context.Context, rootDir string, exampleFilePath string, substitutions map[string]string, verbose bool, prompt PromptFunc) (map[string]string, error) {
	promptedVars := map[string]string{}
	envExamplePath := path.Join(rootDir, exampleFilePath)

	stat, err := os.Stat(envExamplePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	} else if stat != nil {
		if stat.IsDir() {
			return nil, errors.New(".env.example file is a directory")
		}

		envMap, err := godotenv.Read(envExamplePath)
		if err != nil {
			return nil, err
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
					return nil, err
				}
				envMap[key] = newValue
				promptedVars[key] = newValue
			}
		}
		return envMap, nil
	} else {
		return substitutions, nil
	}
}

func PrintDotEnv(envMap map[string]string) error {
	envContents, err := godotenv.Marshal(envMap)
	if err != nil {
		return err
	}
	_, err = fmt.Println(envContents)
	return err
}

func WriteDotEnv(rootDir string, filePath string, envMap map[string]string) error {
	envContents, err := godotenv.Marshal(envMap)
	if err != nil {
		return err
	}
	envLocalPath := path.Join(rootDir, filePath)
	return os.WriteFile(envLocalPath, []byte(envContents+"\n"), 0700)
}

func CloneTemplate(url, dir string) (string, string, error) {
	var stdout = strings.Builder{}
	var stderr = strings.Builder{}

	cmd := exec.Command("git", "clone", "--depth=1", url, dir)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func CleanupTemplate(dir string) error {
	// Remove files that are only needed for template instantiation
	for _, cleanup := range templateIgnoreFiles {
		if err := os.RemoveAll(path.Join(dir, cleanup)); err != nil {
			return err
		}
	}
	return nil
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
