package agentfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/livekit/protocol/logger"
)

const (
	pythonDockerfileURL   = "https://raw.githubusercontent.com/livekit-examples/agent-deployment/refs/heads/main/python-agent-example-app/Dockerfile"
	pythonDockerIgnoreURL = "https://raw.githubusercontent.com/livekit-examples/agent-deployment/refs/heads/main/python-agent-example-app/.dockerignore"
	pythonEntrypoint      = "main.py"
	nodeDockerfileURL     = "https://raw.githubusercontent.com/livekit-examples/agent-deployment/refs/heads/main/node-agent-example-docker/Dockerfile"
	nodeDockerIgnoreURL   = "https://raw.githubusercontent.com/livekit-examples/agent-deployment/refs/heads/main/node-agent-example-docker/.dockerignore"
)

func FindDockerfile(dir string) (bool, error) {
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

func CreateDockerfile(dir string) error {
	projectType := ""
	if isNode(dir) {
		projectType = "node"
	} else if isPython(dir) {
		projectType = "python"
	} else {
		return fmt.Errorf("Unable to determine project type. Please create a Dockerfile in the current directory.")
	}

	var dockerfileContent []byte
	var dockerIgnoreContent []byte
	var err error
	switch projectType {
	case "python":
		dockerfileContent, err = downloadFile(pythonDockerfileURL)
		if err != nil {
			return err
		}
		dockerIgnoreContent, err = downloadFile(pythonDockerIgnoreURL)
		if err != nil {
			return err
		}
	case "node":
		dockerfileContent, err = downloadFile(nodeDockerfileURL)
		if err != nil {
			return err
		}
		dockerIgnoreContent, err = downloadFile(nodeDockerIgnoreURL)
		if err != nil {
			return err
		}
	}

	if projectType == "python" {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, projectType)
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

func downloadFile(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch file: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func validateEntrypoint(dir string, dockerfileContent []byte, projectType string) ([]byte, error) {
	fileList := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fileList[entry.Name()] = true
	}

	valFile := func(fileName string) (string, error) {
		if _, exists := fileList[fileName]; exists {
			return fileName, nil
		}

		var suffix string
		switch projectType {
		case "python":
			suffix = ".py"
		case "node":
			suffix = ".js"
		}

		// Collect all matching files
		var options []string
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), suffix) {
				options = append(options, entry.Name())
			}
		}

		// If no matching files found, return early
		if len(options) == 0 {
			return "", nil
		}

		var selected string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Select %s file to use as entrypoint", projectType)).
					Options(huh.NewOptions(options...)...).
					Value(&selected),
			),
		)

		err := form.Run()
		if err != nil {
			return "", err
		}

		return selected, nil
	}

	newEntrypoint, err := valFile(pythonEntrypoint)
	if err != nil {
		return nil, err
	}

	lines := bytes.Split(dockerfileContent, []byte("\n"))
	var result bytes.Buffer
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := bytes.TrimSpace(line)

		if bytes.HasPrefix(trimmedLine, []byte("ENTRYPOINT")) {
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
			// Handle CMD JSON array format: CMD ["python", "main.py", "start"]
			parts := bytes.Fields(trimmedLine)
			if len(parts) >= 2 && bytes.HasPrefix(parts[1], []byte("[")) {
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				var cmdArray []string
				if err := json.Unmarshal(jsonStr, &cmdArray); err != nil {
					return nil, err
				}
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
			}
		} else if bytes.HasPrefix(trimmedLine, []byte(fmt.Sprintf("RUN python %s", pythonEntrypoint))) {
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
