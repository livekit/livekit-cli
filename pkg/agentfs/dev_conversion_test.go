package agentfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertDockerfileWithUserCreation(t *testing.T) {
	// Create a test Dockerfile similar to the problematic one
	testDockerfile := `# This is an example Dockerfile that builds a minimal container for running LK Agents
# syntax=docker/dockerfile:1
ARG PYTHON_VERSION=3.11.6
FROM python:${PYTHON_VERSION}-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

# Create a non-privileged user that the app will run under.
# See https://docs.docker.com/develop/develop-images/dockerfile_best-practices/#user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/home/appuser" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install gcc and other build dependencies.
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

USER appuser

RUN mkdir -p /home/appuser/.cache
RUN chown -R appuser /home/appuser/.cache

WORKDIR /home/appuser

COPY requirements.txt .
RUN python -m pip install --user --no-cache-dir -r requirements.txt

COPY . .

# ensure that any dependent models are downloaded at build-time
RUN python script.py download-files

# expose healthcheck port
EXPOSE 8081

# Run the application.
CMD ["python","script.py","start"]
`

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "dockerfile-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Write the test Dockerfile
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")
	err = os.WriteFile(dockerfilePath, []byte(testDockerfile), 0644)
	if err != nil {
		t.Fatalf("Failed to write test Dockerfile: %v", err)
	}

	// Convert the Dockerfile
	err = ConvertToDevDockerfile(dockerfilePath)
	if err != nil {
		t.Fatalf("Failed to convert Dockerfile: %v", err)
	}

	// Read the converted Dockerfile
	devDockerfilePath := filepath.Join(tempDir, "Dockerfile.dev")
	content, err := os.ReadFile(devDockerfilePath)
	if err != nil {
		t.Fatalf("Failed to read converted Dockerfile: %v", err)
	}

	convertedContent := string(content)

	// Check that dev-mode blocks are added
	if !strings.Contains(convertedContent, "=== BEGIN LIVEKIT DEV-MODE INJECTION ===") {
		t.Error("Missing dev-mode begin marker")
	}
	if !strings.Contains(convertedContent, "=== END LIVEKIT DEV-MODE INJECTION ===") {
		t.Error("Missing dev-mode end marker")
	}

	// Check that the user creation comes before any USER instruction in dev blocks
	lines := strings.Split(convertedContent, "\n")
	var userCreationLine int = -1
	var firstDevUserLine int = -1
	var inDevBlock bool = false

	for i, line := range lines {
		if strings.Contains(line, "=== BEGIN LIVEKIT DEV-MODE INJECTION ===") {
			inDevBlock = true
		}
		if strings.Contains(line, "=== END LIVEKIT DEV-MODE INJECTION ===") {
			inDevBlock = false
		}

		// Find the adduser command
		if strings.Contains(line, "RUN adduser") {
			userCreationLine = i
		}

		// Find the first USER instruction in dev block
		if inDevBlock && strings.HasPrefix(strings.TrimSpace(line), "USER") && firstDevUserLine == -1 {
			firstDevUserLine = i
		}
	}

	// Verify that if there's a USER instruction in dev block, it comes after user creation
	if firstDevUserLine != -1 && userCreationLine != -1 && firstDevUserLine < userCreationLine {
		t.Errorf("USER instruction (line %d) appears before user creation (line %d)", firstDevUserLine, userCreationLine)
	}

	// Check that the original CMD is preserved in the final CMD
	if !strings.Contains(convertedContent, `CMD ["python","script.py","start"]`) {
		t.Error("Original CMD not preserved correctly")
	}
}

func TestAnalyzeDockerfileUserCreation(t *testing.T) {
	testCases := []struct {
		name              string
		dockerfile        string
		expectedUserIndex int
		expectedUserValue string
		hasUserCreation   bool
	}{
		{
			name: "Simple user creation",
			dockerfile: `FROM ubuntu:latest
RUN adduser --disabled-password myuser
USER myuser`,
			expectedUserIndex: 2,
			expectedUserValue: "myuser",
			hasUserCreation:   true,
		},
		{
			name: "Multi-line user creation",
			dockerfile: `FROM ubuntu:latest
RUN adduser \
    --disabled-password \
    --gecos "" \
    myuser
USER myuser`,
			expectedUserIndex: 5, // USER is on line 5 (0-indexed)
			expectedUserValue: "myuser",
			hasUserCreation:   true,
		},
		{
			name: "No user creation",
			dockerfile: `FROM ubuntu:latest
USER nobody`,
			expectedUserIndex: 1,
			expectedUserValue: "nobody",
			hasUserCreation:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(tc.dockerfile, "\n")
			analysis, err := analyzeDockerfile(lines)
			if err != nil {
				t.Fatalf("Failed to analyze Dockerfile: %v", err)
			}

			if analysis.LastUserIndex != tc.expectedUserIndex {
				t.Errorf("Expected LastUserIndex %d, got %d", tc.expectedUserIndex, analysis.LastUserIndex)
			}

			if analysis.LastUserValue != tc.expectedUserValue {
				t.Errorf("Expected LastUserValue %s, got %s", tc.expectedUserValue, analysis.LastUserValue)
			}

			hasUserCreation := analysis.UserCreationIndex != -1
			if hasUserCreation != tc.hasUserCreation {
				t.Errorf("Expected hasUserCreation %v, got %v", tc.hasUserCreation, hasUserCreation)
			}
		})
	}
}

