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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
	"github.com/livekit/livekit-cli/v2/pkg/config"
)

const defaultDocsServerURL = "https://docs.livekit.io/mcp/"

// docsRequestTimeout is the maximum time allowed for a complete docs MCP
// request (connect + call + response). This prevents the CLI from hanging
// indefinitely if the server is unresponsive.
const docsRequestTimeout = 30 * time.Second

// expectedServerVersion is the major.minor version of the LiveKit docs MCP
// server that this CLI was built against. If the server reports a newer
// major or minor version, a warning is printed to stderr suggesting the
// user update their CLI.
var expectedServerVersion = [2]int{1, 2}

var (
	DocsCommands = []*cli.Command{
		{
			Name:  "docs",
			Usage: "Search and browse LiveKit documentation",
			Description: `Query the LiveKit documentation directly from the terminal. Powered by
the LiveKit docs MCP server (https://docs.livekit.io/mcp).

Typical workflow:

  1. Start with an overview of the docs site:
       lk docs overview

  2. Search for a topic:
       lk docs search "voice agents"

  3. Fetch a specific page to read:
       lk docs get-page /agents/start/voice-ai-quickstart

  4. Search for code across LiveKit repositories:
       lk docs code-search "class AgentSession" --repo livekit/agents

All output is rendered as markdown.`,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:    "json",
					Aliases: []string{"j"},
					Usage:   "Output as JSON instead of markdown",
				},
				&cli.StringFlag{
					Name:   "server-url",
					Hidden: true,
				},
				&cli.StringFlag{
					Name:   "vercel-header",
					Hidden: true,
				},
			},
			Commands: []*cli.Command{
				{
					Name:  "overview",
					Usage: "Get a complete overview of the documentation site and table of contents",
					Description: `Returns the full docs site table of contents with page descriptions.
This is a great starting point to load context for browsing conceptual
docs rather than relying wholly on search.`,
					Action: docsOverview,
				},
				{
					Name:      "search",
					Usage:     "Search the LiveKit documentation",
					ArgsUsage: "[QUERY]",
					Description: `Search the docs for a given query. Returns paged results showing page
titles, hierarchical placement, and (sometimes) a content snippet.
Results can then be fetched via "lk docs get-page" for full content.

The search index covers a large amount of content in many programming
languages. Search should be used as a complement to browsing docs
directly (via "lk docs overview"), not a replacement.`,
					Action: docsSearch,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "query",
							Aliases: []string{"q"},
							Usage:   "Search `QUERY` text",
						},
						&cli.IntFlag{
							Name:    "page",
							Aliases: []string{"p"},
							Usage:   "Page number (starts at 0)",
						},
						&cli.IntFlag{
							Name:  "hits-per-page",
							Usage: "Results per page (1-50, default 20)",
						},
					},
				},
				{
					Name:      "get-page",
					Aliases:   []string{"get-pages"},
					Usage:     "Fetch one or more documentation pages as markdown",
					ArgsUsage: "PATH [PATH...]",
					Description: `Render one or more docs pages to markdown by relative path. Also
supports fetching code from public LiveKit repositories on GitHub.

Examples:
  lk docs get-page /agents/start/voice-ai-quickstart
  lk docs get-page /agents/build/tools /agents/build/vision
  lk docs get-page https://github.com/livekit/agents/blob/main/README.md

Note: auto-generated SDK reference pages (e.g. /reference/client-sdk-js)
are hosted externally and cannot be fetched with this command.`,
					Action: docsGetPage,
				},
				{
					Name:      "code-search",
					Usage:     "Search code across LiveKit GitHub repositories",
					ArgsUsage: "[QUERY]",
					Description: `High-precision GitHub code search across LiveKit repositories. Search
like code, not like English — use actual class names, function names,
and method calls rather than descriptions. Regex is not supported.

Good queries:  "class AgentSession", "def on_enter", "@function_tool"
Bad queries:   "how does handoff work", "agent transfer implementation"

Results come from default branches; very new code in feature branches
may not appear.`,
					Action: docsCodeSearch,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "query",
							Aliases: []string{"q"},
							Usage:   "Search term (use code identifiers, not natural language)",
						},
						&cli.StringFlag{
							Name:    "repo",
							Aliases: []string{"r"},
							Usage:   "Target `REPO` (e.g. livekit/agents) or ALL",
							Value:   "ALL",
						},
						&cli.StringFlag{
							Name:    "language",
							Aliases: []string{"l"},
							Usage:   "Language filter (e.g. Python, TypeScript)",
						},
						&cli.StringFlag{
							Name:  "scope",
							Usage: "Search scope: content, filename, or both",
							Value: "content",
						},
						&cli.IntFlag{
							Name:  "limit",
							Usage: "Max results to return (1-50)",
							Value: 20,
						},
						&cli.BoolFlag{
							Name:  "full-file",
							Usage: "Return full file content instead of snippets",
						},
					},
				},
				{
					Name:      "changelog",
					Usage:     "Get recent releases and changelog for a LiveKit SDK or package",
					ArgsUsage: "IDENTIFIER",
					Description: `Get recent releases for a LiveKit repository or package. Supports
repository IDs (e.g. "livekit/agents") and package identifiers
(e.g. "pypi:livekit-agents", "npm:livekit-client", "cargo:livekit").

Examples:
  lk docs changelog livekit/agents
  lk docs changelog pypi:livekit-agents
  lk docs changelog npm:@livekit/components-react
  lk docs changelog --releases 5 livekit/client-sdk-js`,
					Action: docsChangelog,
					Flags: []cli.Flag{
						&cli.IntFlag{
							Name:  "releases",
							Usage: "Number of releases to fetch (1-20)",
							Value: 2,
						},
						&cli.IntFlag{
							Name:  "skip",
							Usage: "Number of releases to skip for pagination",
						},
					},
				},
				{
					Name:  "list-sdks",
					Usage: "List all LiveKit SDK repositories and package names",
					Description: `Returns a list of all LiveKit SDK repositories (client SDKs, server
SDKs, and agent frameworks) with their package names for each platform.
Useful for cross-referencing dependencies and finding the right SDK.`,
					Action: docsListSDKs,
				},
				{
					Name:      "submit-feedback",
					Usage:     "Submit feedback on the LiveKit documentation",
					ArgsUsage: "[FEEDBACK]",
					Description: `Submit constructive feedback on the LiveKit docs. This feedback is
read by the LiveKit team and used to improve the documentation.
Do not include any personal or proprietary information.

Examples:
  lk docs submit-feedback "The voice agents quickstart needs a Node.js example"
  lk docs submit-feedback --page /agents/build/tools "Missing info about error handling"`,
					Action: docsSubmitFeedback,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "page",
							Usage: "The docs `PAGE` the feedback is about (e.g. /agents/build/tools)",
						},
						&cli.StringFlag{
							Name:    "feedback",
							Aliases: []string{"f"},
							Usage:   "Feedback text (max 1024 characters)",
						},
						&cli.StringFlag{
							Name:  "agent",
							Usage: "Identity of the agent submitting feedback (e.g. \"Cursor\", \"Claude Code\")",
						},
						&cli.StringFlag{
							Name:  "model",
							Usage: "Model `ID` used by the agent (e.g. \"gpt-5\", \"claude-4.5-sonnet\")",
						},
					},
				},
			},
		},
	}
)

