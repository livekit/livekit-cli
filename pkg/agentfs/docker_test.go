package agentfs

import (
	"testing"
)

func TestLoadDockerFiles(t *testing.T) {
	expectedFiles := []string{
		"examples/node.Dockerfile",
		"examples/node.dockerignore",
		"examples/python.Dockerfile",
		"examples/python.dockerignore",
	}

	for _, file := range expectedFiles {
		bytes, err := fs.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read Dockerfile: %v", err)
		}
		if len(bytes) == 0 {
			t.Fatalf("Dockerfile empty: %s", file)
		}
	}
}
