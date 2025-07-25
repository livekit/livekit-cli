package agentfs

import (
	"testing"
)

func TestLoadDockerFiles(t *testing.T) {
	expectedFiles := []string{
		"examples/node.Dockerfile",
		"examples/node.dockerignore",
		"examples/python.pip.Dockerfile",
		"examples/python.pip.dockerignore",
		"examples/python.uv.Dockerfile",
		"examples/python.uv.dockerignore",
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