// ---------------------------------------------------------------------------
// Command handlers
// ---------------------------------------------------------------------------

func docsOverview(ctx context.Context, cmd *cli.Command) error {
	return callDocsToolAndPrint(ctx, cmd, "get_docs_overview", map[string]any{})
}

func docsSearch(ctx context.Context, cmd *cli.Command) error {
	query := cmd.String("query")
	if query == "" && cmd.Args().Len() > 0 {
		query = strings.Join(cmd.Args().Slice(), " ")
	}
	if query == "" {
		return cli.ShowSubcommandHelp(cmd)
	}

	args := map[string]any{
		"query": query,
	}
	if p := cmd.Int("page"); p > 0 {
		args["page"] = p
	}
	if hpp := cmd.Int("hits-per-page"); hpp > 0 {
		args["hitsPerPage"] = hpp
	}

	return callDocsToolAndPrint(ctx, cmd, "docs_search", args)
}

func docsGetPage(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}

	return callDocsToolAndPrint(ctx, cmd, "get_pages", map[string]any{
		"paths": cmd.Args().Slice(),
	})
}

func docsCodeSearch(ctx context.Context, cmd *cli.Command) error {
	query := cmd.String("query")
	if query == "" && cmd.Args().Len() > 0 {
		query = strings.Join(cmd.Args().Slice(), " ")
	}
	if query == "" {
		return cli.ShowSubcommandHelp(cmd)
	}

	args := map[string]any{
		"query": query,
		"repo":  cmd.String("repo"),
	}
	if lang := cmd.String("language"); lang != "" {
		args["language"] = lang
	}
	if scope := cmd.String("scope"); scope != "" {
		args["scope"] = scope
	}
	if limit := cmd.Int("limit"); limit > 0 {
		args["limit"] = limit
	}
	if cmd.Bool("full-file") {
		args["returnFullFile"] = true
	}

	return callDocsToolAndPrint(ctx, cmd, "code_search", args)
}

