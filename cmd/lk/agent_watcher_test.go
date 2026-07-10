package main

import (
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/agentfs"
)

// TestDevChannelAddrFlagFor models handing a (re)started dev-mode agent the
// dev-channel address. The flag was renamed --reload-addr -> --cli-addr, but a
// released SDK handed a flag it predates fails hard at startup (Node's CLI
// rejects unknown options), so the choice keys off the interpreter-resolved
// installed version and every ambiguous case must fall back to what released
// SDKs accept.
func TestDevChannelAddrFlagFor(t *testing.T) {
	tests := []struct {
		name        string
		projectType agentfs.ProjectType
		version     string
		expect      string
	}{
		{
			name:        "python below cutover keeps legacy reload-addr",
			projectType: agentfs.ProjectTypePythonUV,
			version:     "1.6.5",
			expect:      "--reload-addr",
		},
		{
			name:        "python at cutover uses cli-addr",
			projectType: agentfs.ProjectTypePythonPip,
			version:     firstPythonCLIAddrVersion,
			expect:      "--cli-addr",
		},
		{
			name:        "python with undetermined version keeps legacy reload-addr",
			projectType: agentfs.ProjectTypePythonUV,
			version:     "",
			expect:      "--reload-addr",
		},
		{
			name:        "node below cutover gets no flag",
			projectType: agentfs.ProjectTypeNode,
			version:     "1.5.0",
			expect:      "",
		},
		{
			name:        "node at cutover uses cli-addr",
			projectType: agentfs.ProjectTypeNode,
			version:     firstNodeCLIAddrVersion,
			expect:      "--cli-addr",
		},
		{
			name:        "node with undetermined version gets no flag",
			projectType: agentfs.ProjectTypeNode,
			version:     "",
			expect:      "",
		},
		{
			name:        "node with unparseable local build gets no flag",
			projectType: agentfs.ProjectTypeNode,
			version:     "workspace-dev",
			expect:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := devChannelAddrFlagFor(tt.projectType, tt.version); got != tt.expect {
				t.Errorf("devChannelAddrFlagFor(%s, %q) = %q, expected %q",
					tt.projectType, tt.version, got, tt.expect)
			}
		})
	}
}
