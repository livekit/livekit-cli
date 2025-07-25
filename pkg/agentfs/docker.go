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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/logger"
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

func CreateDockerfile(dir string, settingsMap map[string]string) error {
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

	// Provide user feedback about detected project type
	switch projectType {
	case ProjectTypeNode:
		fmt.Printf("✔ Detected Node.js project (found %s)\n", util.Accented("package.json"))
		fmt.Printf("  Using template [%s] with npm/yarn support\n", util.Accented("node"))
	case ProjectTypePythonUV:
		fmt.Printf("✔ Detected Python project with UV package manager\n")
		fmt.Printf("  Using template [%s] for faster builds\n", util.Accented("python.uv"))
		// Validate UV project setup
		validateUVProject(dir)
	case ProjectTypePythonPip:
		fmt.Printf("✔ Detected Python project with pip package manager\n")
		fmt.Printf("  Using template [%s]\n", util.Accented("python.pip"))
	}

	var dockerfileContent []byte
	var dockerIgnoreContent []byte

	dockerfileContent, err = fs.ReadFile("examples/" + string(projectType) + ".Dockerfile")
	if err != nil {
		return fmt.Errorf("failed to load Dockerfile template '%s': %w", string(projectType), err)
	}

	// Load the appropriate dockerignore template for each project type
	dockerIgnoreContent, err = fs.ReadFile("examples/" + string(projectType) + ".dockerignore")
	if err != nil {
		return fmt.Errorf("failed to load .dockerignore template for '%s': %w", string(projectType), err)
	}

	if projectType.IsPython() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap)
		if err != nil {
			return fmt.Errorf("failed to validate Python entry point: %w", err)
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

	fmt.Printf("\n✔ Successfully generated Docker files:\n")
	fmt.Printf("  %s - Container build instructions\n", util.Accented("Dockerfile"))
	fmt.Printf("  %s - Files excluded from build context\n", util.Accented(".dockerignore"))
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  ► Review the %s and uncomment any needed system packages\n", util.Accented("Dockerfile"))
	fmt.Printf("  ► Build your agent: docker build -t my-agent .\n")
	fmt.Printf("  ► Test locally: docker run my-agent\n")

	return nil
}

