// Copyright 2024 LiveKit, Inc.
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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/urfave/cli/v3"

	livekitcli "github.com/livekit/livekit-cli/v2"
)

const defaultDocsServerURL = "https://docs.livekit.io/mcp/"

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
							Usage: "Results per page (1-50)",
							Value: 10,
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
	return callDocsToolAndPrint(ctx, "get_docs_overview", map[string]any{})
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

	return callDocsToolAndPrint(ctx, "docs_search", args)
}

func docsGetPage(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return cli.ShowSubcommandHelp(cmd)
	}

	return callDocsToolAndPrint(ctx, "get_pages", map[string]any{
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

	return callDocsToolAndPrint(ctx, "code_search", args)
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

	return callDocsToolAndPrint(ctx, "get_changelog", args)
}

func docsListSDKs(ctx context.Context, cmd *cli.Command) error {
	return callDocsResourceAndPrint(ctx, "livekit://sdks")
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

	return callDocsToolAndPrint(ctx, "submit_docs_feedback", args)
}

// ---------------------------------------------------------------------------
// Helpers for calling the MCP server and printing results
// ---------------------------------------------------------------------------

func callDocsToolAndPrint(ctx context.Context, tool string, args map[string]any) error {
	client, err := initDocsClient(ctx)
	if err != nil {
		return err
	}

	result, err := client.callTool(ctx, tool, args)
	if err != nil {
		if isNotFoundErr(err) {
			return fmt.Errorf("%w\n\nhint: the docs server does not recognize the %q tool — try updating your lk CLI to the latest version", err, tool)
		}
		return err
	}

	for _, c := range result.Content {
		if c.Type == "text" {
			fmt.Println(c.Text)
		}
	}
	return nil
}

func callDocsResourceAndPrint(ctx context.Context, uri string) error {
	client, err := initDocsClient(ctx)
	if err != nil {
		return err
	}

	result, err := client.readResource(ctx, uri)
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

func initDocsClient(ctx context.Context) (*mcpClient, error) {
	client := newMCPClient(defaultDocsServerURL)
	if err := client.initialize(ctx); err != nil {
		return nil, fmt.Errorf("could not connect to the LiveKit docs server: %w", err)
	}
	return client, nil
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

// mcpResponseError represents a JSON-RPC error response from the MCP server.
type mcpResponseError struct {
	Code    int
	Message string
}

func (e *mcpResponseError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// isNotFoundErr returns true if the error indicates the server does not
// recognize the requested tool, resource, or method.
func isNotFoundErr(err error) bool {
	var rpcErr *mcpResponseError
	if !errors.As(err, &rpcErr) {
		return false
	}
	switch rpcErr.Code {
	case -32601: // Method not found
		return true
	case -32602: // Invalid params — may indicate unknown tool
		lower := strings.ToLower(rpcErr.Message)
		return strings.Contains(lower, "not found") || strings.Contains(lower, "unknown")
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Minimal MCP streamable HTTP client
// ---------------------------------------------------------------------------

type mcpClient struct {
	endpoint   string
	sessionID  string
	httpClient *http.Client
	nextID     atomic.Int64
}

func newMCPClient(endpoint string) *mcpClient {
	return &mcpClient{
		endpoint:   endpoint,
		httpClient: &http.Client{},
	}
}

func (c *mcpClient) initialize(ctx context.Context) error {
	_, err := c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]string{
			"name":    "lk",
			"version": livekitcli.Version,
		},
		"capabilities": map[string]any{},
	})
	if err != nil {
		return err
	}

	return c.sendNotification(ctx, "notifications/initialized")
}

// -- Tool calling ----------------------------------------------------------

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *mcpClient) callTool(ctx context.Context, name string, args map[string]any) (*mcpToolResult, error) {
	raw, err := c.sendRequest(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}

	var result mcpToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}
	if result.IsError {
		var msg string
		for _, c := range result.Content {
			if c.Type == "text" {
				msg = c.Text
				break
			}
		}
		return nil, fmt.Errorf("tool error: %s", msg)
	}
	return &result, nil
}

// -- Resource reading ------------------------------------------------------

type mcpResourceResult struct {
	Contents []mcpResourceContent `json:"contents"`
}

type mcpResourceContent struct {
	URI      string `json:"uri"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

func (c *mcpClient) readResource(ctx context.Context, uri string) (*mcpResourceResult, error) {
	raw, err := c.sendRequest(ctx, "resources/read", map[string]any{
		"uri": uri,
	})
	if err != nil {
		return nil, err
	}

	var result mcpResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resource result: %w", err)
	}
	return &result, nil
}

// -- Transport -------------------------------------------------------------

// sendNotification sends a JSON-RPC notification (no ID, no response expected).
func (c *mcpClient) sendNotification(ctx context.Context, method string) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("MCP notification failed with status %d", resp.StatusCode)
	}
	return nil
}

// sendRequest sends a JSON-RPC request and returns the result field.
func (c *mcpClient) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return c.readSSE(resp.Body)
	}

	return c.readJSON(resp.Body)
}

func (c *mcpClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *mcpClient) readJSON(r io.Reader) (json.RawMessage, error) {
	var resp jsonRPCResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode MCP response: %w", err)
	}
	if resp.Error != nil {
		return nil, &mcpResponseError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return resp.Result, nil
}

func (c *mcpClient) readSSE(r io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // up to 10 MB

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, line[6:])
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, line[5:])
		case line == "" && len(dataLines) > 0:
			data := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]

			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				continue
			}
			// Skip notifications (no ID)
			if resp.ID == nil {
				continue
			}
			if resp.Error != nil {
				return nil, &mcpResponseError{Code: resp.Error.Code, Message: resp.Error.Message}
			}
			return resp.Result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading MCP stream: %w", err)
	}

	return nil, fmt.Errorf("no response received from MCP server")
}
