package agentfs

import (
	"path"
	"testing"
)

func TestLoadDockerFiles(t *testing.T) {
	expectedFiles := []string{
		path.Join("examples", "node.Dockerfile"),
		path.Join("examples", "node.dockerignore"),
		path.Join("examples", "python.pip.Dockerfile"),
		path.Join("examples", "python.pip.dockerignore"),
		path.Join("examples", "python.uv.Dockerfile"),
		path.Join("examples", "python.uv.dockerignore"),
	}

	for _, file := range expectedFiles {
		bytes, err := embedfs.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read Dockerfile: %v", err)
		}
		if len(bytes) == 0 {
			t.Fatalf("Dockerfile empty: %s", file)
		}
	}
}
