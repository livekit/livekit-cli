package agentfs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

// Constants for the code blocks to be injected into the Dockerfile
const (
	installBlock = `
# === BEGIN LIVEKIT DEV-MODE INJECTION ===
# Switch to root to install dependencies
USER root

# Install system dependencies for dev mode: curl, Node.js (for nodemon), and cloudflared
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && NODE_MAJOR=20 \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_$NODE_MAJOR.x nodistro main" | tee /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y nodejs \
    && npm install -g nodemon \
    && ARCH=$(dpkg --print-architecture) \
    && echo "Detected architecture: $ARCH" \
    && curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}.deb \
    && dpkg -i cloudflared.deb && rm cloudflared.deb \
    && rm -rf /var/lib/apt/lists/*
`

	copyBlock = `
# Copy the dev tools into a standard, isolated location
COPY dev-tools/sync_server.py /opt/livekit-dev-tools/
COPY dev-tools/sync_server.js /opt/livekit-dev-tools/
COPY dev-tools/live-dev-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/live-dev-entrypoint.sh
`

	finalInstructionsTemplate = `
# Set the entrypoint to our dev-mode script. It will start all background
# services and then execute the original CMD from below.
ENTRYPOINT ["/usr/local/bin/live-dev-entrypoint.sh"]

# The original command is passed as arguments to the new entrypoint
CMD %s
# === END LIVEKIT DEV-MODE INJECTION ===
`
)

// DockerfileInstruction represents a parsed Dockerfile instruction
type DockerfileInstruction struct {
	Command string
	Args    []string
	Raw     string
}

// ConvertToDevDockerfile converts a standard Dockerfile to a development-mode enabled Dockerfile
func ConvertToDevDockerfile(dockerfilePath string) error {
	// Check if file exists
	if _, err := os.Stat(dockerfilePath); err != nil {
		return fmt.Errorf("dockerfile not found at '%s': %w", dockerfilePath, err)
	}

	fmt.Printf("Processing '%s'...\n", dockerfilePath)

	// Read the Dockerfile
	lines, err := readDockerfileLines(dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to read dockerfile: %w", err)
	}

	// Analyze the Dockerfile
	analysis, err := analyzeDockerfile(lines)
	if err != nil {
		return err
	}

	// Apply modifications
	modifiedLines := applyDevModeModifications(lines, analysis)

	// Write the output
	outputPath := filepath.Join(filepath.Dir(dockerfilePath), "Dockerfile.dev")
	if err := writeDockerfile(outputPath, modifiedLines); err != nil {
		return fmt.Errorf("failed to write dev dockerfile: %w", err)
	}

	fmt.Printf("\n✔ Success! ✨\n")
	fmt.Printf("A new dev-mode enabled Dockerfile has been created at: %s\n", util.Accented(outputPath))
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("1. Ensure the 'dev-tools' directory is in the same folder.\n")
	fmt.Printf("2. Build the new image: docker build -t my-agent-dev -f %s .\n", outputPath)
	fmt.Printf("3. Run the container with the required environment variables:\n")
	fmt.Printf("   docker run --rm -it -e DEV_SYNC_TOKEN=\"your-secret\" -e AGENT_WORKDIR=\"/path/inside/container\" my-agent-dev\n")

	return nil
}

// DockerfileAnalysis contains the analysis results of a Dockerfile
type DockerfileAnalysis struct {
	LastFromIndex       int
	LastUserIndex       int
	LastUserValue       string
	OriginalEntrypoint  []string
	OriginalCmd         []string
	EntrypointLineIndex int
	CmdLineIndex        int
	UserCreationIndex   int    // Index where user is created (RUN adduser)
	FirstUserIndex      int    // First USER instruction
}

func readDockerfileLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

