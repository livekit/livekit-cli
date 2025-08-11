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
	if len(settingsMap) == 0 {
		return fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	var dockerfileContent []byte
	var dockerIgnoreContent []byte
	var err error

	dockerfileContent, err = fs.ReadFile("examples/" + string(projectType) + ".Dockerfile")
	if err != nil {
		return err
	}

	dockerIgnoreContent, err = fs.ReadFile("examples/" + string(projectType) + ".dockerignore")
	if err != nil {
		return err
	}

	// TODO: (@rektdeckard) support Node entrypoint validation
	if projectType.IsPython() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap)
		if err != nil {
			return err
		}
	}

	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfileContent, 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(dir, ".dockerignore"), dockerIgnoreContent, 0644)
	if err != nil {
		return err
	}

	return nil
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType, settingsMap map[string]string) ([]byte, error) {
	valFile := func(fileName string) (string, error) {
		// Parse dockerignore patterns to filter out files that won't be in build context
		reader := bytes.NewReader(dockerignoreContent)
		patterns, err := ignorefile.ReadAll(reader)
		if err != nil {
			return "", fmt.Errorf("failed to parse .dockerignore: %w", err)
		}
		matcher, err := patternmatcher.New(patterns)
		if err != nil {
			return "", fmt.Errorf("failed to create pattern matcher: %w", err)
		}

		// Recursively find all relevant files, respecting dockerignore
		fileMap := make(map[string]bool)
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Skip files that match .dockerignore patterns
			if ignored, err := matcher.MatchesOrParentMatches(path); ignored {
				return nil
			} else if err != nil {
				return err
			}

			// Only include files with the correct extension
			if !d.IsDir() && strings.HasSuffix(d.Name(), projectType.FileExt()) {
				// Convert to relative path from directory
				relPath, err := filepath.Rel(dir, path)
				if err != nil {
					return err
				}
				fileMap[relPath] = true
			}
			return nil
		}); err != nil {
			return "", fmt.Errorf("error walking directory %s: %w", dir, err)
		}

		// Check if the specified file exists
		if _, exists := fileMap[fileName]; exists {
			return fileName, nil
		}

		// Smart entry point discovery with prioritization
		var candidates []string
		var priorityOrder []string

		if projectType.IsPython() {
			// Common Python entry point patterns in order of preference
			priorityOrder = []string{
				"main.py",         // Most common
				"src/main.py",     // Modern src layout
				"agent.py",        // LiveKit agents
				"src/agent.py",    // LiveKit agents in src
				"app.py",          // Flask/web apps
				"src/app.py",      // Flask/web apps in src
				"__main__.py",     // Python module entry
				"src/__main__.py", // Python module entry in src
			}
		} else if projectType == ProjectTypeNode {
			// Common Node.js entry point patterns
			priorityOrder = []string{
				"index.js",
				"main.js",
				"app.js",
				"src/index.js",
				"src/main.js",
				"src/app.js",
				"agent.js",
				"src/agent.js",
			}
		}

		// First, check priority patterns that exist
		for _, pattern := range priorityOrder {
			if _, exists := fileMap[pattern]; exists {
				candidates = append(candidates, pattern)
			}
		}

		// Then add any other matching files not already in candidates
		for fileName := range fileMap {
			if !slices.Contains(candidates, fileName) {
				candidates = append(candidates, fileName)
			}
		}

		// If we have a single clear choice, use it
		if len(candidates) == 1 {
			return candidates[0], nil
		}

		// If no matching files found, return early
		if len(candidates) == 0 {
			return "", nil
		}

		// Create enhanced options with descriptions
		var selectOptions []huh.Option[string]
		for _, option := range candidates {
			var description string
			switch {
			case strings.Contains(option, "agent."):
				description = fmt.Sprintf("%s (LiveKit agent)", option)
			case strings.Contains(option, "src/"):
				description = fmt.Sprintf("%s (common src/ layout)", option)
			case strings.Contains(option, "main."):
				description = fmt.Sprintf("%s (common main entry point)", option)
			case strings.Contains(option, "app."):
				description = fmt.Sprintf("%s (app entry point)", option)
			case strings.Contains(option, "__main__.py"):
				description = fmt.Sprintf("%s (Python module entry)", option)
			default:
				description = option
			}

			selectOptions = append(selectOptions, huh.Option[string]{
				Key:   description,
				Value: option,
			})
		}

		// Set the first (highest priority) option as default
		var selected string
		if len(candidates) > 0 {
			selected = candidates[0]
		}

		title := fmt.Sprintf("Multiple %s files found. Select entrypoint:", projectType.Lang())
		if len(candidates) > 5 {
			title = fmt.Sprintf("Found %d %s files. Select entrypoint:", len(candidates), projectType.Lang())
		}

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title).
					Description("The selected file will be used as the main entry point for your agent").
					Options(selectOptions...).
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

	tpl := template.Must(template.New("Dockerfile").Parse(string(dockerfileContent)))
	buf := &bytes.Buffer{}
	tpl.Execute(buf, map[string]string{
		"ProgramMain": newEntrypoint,
	})

	return buf.Bytes(), nil
}
