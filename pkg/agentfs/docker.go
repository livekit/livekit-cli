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

func CreateDockerfile(dir string, settingsMap map[string]string, silent bool) error {
	if len(settingsMap) == 0 {
		return fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	projectType, err := DetectProjectType(dir)
	if err != nil {
		return fmt.Errorf(`× Unable to determine project type

Supported project types:
  • Python: requires requirements.txt or pyproject.toml
  • Node.js: requires package.json

Please ensure your project has the appropriate dependency file, or create a Dockerfile manually in the current directory`)
	}

	if !silent {
		fmt.Printf("Creating Dockerfiles...\n")

		// Provide user feedback about detected project type
		switch projectType {
		case ProjectTypeNodeNPM:
			fmt.Printf("✔ Detected Node.js project with npm package manager\n")
			fmt.Printf("  Using template [%s] with npm support\n", util.Accented("node.npm"))
		case ProjectTypePythonUV:
			fmt.Printf("✔ Detected Python project with UV package manager\n")
			fmt.Printf("  Using template [%s] for faster builds\n", util.Accented("python.uv"))
		case ProjectTypePythonPoetry:
			fmt.Printf("✔ Detected Python project with Poetry package manager\n")
			fmt.Printf("  Using template [%s] with dependency groups support\n", util.Accented("python.poetry"))
		case ProjectTypePythonHatch:
			fmt.Printf("✔ Detected Python project with Hatch package manager\n")
			fmt.Printf("  Using template [%s] with isolated environments\n", util.Accented("python.hatch"))
		case ProjectTypePythonPDM:
			fmt.Printf("✔ Detected Python project with PDM package manager\n")
			fmt.Printf("  Using template [%s] with lock file support\n", util.Accented("python.pdm"))
		case ProjectTypePythonPipenv:
			fmt.Printf("✔ Detected Python project with Pipenv package manager\n")
			fmt.Printf("  Using template [%s] with virtual environment isolation\n", util.Accented("python.pipenv"))
		case ProjectTypePythonPip:
			fmt.Printf("✔ Detected Python project with pip package manager\n")
			fmt.Printf("  Using template [%s]\n", util.Accented("python.pip"))
		}
	}

	if projectType == ProjectTypePythonUV {
		// Validate UV project setup
		validateUVProject(dir, silent)
	} else if projectType == ProjectTypePythonPoetry {
		// Validate Poetry project setup
		validatePoetryProject(dir, silent)
	} else if projectType == ProjectTypePythonHatch {
		// Validate Hatch project setup
		validateHatchProject(dir, silent)
	} else if projectType == ProjectTypePythonPDM {
		// Validate PDM project setup
		validatePDMProject(dir, silent)
	} else if projectType == ProjectTypePythonPipenv {
		// Validate Pipenv project setup
		validatePipenvProject(dir, silent)
	} else if projectType == ProjectTypeNodeNPM {
		// Validate npm project setup
		validateNPMProject(dir, silent)
	}

	var dockerfileContent []byte
	var dockerIgnoreContent []byte

	dockerfileContent, err = fs.ReadFile("examples/" + string(projectType) + ".Dockerfile")
	if err != nil {
		return fmt.Errorf("failed to load Dockerfile template '%s': %w", string(projectType), err)
	}

	dockerIgnoreContent, err = fs.ReadFile("examples/" + string(projectType) + ".dockerignore")
	if err != nil {
		return fmt.Errorf("failed to load .dockerignore template for '%s': %w", string(projectType), err)
	}

	// Validate entrypoint for both Python and Node.js projects
	if projectType.IsPython() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap, silent)
		if err != nil {
			return fmt.Errorf("failed to validate Python entry point: %w", err)
		}
	} else if projectType.IsNode() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap, silent)
		if err != nil {
			return fmt.Errorf("failed to validate Node.js entry point: %w", err)
		}
	}

	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfileContent, 0644)
	if err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	err = os.WriteFile(filepath.Join(dir, ".dockerignore"), dockerIgnoreContent, 0644)
	if err != nil {
		return fmt.Errorf("failed to write .dockerignore: %w", err)
	}

	if !silent {
		fmt.Printf("\n✔ Successfully generated Docker files:\n")
		fmt.Printf("  %s - Container build instructions\n", util.Accented("Dockerfile"))
		fmt.Printf("  %s - Files excluded from build context\n", util.Accented(".dockerignore"))
		fmt.Printf("\nNext steps:\n")
		fmt.Printf("  ► Review the %s and uncomment/update any needed packages\n", util.Accented("Dockerfile"))
		fmt.Printf("  ► Build your agent: docker build -t my-agent .\n")
	}

	return nil
}