func TestValidateEntrypointPreservesDevMode(t *testing.T) {
	// Create a test dev-mode Dockerfile
	devDockerfile := `# syntax=docker/dockerfile:1
FROM python:3.11-slim

# Install dependencies
RUN apt-get update && apt-get install -y curl

# Setup dev tools
COPY dev-tools/live-dev-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/live-dev-entrypoint.sh

# Application setup
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY . .

# Dev-mode entrypoint - this should NOT be modified
ENTRYPOINT ["/usr/local/bin/live-dev-entrypoint.sh"]
CMD ["python", "app.py", "start"]
`

	// Create temp files
	tempDir, err := os.MkdirTemp("", "entrypoint-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Write Dockerfile
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")
	err = os.WriteFile(dockerfilePath, []byte(devDockerfile), 0644)
	if err != nil {
		t.Fatalf("Failed to write Dockerfile: %v", err)
	}

	// Create the app.py file so validateEntrypoint can find it
	err = os.WriteFile(filepath.Join(tempDir, "app.py"), []byte("print('test')"), 0644)
	if err != nil {
		t.Fatalf("Failed to write app.py: %v", err)
	}

	// Create dummy dockerignore
	dockerignoreContent := ""

	// Create dummy settings map
	settingsMap := map[string]string{
		"python_entrypoint": "app.py",
	}

	// Validate entrypoint
	result, err := validateEntrypoint(tempDir, []byte(devDockerfile), []byte(dockerignoreContent), ProjectTypePythonPip, settingsMap)
	if err != nil {
		t.Fatalf("validateEntrypoint failed: %v", err)
	}

	resultStr := string(result)

	// Check that dev-entrypoint.sh is preserved
	if !strings.Contains(resultStr, "/usr/local/bin/live-dev-entrypoint.sh") {
		t.Errorf("Dev-mode entrypoint was not preserved")
		t.Logf("Result:\n%s", resultStr)
	}

	// Check that the ENTRYPOINT line wasn't replaced with app.py
	if strings.Contains(resultStr, `ENTRYPOINT ["app.py"]`) {
		t.Errorf("Dev-mode entrypoint was incorrectly replaced with app.py")
	}

	// Check that CMD still references the Python script
	if !strings.Contains(resultStr, `CMD ["python","app.py","start"]`) {
		t.Errorf("CMD was not updated correctly")
		t.Logf("Looking for: CMD [\"python\",\"app.py\",\"start\"]")
		t.Logf("Result:\n%s", resultStr)
	}
}

func TestCreateDevDockerfileEntrypoint(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "dev-dockerfile-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a minimal Python project
	requirementsContent := `livekit
livekit-agents`

	appContent := `import livekit
print("Hello from LiveKit agent")`

	// Write files
	err = os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(requirementsContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write requirements.txt: %v", err)
	}

	err = os.WriteFile(filepath.Join(tempDir, "app.py"), []byte(appContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write app.py: %v", err)
	}

	// Mock settings
	settingsMap := map[string]string{
		"python_entrypoint": "app.py",
	}

	// Create dev Dockerfile
	err = CreateDevDockerfile(tempDir, settingsMap)
	if err != nil {
		t.Fatalf("Failed to create dev Dockerfile: %v", err)
	}

	// Read the created Dockerfile
	dockerfilePath := filepath.Join(tempDir, "livekit.develop.Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("Failed to read dev Dockerfile: %v", err)
	}

	contentStr := string(content)

	// Verify entrypoint is correct
	if !strings.Contains(contentStr, `ENTRYPOINT ["/usr/local/bin/live-dev-entrypoint.sh"]`) {
		t.Errorf("Dev Dockerfile does not have correct ENTRYPOINT")
		t.Logf("Content:\n%s", contentStr)
	}

	// Verify CMD contains the Python script
	if !strings.Contains(contentStr, `CMD ["python","app.py","start"]`) {
		t.Errorf("Dev Dockerfile does not have correct CMD")
	}

	// Verify dev-tools are copied
	if !strings.Contains(contentStr, "COPY dev-tools/live-dev-entrypoint.sh /usr/local/bin/") {
		t.Errorf("Dev Dockerfile does not copy entrypoint script")
	}
}