func validateUVProject(dir string) {
	uvLockPath := filepath.Join(dir, "uv.lock")
	if _, err := os.Stat(uvLockPath); err != nil {
		fmt.Printf("! Warning: UV project detected but %s file not found\n", util.Accented("uv.lock"))
		fmt.Printf("  Consider running %s to generate %s for reproducible builds\n", util.Accented("uv lock"), util.Accented("uv.lock"))
		fmt.Printf("  This ensures consistent dependency versions across environments\n\n")
	}
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType, settingsMap map[string]string) ([]byte, error) {
	// Parse dockerignore patterns to filter out files that won't be in build context
	reader := bytes.NewReader(dockerignoreContent)
	patterns, err := ignorefile.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse .dockerignore: %w", err)
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, fmt.Errorf("failed to create pattern matcher: %w", err)
	}

	// Use recursive traversal to find all relevant files, respecting dockerignore
	var allFiles []string
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
			allFiles = append(allFiles, relPath)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("error walking directory %s: %w", dir, err)
	}

	// Convert to map for our existing prioritization logic compatibility
	fileList := make(map[string]bool)
	for _, file := range allFiles {
		fileList[file] = true
	}

	valFile := func(fileName string) (string, error) {
		if _, exists := fileList[fileName]; exists {
			return fileName, nil
		}

		// Determine project type from ProjectType enum
		var suffix string
		if projectType.IsPython() {
			suffix = ".py"
		} else if projectType == ProjectTypeNode {
			suffix = ".js"
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
			priorityOrder = []string{
				"index.js",
				"main.js",
				"app.js",
				"src/index.js",
				"src/main.js",
				"src/app.js",
			}
		}

		// First, check priority patterns that exist
		for _, pattern := range priorityOrder {
			if _, exists := fileList[pattern]; exists {
				candidates = append(candidates, pattern)
			}
		}

		// Then add any other matching files not already in candidates
		for fileName := range fileList {
			if strings.HasSuffix(fileName, suffix) {
				// Skip if already in candidates
				if !slices.Contains(candidates, fileName) {
					candidates = append(candidates, fileName)
				}
			}
		}

		// If we have a clear top choice, suggest it
		if len(candidates) == 1 {
			return candidates[0], nil
		}

		options := candidates

		// If no matching files found, return early
		if len(options) == 0 {
			return "", nil
		}

		// Create enhanced options with descriptions
		var selectOptions []huh.Option[string]
		for _, option := range options {
			var description string
			switch {
			case strings.Contains(option, "src/"):
				description = fmt.Sprintf("%s (modern src/ layout)", option)
			case strings.Contains(option, "main."):
				description = fmt.Sprintf("%s (common main entry point)", option)
			case strings.Contains(option, "agent."):
				description = fmt.Sprintf("%s (LiveKit agent)", option)
			case strings.Contains(option, "app."):
				description = fmt.Sprintf("%s (application entry point)", option)
			case strings.Contains(option, "__main__.py"):
				description = fmt.Sprintf("%s (Python module entry)", option)
			default:
				description = option
			}

			// For Python module paths, suggest both file and module syntax
			if projectType.IsPython() && strings.HasPrefix(option, "src/") && strings.HasSuffix(option, ".py") {
				modulePath := strings.TrimPrefix(option, "src/")
				modulePath = strings.TrimSuffix(modulePath, ".py")
				modulePath = strings.ReplaceAll(modulePath, "/", ".")
				if modulePath != "" {
					description += fmt.Sprintf(" [can also use: src.%s]", modulePath)
				}
			}

			selectOptions = append(selectOptions, huh.Option[string]{
				Key:   description,
				Value: option,
			})
		}

		// Set the first (highest priority) option as default
		var selected string
		if len(options) > 0 {
			selected = options[0]
		}
		title := fmt.Sprintf("Multiple %s files found. Select entrypoint:", projectType.Lang())
		if len(options) > 3 {
			title = fmt.Sprintf("Found %d %s files. Select entrypoint:", len(options), projectType.Lang())
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

		err := form.Run()
		if err != nil {
			return "", err
		}

		return selected, nil
	}

	err = validateSettingsMap(settingsMap, []string{"python_entrypoint"})
	if err != nil {
		return nil, err
	}

	pythonEntrypoint := settingsMap["python_entrypoint"]
	newEntrypoint, err := valFile(pythonEntrypoint)
	if err != nil {
		return nil, err
	}

	lines := bytes.Split(dockerfileContent, []byte("\n"))
	var result bytes.Buffer
	for i := range len(lines) {
		line := lines[i]
		trimmedLine := bytes.TrimSpace(line)

		if bytes.HasPrefix(trimmedLine, []byte("ARG PROGRAM_MAIN")) {
			// Replace ARG PROGRAM_MAIN with the selected entry point
			fmt.Fprintf(&result, "ARG PROGRAM_MAIN=\"%s\"\n", newEntrypoint)
		} else if bytes.HasPrefix(trimmedLine, []byte("ENTRYPOINT")) {
			// Extract the current entrypoint file
			parts := bytes.Fields(trimmedLine)
			if len(parts) < 2 {
				return nil, fmt.Errorf("invalid ENTRYPOINT format")
			}

			// Handle both JSON array and shell format
			var currentEntrypoint string
			if bytes.HasPrefix(parts[1], []byte("[")) {
				// JSON array format: ENTRYPOINT ["python", "app.py"]
				// Get the last element before the closing bracket
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				var entrypointArray []string
				if err := json.Unmarshal(jsonStr, &entrypointArray); err != nil {
					return nil, fmt.Errorf("invalid ENTRYPOINT JSON format: %v", err)
				}
				if len(entrypointArray) > 0 {
					currentEntrypoint = entrypointArray[len(entrypointArray)-1]
				}
			} else {
				// Shell format: ENTRYPOINT python app.py
				currentEntrypoint = string(parts[len(parts)-1])
			}

			logger.Debugw("found entrypoint", "entrypoint", currentEntrypoint)

			// Preserve the original format
			if bytes.HasPrefix(parts[1], []byte("[")) {
				// Replace the last element in the JSON array
				var entrypointArray []string
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				if err := json.Unmarshal(jsonStr, &entrypointArray); err != nil {
					return nil, err
				}
				entrypointArray[len(entrypointArray)-1] = newEntrypoint
				newJSON, err := json.Marshal(entrypointArray)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&result, "ENTRYPOINT %s\n", newJSON)
			} else {
				// Preserve the original command but replace the last part
				parts[len(parts)-1] = []byte(newEntrypoint)
				result.Write(bytes.Join(parts, []byte(" ")))
				result.WriteByte('\n')
			}
		} else if bytes.HasPrefix(trimmedLine, []byte("CMD")) {
			// Handle CMD JSON array format: CMD ["python", "main.py", "start"] or CMD ["uv", "run", "main.py", "start"]
			parts := bytes.Fields(trimmedLine)
			if len(parts) >= 2 && bytes.HasPrefix(parts[1], []byte("[")) {
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				var cmdArray []string
				if err := json.Unmarshal(jsonStr, &cmdArray); err != nil {
					return nil, err
				}

				// Find and replace the Python file in the command array
				for i, arg := range cmdArray {
					if strings.HasSuffix(arg, ".py") {
						cmdArray[i] = newEntrypoint
						break
					}
				}

				newJSON, err := json.Marshal(cmdArray)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&result, "CMD %s\n", newJSON)
			} else {
				// Handle non-JSON CMD format, just pass through
				result.Write(line)
				if i < len(lines)-1 {
					result.WriteByte('\n')
				}
			}
		} else if bytes.HasPrefix(trimmedLine, fmt.Appendf(nil, "RUN python %s", pythonEntrypoint)) {
			line = bytes.ReplaceAll(line, []byte(pythonEntrypoint), []byte(newEntrypoint))
			result.Write(line)
			if i < len(lines)-1 {
				result.WriteByte('\n')
			}
		} else if bytes.HasPrefix(trimmedLine, fmt.Appendf(nil, "RUN uv run %s", pythonEntrypoint)) {
			// Handle UV-specific RUN commands
			line = bytes.ReplaceAll(line, []byte(pythonEntrypoint), []byte(newEntrypoint))
			result.Write(line)
			if i < len(lines)-1 {
				result.WriteByte('\n')
			}
		} else {
			result.Write(line)
			if i < len(lines)-1 {
				result.WriteByte('\n')
			}
		}
	}

	return result.Bytes(), nil
}