func docsChangelog(ctx context.Context, cmd *cli.Command) error {
	identifier := cmd.Args().First()
	if identifier == "" {
		return cli.ShowSubcommandHelp(cmd)
	}

	args := map[string]any{
		"identifier": identifier,
	}
	if n := cmd.Int("releases"); n > 0 {
		args["releasesToFetch"] = n
	}
	if s := cmd.Int("skip"); s > 0 {
		args["skip"] = s
	}

	return callDocsToolAndPrint(ctx, cmd, "get_changelog", args)
}

func docsListSDKs(ctx context.Context, cmd *cli.Command) error {
	return callDocsResourceAndPrint(ctx, cmd, "livekit://sdks")
}

func docsSubmitFeedback(ctx context.Context, cmd *cli.Command) error {
	feedback := cmd.String("feedback")
	if feedback == "" && cmd.Args().Len() > 0 {
		feedback = strings.Join(cmd.Args().Slice(), " ")
	}
	if feedback == "" {
		return cli.ShowSubcommandHelp(cmd)
	}

	args := map[string]any{
		"feedback": feedback,
	}
	if page := cmd.String("page"); page != "" {
		args["page"] = page
	}
	if agent := cmd.String("agent"); agent != "" {
		args["agent"] = agent
	}
	if model := cmd.String("model"); model != "" {
		args["model"] = model
	}

	return callDocsToolAndPrint(ctx, cmd, "submit_docs_feedback", args)
}

// ---------------------------------------------------------------------------
// Helpers for calling the MCP server and printing results
// ---------------------------------------------------------------------------

func callDocsToolAndPrint(ctx context.Context, cmd *cli.Command, tool string, args map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, docsRequestTimeout)
	defer cancel()

	session, err := initDocsSession(ctx, cmd)
	if err != nil {
		return err
	}
	defer session.Close()

	// Inject lightweight telemetry params for the docs MCP server.
	args["lk_cli_version"] = livekitcli.Version
	if id := tryLoadProjectID(cmd); id != "" {
		args["project_id"] = id
	}
	if cmd.Bool("json") {
		args["format"] = "json"
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		if isNotFoundErr(err) {
			return fmt.Errorf("%w\n\nhint: the docs server does not recognize the %q tool — try updating your lk CLI to the latest version", err, tool)
		}
		return err
	}
	if result.IsError {
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				return fmt.Errorf("tool error: %s", tc.Text)
			}
		}
		return fmt.Errorf("tool returned an error")
	}

	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			fmt.Println(tc.Text)
		}
	}
	return nil
}

