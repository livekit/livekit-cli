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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"

	livekitcli "github.com/livekit/livekit-cli/v2"
)

// Participant actions: the catalog is published in the lk.actions attribute,
// and each action is served as the RPC method "action:<name>".
const (
	actionsAttribute   = "lk.actions"
	actionMethodPrefix = "action:"
	actionDeclinedCode = 1710
	actionConsentNone  = "none"
)

var MCPCommands = []*cli.Command{
	{
		Name:  "mcp",
		Usage: "Expose MCP servers to agents in a room",
		Commands: []*cli.Command{
			{
				Name:  "bridge",
				Usage: "Join a room and expose a local MCP server's tools as participant actions",
				UsageText: "lk mcp bridge --room ROOM [OPTIONS] -- COMMAND [ARGS...]\n" +
					"lk mcp bridge --room ROOM [OPTIONS] --http URL",
				Description: "Connects to the MCP server (a COMMAND over stdio, or a Streamable HTTP --http URL), " +
					"publishes its tools in the " + actionsAttribute + " attribute and serves each one as the RPC method " +
					actionMethodPrefix + "<tool>. By default only agent participants may call.",
				Action: mcpBridge,
				Flags: []cli.Flag{
					roomFlag,
					optional(identityFlag),
					&cli.StringFlag{
						Name:  "http",
						Usage: "Streamable HTTP `URL` of the MCP server, instead of COMMAND",
					},
					&cli.StringSliceFlag{
						Name:  "allow",
						Usage: "Participant `IDENTITY` allowed to call; repeatable. Replaces the agents-only default",
					},
				},
			},
		},
	},
}

type actionEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
	Consent     string `json:"consent"`
}

func mcpBridge(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	args := cmd.Args().Slice()
	endpoint := cmd.String("http")
	var transport mcp.Transport
	var serverName string
	switch {
	case endpoint != "" && len(args) > 0:
		return errors.New("pass either --http or COMMAND, not both")
	case endpoint != "":
		u, err := url.Parse(endpoint)
		if err != nil {
			return fmt.Errorf("invalid --http: %w", err)
		}
		serverName = u.Hostname()
		transport = &mcp.StreamableClientTransport{Endpoint: endpoint}
	case len(args) > 0:
		serverName = filepath.Base(args[0])
		c := exec.Command(args[0], args[1:]...)
		c.Stderr = os.Stderr
		transport = &mcp.CommandTransport{Command: c}
	default:
		return errors.New("an MCP server COMMAND or --http is required")
	}

	identity := cmd.String("identity")
	if identity == "" {
		identity = "mcp-" + serverName
	}

	b := &mcpBridgeState{allow: map[string]bool{}, served: map[string]bool{}}
	for _, id := range cmd.StringSlice("allow") {
		b.allow[id] = true
	}

	// Attributes are republished whenever the tool list changes.
	canUpdateOwnMetadata := true
	at := auth.NewAccessToken(pc.APIKey, pc.APISecret).
		SetIdentity(identity).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:             true,
			Room:                 cmd.String("room"),
			CanUpdateOwnMetadata: &canUpdateOwnMetadata,
		})
	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	disconnected := make(chan struct{})
	b.room = lksdk.NewRoom(&lksdk.RoomCallback{
		OnDisconnected: func() { close(disconnected) },
	})
	if err := b.room.JoinWithToken(pc.URL, token); err != nil {
		return err
	}
	defer b.room.Disconnect()

	client := mcp.NewClient(&mcp.Implementation{Name: "lk-mcp-bridge", Version: livekitcli.Version}, &mcp.ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *mcp.ToolListChangedRequest) {
			if err := b.syncTools(ctx, req.Session); err != nil {
				logger.Warnw("failed to refresh MCP tools", err)
			}
		},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connecting to MCP server: %w", err)
	}
	defer session.Close()

	if err := b.syncTools(ctx, session); err != nil {
		return err
	}
	logger.Infow("bridging MCP server", "room", b.room.Name(), "identity", identity,
		"server", session.InitializeResult().ServerInfo.Name)

	sessionDone := make(chan struct{})
	go func() {
		_ = session.Wait()
		close(sessionDone)
	}()

	select {
	case <-ctx.Done():
		return nil
	case <-disconnected:
		return nil
	case <-sessionDone:
		return errors.New("MCP server exited")
	}
}

type mcpBridgeState struct {
	room  *lksdk.Room
	allow map[string]bool

	mu     sync.Mutex
	served map[string]bool
}

// syncTools publishes the server's current tools and registers an RPC method for each.
func (b *mcpBridgeState) syncTools(ctx context.Context, session *mcp.ClientSession) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var entries []actionEntry
	current := map[string]bool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("listing MCP tools: %w", err)
		}
		entries = append(entries, actionEntry{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
			Consent:     actionConsentNone,
		})
		current[tool.Name] = true
		if !b.served[tool.Name] {
			if err := b.room.RegisterRpcCtxMethod(actionMethodPrefix+tool.Name, b.handler(session, tool.Name)); err != nil {
				return err
			}
		}
	}
	for name := range b.served {
		if !current[name] {
			b.room.UnregisterRpcMethod(actionMethodPrefix + name)
		}
	}
	b.served = current

	catalog, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	b.room.LocalParticipant.SetAttributes(map[string]string{actionsAttribute: string(catalog)})
	logger.Infow("published actions", "count", len(entries), "bytes", len(catalog))
	return nil
}

func (b *mcpBridgeState) handler(session *mcp.ClientSession, tool string) lksdk.RpcHandlerCtxFunc {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		caller := ""
		if meta := lksdk.RPCMetadataFromContext(ctx); meta != nil {
			caller = meta.CallerIdentity
		}
		if !b.allowed(caller) {
			logger.Warnw("declining action from unauthorized participant", nil, "caller", caller, "action", tool)
			return nil, lksdk.NewRpcError(actionDeclinedCode, "caller is not allowed to use this action", nil)
		}

		var args map[string]any
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &args); err != nil {
				return nil, lksdk.NewRpcError(lksdk.RpcApplicationError, "arguments must be a JSON object", nil)
			}
		}
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return nil, lksdk.NewRpcError(lksdk.RpcApplicationError, err.Error(), nil)
		}
		if res.IsError {
			return nil, lksdk.NewRpcError(lksdk.RpcApplicationError, toolResultText(res), nil)
		}
		return json.Marshal(toolResultValue(res))
	}
}

// A caller's first call can arrive over the data channel before its join
// reaches us over signalling, so wait briefly for an unknown identity.
func (b *mcpBridgeState) allowed(identity string) bool {
	if len(b.allow) > 0 {
		return b.allow[identity]
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if p := b.room.GetParticipantByIdentity(identity); p != nil {
			return p.Kind() == lksdk.ParticipantAgent
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// toolResultValue is the action result: structured content when the tool
// provides it, else its text, else the raw content blocks (images, resources).
func toolResultValue(res *mcp.CallToolResult) any {
	if res.StructuredContent != nil {
		return res.StructuredContent
	}
	allText := true
	for _, c := range res.Content {
		if _, ok := c.(*mcp.TextContent); !ok {
			allText = false
		}
	}
	if allText {
		return toolResultText(res)
	}
	return res.Content
}

func toolResultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}