func validateUVProject(dir string, silent bool) {
	uvLockPath := filepath.Join(dir, "uv.lock")
	if _, err := os.Stat(uvLockPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: UV project detected but %s file not found\n", util.Accented("uv.lock"))
			fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("uv lock"), util.Accented("uv.lock"))
			fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
		}
	}
}

func validatePoetryProject(dir string, silent bool) {
	poetryLockPath := filepath.Join(dir, "poetry.lock")
	if _, err := os.Stat(poetryLockPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: Poetry project detected but %s file not found\n", util.Accented("poetry.lock"))
			fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("poetry lock"), util.Accented("poetry.lock"))
			fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
		}
	}
}

func validateHatchProject(dir string, silent bool) {
	// Hatch doesn't use lock files by default, but we should check for pyproject.toml
	pyprojectPath := filepath.Join(dir, "pyproject.toml")
	if _, err := os.Stat(pyprojectPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: Hatch project detected but %s file not found\n", util.Accented("pyproject.toml"))
			fmt.Printf("  Hatch requires a valid %s with project metadata\n", util.Accented("pyproject.toml"))
			fmt.Printf("  Consider running %s to create a proper project structure\n\n", util.Accented("hatch new"))
		}
	}
}

func validatePDMProject(dir string, silent bool) {
	pdmLockPath := filepath.Join(dir, "pdm.lock")
	if _, err := os.Stat(pdmLockPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: PDM project detected but %s file not found\n", util.Accented("pdm.lock"))
			fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("pdm lock"), util.Accented("pdm.lock"))
			fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
		}
	}
}

func validatePipenvProject(dir string, silent bool) {
	pipfileLockPath := filepath.Join(dir, "Pipfile.lock")
	if _, err := os.Stat(pipfileLockPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: Pipenv project detected but %s file not found\n", util.Accented("Pipfile.lock"))
			fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("pipenv lock"), util.Accented("Pipfile.lock"))
			fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
		}
	}
}

func validateNPMProject(dir string, silent bool) {
	packageLockPath := filepath.Join(dir, "package-lock.json")
	if _, err := os.Stat(packageLockPath); err != nil {
		if !silent {
			fmt.Printf("! Warning: npm project detected but %s file not found\n", util.Accented("package-lock.json"))
			fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("npm install"), util.Accented("package-lock.json"))
			fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
		}
	}
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType, settingsMap map[string]string, silent bool) ([]byte, error) {
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
				"agent.py",        // LiveKit agents
				"src/agent.py",    // LiveKit agents in src
				"main.py",         // Most common
				"src/main.py",     // Modern src layout
				"app.py",          // Flask/web apps
				"src/app.py",      // Flask/web apps in src
				"__main__.py",     // Python module entry
				"src/__main__.py", // Python module entry in src
			}
		} else if projectType.IsNode() {
			// Common Node.js entry point patterns
			priorityOrder = []string{
				"dist/agent.js", // Built TypeScript output
				"dist/index.js", // Built TypeScript output
				"dist/main.js",  // Built TypeScript output
				"dist/app.js",   // Built TypeScript output
				"agent.js",
				"index.js",
				"main.js",
				"app.js",
				"src/agent.js",
				"src/index.js",
				"src/main.js",
				"src/app.js",
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

		// If silent mode, automatically use the top choice
		if silent {
			return selected, nil
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

	// Determine which entrypoint key to use based on project type
	var entrypointKey string
	var defaultEntrypoint string

	if projectType.IsPython() {
		entrypointKey = "python_entrypoint"
		defaultEntrypoint = settingsMap[entrypointKey]
		if defaultEntrypoint == "" {
			defaultEntrypoint = "main.py"
		}
	} else if projectType.IsNode() {
		entrypointKey = "node_entrypoint"
		defaultEntrypoint = settingsMap[entrypointKey]
		if defaultEntrypoint == "" {
			defaultEntrypoint = "dist/agent.js"
		}
	}

	newEntrypoint, err := valFile(defaultEntrypoint)
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