func callDocsResourceAndPrint(ctx context.Context, cmd *cli.Command, uri string) error {
	ctx, cancel := context.WithTimeout(ctx, docsRequestTimeout)
	defer cancel()

	session, err := initDocsSession(ctx, cmd)
	if err != nil {
		return err
	}
	defer session.Close()

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: uri,
	})
	if err != nil {
		if isNotFoundErr(err) {
			return fmt.Errorf("%w\n\nhint: the docs server does not recognize the %q resource — try updating your lk CLI to the latest version", err, uri)
		}
		return err
	}

	for _, c := range result.Contents {
		if c.Text == "" {
			continue
		}
		text := c.Text
		// The server may return the text as a JSON-encoded string;
		// attempt to decode it so that newlines render properly.
		if strings.HasPrefix(text, "\"") {
			var decoded string
			if err := json.Unmarshal([]byte(text), &decoded); err == nil {
				text = decoded
			}
		}
		fmt.Println(text)
	}
	return nil
}

func initDocsSession(ctx context.Context, cmd *cli.Command) (*mcp.ClientSession, error) {
	endpoint := defaultDocsServerURL
	if u := cmd.String("server-url"); u != "" {
		endpoint = u
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
	}
	if v := cmd.String("vercel-header"); v != "" {
		transport.HTTPClient = &http.Client{
			Transport: &headerTransport{
				base:    http.DefaultTransport,
				headers: map[string]string{"x-vercel-protection-bypass": v},
			},
		}
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: "lk", Version: livekitcli.Version},
		nil,
	)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to the LiveKit docs server: %w", err)
	}

	checkServerVersion(session)
	return session, nil
}

// tryLoadProjectID attempts to resolve the current LiveKit Cloud project ID
// without producing any console output. It returns an empty string if no
// project is configured.
func tryLoadProjectID(cmd *cli.Command) string {
	// Explicit --project flag (inherited from root).
	if name := cmd.String("project"); name != "" {
		if pc, err := config.LoadProject(name); err == nil && pc.ProjectId != "" {
			return pc.ProjectId
		}
	}
	// Fall back to the default project.
	if pc, err := config.LoadDefaultProject(); err == nil && pc.ProjectId != "" {
		return pc.ProjectId
	}
	return ""
}

// headerTransport wraps an http.RoundTripper and injects extra headers.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// checkServerVersion prints a warning to stderr if the docs MCP server
// reports a newer major or minor version than what this CLI expects.
func checkServerVersion(session *mcp.ClientSession) {
	info := session.InitializeResult()
	if info == nil || info.ServerInfo == nil || info.ServerInfo.Version == "" {
		return
	}

	major, minor, ok := parseMajorMinor(info.ServerInfo.Version)
	if !ok {
		return
	}
	if major > expectedServerVersion[0] || (major == expectedServerVersion[0] && minor > expectedServerVersion[1]) {
		fmt.Fprintf(os.Stderr,
			"warning: the LiveKit docs server is version %s but this CLI was built for %d.%d.x — consider updating lk to the latest version\n\n",
			info.ServerInfo.Version, expectedServerVersion[0], expectedServerVersion[1],
		)
	}
}

// parseMajorMinor extracts the first two numeric components from a semver string.
func parseMajorMinor(version string) (major, minor int, ok bool) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

// isNotFoundErr returns true if the error indicates the server does not
// recognize the requested tool, resource, or method.
func isNotFoundErr(err error) bool {
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		return false
	}
	switch rpcErr.Code {
	case jsonrpc.CodeMethodNotFound: // -32601
		return true
	case mcp.CodeResourceNotFound: // -32002
		return true
	case jsonrpc.CodeInvalidParams: // -32602 — may indicate unknown tool
		lower := strings.ToLower(rpcErr.Message)
		return strings.Contains(lower, "not found") || strings.Contains(lower, "unknown")
	default:
		return false
	}
}
