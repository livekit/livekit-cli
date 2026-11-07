package agentfs

import (
	"strings"
	"testing"
)

func TestStartCommandWarning(t *testing.T) {
	cases := []struct {
		name       string
		dockerfile string
		warns      bool
	}{
		{"node exec form", "FROM node:22-slim\nCMD [\"node\", \"dist/agent.js\", \"start\"]", false},
		{"pnpm start", "FROM node:22-slim\nCMD [\"pnpm\", \"start\"]", true},
		{"npm start", "FROM node:22-slim\nCMD [\"npm\", \"start\"]", true},
		{"yarn via path", "FROM node:22-slim\nCMD [\"/usr/local/bin/yarn\", \"start\"]", true},
		{"shell form", "FROM python:3.12\nCMD python agent.py start", true},
		{"shell form with exec", "FROM python:3.12\nCMD exec python agent.py start", false},
		{"explicit sh -c", "FROM python:3.12\nCMD [\"sh\", \"-c\", \"python agent.py start\"]", true},
		{"bash shell form", "FROM python:3.12\nSHELL [\"/bin/bash\", \"-c\"]\nCMD python agent.py start", false},
		{"uv run", "FROM python:3.12\nCMD [\"uv\", \"run\", \"agent.py\", \"start\"]", false},
		{"entrypoint plus cmd", "FROM node:22-slim\nENTRYPOINT [\"pnpm\"]\nCMD [\"start\"]", true},
		{"cmd in base stage", "FROM node:22-slim AS base\nCMD [\"pnpm\", \"start\"]\nFROM base\nRUN true", true},
		{"cmd only in build stage", "FROM node:22-slim AS build\nCMD [\"pnpm\", \"start\"]\nFROM node:22-slim\nCMD [\"node\", \"agent.js\"]", false},
		{"no cmd", "FROM node:22-slim", false},
		{"unparsable", "NOT A DOCKERFILE", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StartCommandWarning([]byte(tc.dockerfile))
			if (got != "") != tc.warns {
				t.Fatalf("warns = %v, want %v (%q)", got != "", tc.warns, got)
			}
		})
	}
}

func TestStartCommandWarningTemplates(t *testing.T) {
	for _, tc := range []struct {
		projectType ProjectType
		warns       bool
	}{
		{ProjectTypeNode, true},
		{ProjectTypePythonUV, false},
		{ProjectTypePythonPip, false},
	} {
		t.Run(string(tc.projectType), func(t *testing.T) {
			content, err := embedfs.ReadFile("examples/" + string(tc.projectType) + ".Dockerfile")
			if err != nil {
				t.Fatal(err)
			}
			rendered := strings.ReplaceAll(string(content), "{{.ProgramMain}}", "agent.py")
			if got := StartCommandWarning([]byte(rendered)); (got != "") != tc.warns {
				t.Fatalf("warns = %v, want %v (%q)", got != "", tc.warns, got)
			}
		})
	}
}
