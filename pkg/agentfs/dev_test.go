package agentfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInstruction(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
		wantErr  bool
	}{
		{
			name:     "JSON array format",
			input:    `CMD ["/bin/sh", "-c", "echo hello"]`,
			expected: []string{"/bin/sh", "-c", "echo hello"},
		},
		{
			name:     "Shell format",
			input:    "CMD python app.py",
			expected: []string{"python", "app.py"},
		},
		{
			name:     "ENTRYPOINT JSON",
			input:    `ENTRYPOINT ["python", "-m", "myapp"]`,
			expected: []string{"python", "-m", "myapp"},
		},
		{
			name:     "Invalid format",
			input:    "CMD",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseInstruction(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d args, got %d", len(tt.expected), len(result))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("Expected arg[%d] = %s, got %s", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestAnalyzeDockerfile(t *testing.T) {
	dockerfileContent := []string{
		"FROM python:3.11-slim",
		"WORKDIR /app",
		"USER appuser",
		"COPY . .",
		"RUN pip install -r requirements.txt",
		`ENTRYPOINT ["python"]`,
		`CMD ["-m", "myapp"]`,
	}

	analysis, err := analyzeDockerfile(dockerfileContent)
	if err != nil {
		t.Fatalf("Failed to analyze Dockerfile: %v", err)
	}

	if analysis.LastFromIndex != 0 {
		t.Errorf("Expected LastFromIndex = 0, got %d", analysis.LastFromIndex)
	}

	if analysis.LastUserIndex != 2 {
		t.Errorf("Expected LastUserIndex = 2, got %d", analysis.LastUserIndex)
	}

	if analysis.LastUserValue != "appuser" {
		t.Errorf("Expected LastUserValue = 'appuser', got '%s'", analysis.LastUserValue)
	}

	if len(analysis.OriginalEntrypoint) != 1 || analysis.OriginalEntrypoint[0] != "python" {
		t.Errorf("Expected OriginalEntrypoint = ['python'], got %v", analysis.OriginalEntrypoint)
	}

	if len(analysis.OriginalCmd) != 2 || analysis.OriginalCmd[0] != "-m" || analysis.OriginalCmd[1] != "myapp" {
		t.Errorf("Expected OriginalCmd = ['-m', 'myapp'], got %v", analysis.OriginalCmd)
	}
}

func TestConvertToDevDockerfile(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "dockerfile-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test Dockerfile
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")
	dockerfileContent := `FROM python:3.11-slim
WORKDIR /app
USER appuser
COPY . .
RUN pip install -r requirements.txt
ENTRYPOINT ["python"]
CMD ["-m", "myapp"]
`
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("Failed to write test Dockerfile: %v", err)
	}

	// Convert to dev Dockerfile
	if err := ConvertToDevDockerfile(dockerfilePath); err != nil {
		t.Fatalf("Failed to convert Dockerfile: %v", err)
	}

	// Check that Dockerfile.dev was created
	devPath := filepath.Join(tempDir, "Dockerfile.dev")
	if _, err := os.Stat(devPath); err != nil {
		t.Fatalf("Dockerfile.dev was not created: %v", err)
	}

	// Read and verify content
	content, err := os.ReadFile(devPath)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile.dev: %v", err)
	}

	contentStr := string(content)

	// Check for key injections
	checks := []string{
		"=== BEGIN LIVEKIT DEV-MODE INJECTION ===",
		"=== END LIVEKIT DEV-MODE INJECTION ===",
		"apt-get install -y nodejs",
		"npm install -g nodemon",
		"cloudflared",
		"COPY dev-tools /opt/livekit-dev-tools/",
		`ENTRYPOINT ["/opt/livekit-dev-tools/live-dev-entrypoint.sh"]`,
		`CMD ["python","-m","myapp"]`,
		"USER root",
		"USER appuser", // Should switch back to original user
	}

	for _, check := range checks {
		if !strings.Contains(contentStr, check) {
			t.Errorf("Expected content to contain '%s', but it didn't", check)
		}
	}

	// Verify original commands are commented out
	if !strings.Contains(contentStr, "# DEV-MODE: Original command commented out") {
		t.Error("Expected original commands to be commented out")
	}
}

func TestConvertDockerfileWithoutUserInstruction(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "dockerfile-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test Dockerfile without USER instruction
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")
	dockerfileContent := `FROM node:20-slim
WORKDIR /app
COPY . .
RUN npm install
CMD ["node", "server.js"]
`
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("Failed to write test Dockerfile: %v", err)
	}

	// Convert to dev Dockerfile
	if err := ConvertToDevDockerfile(dockerfilePath); err != nil {
		t.Fatalf("Failed to convert Dockerfile: %v", err)
	}

	// Read and verify content
	devPath := filepath.Join(tempDir, "Dockerfile.dev")
	content, err := os.ReadFile(devPath)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile.dev: %v", err)
	}

	contentStr := string(content)

	// Should not have "USER appuser" since original didn't have USER instruction
	if strings.Contains(contentStr, "USER appuser") {
		t.Error("Should not contain 'USER appuser' when original has no USER instruction")
	}

	// Should still have the dev mode injections
	if !strings.Contains(contentStr, "=== BEGIN LIVEKIT DEV-MODE INJECTION ===") {
		t.Error("Expected dev mode injection markers")
	}
}

func TestConvertDockerfileNoCommand(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "dockerfile-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test Dockerfile without CMD or ENTRYPOINT
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")
	dockerfileContent := `FROM alpine:latest
WORKDIR /app
COPY . .
`
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("Failed to write test Dockerfile: %v", err)
	}

	// Convert to dev Dockerfile
	if err := ConvertToDevDockerfile(dockerfilePath); err != nil {
		t.Fatalf("Failed to convert Dockerfile: %v", err)
	}

	// Read and verify content
	devPath := filepath.Join(tempDir, "Dockerfile.dev")
	content, err := os.ReadFile(devPath)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile.dev: %v", err)
	}

	contentStr := string(content)

	// Should have fallback command
	if !strings.Contains(contentStr, "Warning: No original CMD or ENTRYPOINT found") {
		t.Error("Expected warning message for missing CMD/ENTRYPOINT")
	}
}