func analyzeDockerfile(lines []string) (*DockerfileAnalysis, error) {
	analysis := &DockerfileAnalysis{
		LastFromIndex:     -1,
		LastUserIndex:     -1,
		FirstUserIndex:    -1,
		UserCreationIndex: -1,
		LastUserValue:     "root", // Default Docker user
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		switch {
		case strings.HasPrefix(upper, "FROM"):
			analysis.LastFromIndex = i

		case strings.HasPrefix(upper, "USER"):
			if analysis.FirstUserIndex == -1 {
				analysis.FirstUserIndex = i
			}
			analysis.LastUserIndex = i
			parts := strings.Fields(trimmed)
			if len(parts) > 1 {
				analysis.LastUserValue = parts[1]
			}

		case strings.HasPrefix(upper, "RUN"):
			// Check if this RUN command creates a user (adduser or useradd)
			if strings.Contains(trimmed, "adduser") || strings.Contains(trimmed, "useradd") {
				// This is likely where a user is being created
				// Find the end of multi-line RUN command
				endIndex := i
				for j := i; j < len(lines); j++ {
					if !strings.HasSuffix(strings.TrimSpace(lines[j]), "\\") {
						endIndex = j
						break
					}
				}
				analysis.UserCreationIndex = endIndex
			}

		case strings.HasPrefix(upper, "ENTRYPOINT"):
			analysis.EntrypointLineIndex = i
			args, err := parseInstruction(trimmed)
			if err == nil {
				analysis.OriginalEntrypoint = args
			}

		case strings.HasPrefix(upper, "CMD"):
			analysis.CmdLineIndex = i
			args, err := parseInstruction(trimmed)
			if err == nil {
				analysis.OriginalCmd = args
			}
		}
	}

	if analysis.LastFromIndex == -1 {
		return nil, fmt.Errorf("could not find a 'FROM' instruction in the Dockerfile")
	}

	return analysis, nil
}

func parseInstruction(line string) ([]string, error) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid instruction format")
	}

	instructionBody := strings.TrimSpace(parts[1])

	// Check if it's JSON array format (exec form)
	if strings.HasPrefix(instructionBody, "[") {
		var args []string
		if err := json.Unmarshal([]byte(instructionBody), &args); err != nil {
			return nil, err
		}
		return args, nil
	}

	// Shell form - split by spaces (simplified version)
	// Note: This is a simplified parser that doesn't handle quoted strings perfectly
	return strings.Fields(instructionBody), nil
}

func applyDevModeModifications(lines []string, analysis *DockerfileAnalysis) []string {
	// Create a copy of lines to modify
	modifiedLines := make([]string, len(lines))
	copy(modifiedLines, lines)

	// Comment out original ENTRYPOINT and CMD
	if analysis.EntrypointLineIndex >= 0 {
		modifiedLines[analysis.EntrypointLineIndex] = fmt.Sprintf("# DEV-MODE: Original command commented out\n# %s", lines[analysis.EntrypointLineIndex])
	}
	if analysis.CmdLineIndex >= 0 {
		modifiedLines[analysis.CmdLineIndex] = fmt.Sprintf("# DEV-MODE: Original command commented out\n# %s", lines[analysis.CmdLineIndex])
	}

	// Combine original ENTRYPOINT and CMD
	finalCommand := append(analysis.OriginalEntrypoint, analysis.OriginalCmd...)
	if len(finalCommand) == 0 {
		fmt.Println("Warning: Could not determine the original CMD or ENTRYPOINT. The final container may not start correctly.")
		fmt.Println("Please ensure your original Dockerfile has a CMD or ENTRYPOINT instruction.")
		finalCommand = []string{"/bin/echo", "Warning: No original CMD or ENTRYPOINT found."}
	}

	// Convert final command to JSON
	finalCmdJSON, _ := json.Marshal(finalCommand)

	// Prepare the final instructions with the JSON command
	finalInstructions := fmt.Sprintf(finalInstructionsTemplate, string(finalCmdJSON))

	// Build the new lines with injections
	var newLines []string
	devModeInjected := false
	
	for i, line := range modifiedLines {
		// Check if we should inject dev-mode dependencies here
		shouldInjectHere := false
		
		if !devModeInjected {
			// If there's a user creation, inject after that
			if analysis.UserCreationIndex >= 0 && i == analysis.UserCreationIndex {
				shouldInjectHere = true
			} else if analysis.UserCreationIndex == -1 && i == analysis.LastFromIndex {
				// No user creation found, inject after FROM
				shouldInjectHere = true
			}
		}
		
		newLines = append(newLines, line)

		if shouldInjectHere {
			newLines = append(newLines, installBlock)
			// Only switch back to user if we have a USER instruction and the user exists
			if analysis.LastUserValue != "root" && analysis.FirstUserIndex > -1 && 
			   (analysis.UserCreationIndex == -1 || analysis.FirstUserIndex > analysis.UserCreationIndex) {
				userReset := fmt.Sprintf("\n# Switch back to the original user\nUSER %s\n", analysis.LastUserValue)
				newLines = append(newLines, userReset)
			}
			devModeInjected = true
		}

		// Insert COPY block before the first USER instruction or near the end
		if (analysis.FirstUserIndex > 0 && i == analysis.FirstUserIndex-1) ||
			(analysis.FirstUserIndex == -1 && i == len(modifiedLines)-2) {
			// Make sure we're root for the chmod operation
			if analysis.LastUserValue != "root" && analysis.FirstUserIndex > 0 {
				newLines = append(newLines, "\n# Switch to root for dev-tools setup\nUSER root")
			}
			newLines = append(newLines, copyBlock)
			// Switch back to the user if needed
			if analysis.LastUserValue != "root" && analysis.FirstUserIndex > 0 {
				newLines = append(newLines, fmt.Sprintf("# Switch back to the original user\nUSER %s\n", analysis.LastUserValue))
			}
		}
	}

	// Add final instructions at the end
	newLines = append(newLines, finalInstructions)

	return newLines
}

