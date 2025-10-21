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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

//go:embed examples/*
var embedfs embed.FS

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

func HasDockerIgnore(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == ".dockerignore" {
			return true, nil
		}
	}
	return false, nil
}

func CreateDockerIgnoreFile(dir string, projectType ProjectType) error {
	dockerIgnoreContent, err := embedfs.ReadFile(path.Join("examples", string(projectType)+".dockerignore"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"), dockerIgnoreContent, 0644); err != nil {
		return err
	}
	return nil
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

	// NOTE: embed.FS uses unix-style path separators on all platforms, so cannot use filepath.Join here.
	// path.Join always uses '/' as the separator.
	dockerfileContent, err := embedfs.ReadFile(path.Join("examples", string(projectType)+".Dockerfile"))
	if err != nil {
		return nil, nil, err
	}

	// NOTE: embed.FS uses unix-style path separators on all platforms, so cannot use filepath.Join here
	// path.Join always uses '/' as the separator.
	dockerIgnoreContent, err := embedfs.ReadFile(path.Join("examples", string(projectType)+".dockerignore"))
	if err != nil {
		return nil, nil, err
	}

	dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType)
	if err != nil {
		return nil, nil, err
	}

	return dockerfileContent, dockerIgnoreContent, nil
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType) ([]byte, error) {
	// Build matcher from the Dockerignore content so we don't consider ignored files
	reader := bytes.NewReader(dockerignoreContent)
	patterns, err := ignorefile.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("error walking directory %s: %w", dir, err)
	}

	// Prioritize common entrypoint filenames at the top of the list
	if len(fileList) > 1 {
		priority := func(p string) int {
			name := filepath.Base(p)
			switch name {
			case "__main__.py", "index.js":
				return 0
			case "main.py", "main.js":
				return 1
			case "agent.py", "agent.js":
				return 2
			default:
				return 3
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

	var newEntrypoint string
	if len(fileList) == 0 {
		newEntrypoint = projectType.DefaultEntrypoint()
	} else if len(fileList) == 1 {
		newEntrypoint = fileList[0]
	} else {
		selected := fileList[0]
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
			return nil, err
		}
		newEntrypoint = util.ToUnixPath(selected)
	}

	fmt.Printf("Using entrypoint file [%s]\n", util.Accented(newEntrypoint))

	tpl := template.Must(template.New("Dockerfile").Parse(string(dockerfileContent)))
	buf := &bytes.Buffer{}
	tpl.Execute(buf, map[string]string{
		"ProgramMain": newEntrypoint,
	})

	return buf.Bytes(), nil
}
