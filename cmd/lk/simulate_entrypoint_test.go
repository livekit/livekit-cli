package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

func TestFindEntrypointDefaults(t *testing.T) {
	cases := []struct {
		name        string
		projectType agentfs.ProjectType
		files       []string
		want        string
	}{
		{"python agent.py", agentfs.ProjectTypePythonUV, []string{"agent.py"}, "agent.py"},
		{"python main.py", agentfs.ProjectTypePythonUV, []string{"main.py"}, "main.py"},
		{"python agent.py wins", agentfs.ProjectTypePythonUV, []string{"main.py", "agent.py"}, "agent.py"},
		{"python src/main.py", agentfs.ProjectTypePythonUV, []string{"src/main.py"}, "src/main.py"},
		{"node main.ts", agentfs.ProjectTypeNode, []string{"main.ts"}, "main.ts"},
		{"node main.js", agentfs.ProjectTypeNode, []string{"main.js"}, "main.js"},
		{"node main.ts wins", agentfs.ProjectTypeNode, []string{"main.js", "main.ts"}, "main.ts"},
		{"node src/main.js", agentfs.ProjectTypeNode, []string{"src/main.js"}, "src/main.js"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				path := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := findEntrypoint(dir, "", tc.projectType)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.ToSlash(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
