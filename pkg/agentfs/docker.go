// Copyright 2025 LiveKit, Inc.
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

package agentfs

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

//go:embed examples/*
var fs embed.FS

func HasDockerfile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.Name() == "Dockerfile" {
			return true, nil
		}
	}
	return false, nil
}

func CreateDockerfile(dir string, projectType ProjectType, settingsMap map[string]string) error {
	dockerfileContent, dockerIgnoreContent, err := GenerateDockerArtifacts(dir, projectType, settingsMap)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfileContent, 0644); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"), dockerIgnoreContent, 0644); err != nil {
		return err
	}

	return nil
}

// GenerateDockerArtifacts returns the Dockerfile and .dockerignore contents for the
// provided project type without writing them to disk. The Dockerfile content may be
// templated/validated (e.g., Python entrypoint).
func GenerateDockerArtifacts(dir string, projectType ProjectType, settingsMap map[string]string) ([]byte, []byte, error) {
	if len(settingsMap) == 0 {
		return nil, nil, fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	dockerfileContent, err := fs.ReadFile("examples/" + string(projectType) + ".Dockerfile")
	if err != nil {
		return nil, nil, err
	}

	dockerIgnoreContent, err := fs.ReadFile("examples/" + string(projectType) + ".dockerignore")
	if err != nil {
		return nil, nil, err
	}

	// TODO: (@rektdeckard) support Node entrypoint validation
	if projectType.IsPython() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap)
		if err != nil {
			return nil, nil, err
		}
	}

	return dockerfileContent, dockerIgnoreContent, nil
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType, settingsMap map[string]string) ([]byte, error) {
	valFile := func(fileName string) (string, error) {
		// NOTE: we need to recurse to find entrypoints which may exist in src/ or some other directory.
		// This could be a lot of files, so we omit any files in .dockerignore, since they cannot be
		// used as entrypoints.

		reader := bytes.NewReader(dockerignoreContent)
		patterns, err := ignorefile.ReadAll(reader)
		if err != nil {
			return "", err
		}
		matcher, err := patternmatcher.New(patterns)
		if err != nil {
			return "", err
		}

		var fileList []string
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ignored, err := matcher.MatchesOrParentMatches(path); ignored {
				return nil
			} else if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), projectType.FileExt()) {
				// Exclude double-underscore files (e.g., __init__.py) which cannot be entrypoint
				// except for __main__.py, which is the default entrypoint for Python.
				if strings.HasPrefix(d.Name(), "__") && d.Name() != "__main__.py" {
					return nil
				}
				fileList = append(fileList, path)
			}
			return nil
		}); err != nil {
			return "", fmt.Errorf("error walking directory %s: %w", dir, err)
		}
		if slices.Contains(fileList, fileName) {
			return fileName, nil
		}

		// If no matching files found, return early
		if len(fileList) == 0 {
			return "", nil
		}

		// Prioritize common entrypoint filenames at the top of the list
		if len(fileList) > 1 {
			priority := func(p string) int {
				name := filepath.Base(p)
				switch name {
				case "main.py":
					return 0
				case "agent.py":
					return 1
				default:
					return 2
				}
			}
			sort.SliceStable(fileList, func(i, j int) bool {
				pi := priority(fileList[i])
				pj := priority(fileList[j])
				if pi != pj {
					return pi < pj
				}
				return fileList[i] < fileList[j]
			})
		}

		// If there's only one candidate, select it automatically
		if len(fileList) == 1 {
			return fileList[0], nil
		}

		var selected string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Select the %s file which contains your agent's entrypoint", projectType.Lang())).
					Options(huh.NewOptions(fileList...)...).
					Value(&selected).
					WithTheme(util.Theme),
			),
		)

		if err := form.Run(); err != nil {
			return "", err
		}
		return selected, nil
	}

	if err := validateSettingsMap(settingsMap, []string{"python_entrypoint"}); err != nil {
		return nil, err
	}

	pythonEntrypoint := settingsMap["python_entrypoint"]
	newEntrypoint, err := valFile(pythonEntrypoint)
	if err != nil {
		return nil, err
	}

	if newEntrypoint == "" {
		newEntrypoint = pythonEntrypoint
	}
	fmt.Printf("Using entrypoint file [%s]\n", util.Accented(newEntrypoint))

	tpl := template.Must(template.New("Dockerfile").Parse(string(dockerfileContent)))
	buf := &bytes.Buffer{}
	tpl.Execute(buf, map[string]string{
		"ProgramMain": newEntrypoint,
	})

	return buf.Bytes(), nil
}
