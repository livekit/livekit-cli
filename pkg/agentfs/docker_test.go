package agentfs

import (
	"testing"
)

func TestLoadDockerFiles(t *testing.T) {
	expectedFiles := []string{
		"examples/node.npm.Dockerfile",
		"examples/node.npm.dockerignore",
		"examples/node.pnpm.Dockerfile",
		"examples/node.pnpm.dockerignore",
		"examples/node.yarn.Dockerfile",
		"examples/node.yarn.dockerignore",
		"examples/node.yarn-berry.Dockerfile",
		"examples/node.yarn-berry.dockerignore",
		"examples/node.bun.Dockerfile",
		"examples/node.bun.dockerignore",
		"examples/python.pip.Dockerfile",
		"examples/python.pip.dockerignore",
		"examples/python.uv.Dockerfile",
		"examples/python.uv.dockerignore",
		"examples/python.poetry.Dockerfile",
		"examples/python.poetry.dockerignore",
		"examples/python.pipenv.Dockerfile",
		"examples/python.pipenv.dockerignore",
		"examples/python.pdm.Dockerfile",
		"examples/python.pdm.dockerignore",
		"examples/python.hatch.Dockerfile",
		"examples/python.hatch.dockerignore",
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