func writeDockerfile(path string, lines []string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
	}

	return writer.Flush()
}

// ConvertDockerfileInPlace reads a Dockerfile from the given path and overwrites it with the dev-mode version
func ConvertDockerfileInPlace(dockerfilePath string) error {
	// First convert to .dev file
	if err := ConvertToDevDockerfile(dockerfilePath); err != nil {
		return err
	}

	// Then move the .dev file to replace the original
	devPath := filepath.Join(filepath.Dir(dockerfilePath), "Dockerfile.dev")
	return os.Rename(devPath, dockerfilePath)
}

// ValidateDevModeDockerfile checks if a Dockerfile has dev-mode injections
func ValidateDevModeDockerfile(dockerfilePath string) (bool, error) {
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false, fmt.Errorf("failed to read dockerfile: %w", err)
	}

	contentStr := string(content)
	hasDevMode := strings.Contains(contentStr, "=== BEGIN LIVEKIT DEV-MODE INJECTION ===") &&
		strings.Contains(contentStr, "=== END LIVEKIT DEV-MODE INJECTION ===")

	return hasDevMode, nil
}

// ExtractOriginalCommands extracts the original CMD and ENTRYPOINT from a dev-mode Dockerfile
func ExtractOriginalCommands(dockerfilePath string) (entrypoint []string, cmd []string, err error) {
	lines, err := readDockerfileLines(dockerfilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read dockerfile: %w", err)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Look for commented out original commands
		if strings.HasPrefix(trimmed, "# DEV-MODE: Original command commented out") {
			continue
		}
		
		// Check the line after the comment marker
		if strings.HasPrefix(trimmed, "# ENTRYPOINT") || strings.HasPrefix(trimmed, "# CMD") {
			// Remove the comment prefix
			uncommented := strings.TrimPrefix(trimmed, "# ")
			
			if strings.HasPrefix(strings.ToUpper(uncommented), "ENTRYPOINT") {
				args, err := parseInstruction(uncommented)
				if err == nil {
					entrypoint = args
				}
			} else if strings.HasPrefix(strings.ToUpper(uncommented), "CMD") {
				args, err := parseInstruction(uncommented)
				if err == nil {
					cmd = args
				}
			}
		}
	}

	return entrypoint, cmd, nil
}