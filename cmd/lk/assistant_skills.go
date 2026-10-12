// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/livekit/livekit-cli/v2/pkg/skills"
)

// codingAgentSetup is what LiveKit has already set up for the user's coding
// agents, so the assistant doesn't offer to install it again. It reads only
// local files, so it doesn't check whether the skills are current.
type codingAgentSetup struct {
	Detected []string    `json:"coding_agents_detected"`
	Project  skillsScope `json:"project"` // in the user's current directory
	Global   skillsScope `json:"global"`  // for all their projects
	// Ready is true when LiveKit skills and the Docs MCP server are both set
	// up for at least one coding agent, in either scope.
	Ready bool `json:"ready"`
}

type skillsScope struct {
	Skills []string `json:"livekit_skills"`
	MCP    []string `json:"docs_mcp_configured_for"` // coding agent IDs
}

func checkCodingAgentSetup(dir string) codingAgentSetup {
	home, _ := os.UserHomeDir()
	env := &skills.Env{Home: home, Root: dir}
	var setup codingAgentSetup
	setup.Detected = agentIDs(skills.DetectAgents(env))
	if setup.Detected == nil {
		setup.Detected = []string{}
	}
	setup.Project = checkSkillsScope(env, skills.ScopeProject)
	setup.Global = checkSkillsScope(env, skills.ScopeGlobal)
	hasSkills := len(setup.Project.Skills) > 0 || len(setup.Global.Skills) > 0
	hasMCP := len(setup.Project.MCP) > 0 || len(setup.Global.MCP) > 0
	setup.Ready = hasSkills && hasMCP
	return setup
}

func checkSkillsScope(env *skills.Env, scope skills.Scope) skillsScope {
	st := skillsScope{Skills: []string{}, MCP: []string{}}
	if in, err := skills.NewInstaller(env, scope, nil); err == nil {
		dirs := skills.GroupSkillsDirs(env, scope, skills.Agents)
		names, _ := in.InstalledLiveKitSkills(dirs)
		// The lock can list skills whose files are gone; count only copies
		// that are there.
		copies, _ := in.Inspect(names, dirs)
		for _, c := range copies {
			if c.State != skills.StateMissing && !slices.Contains(st.Skills, c.Skill) {
				st.Skills = append(st.Skills, c.Skill)
			}
		}
		slices.Sort(st.Skills)
	}
	for _, a := range skills.Agents {
		if skills.CheckMCP(env, scope, a).State == skills.MCPConfigured {
			st.MCP = append(st.MCP, a.ID)
		}
	}
	return st
}

func (c codingAgentSetup) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}
