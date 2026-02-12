package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func findCommandByName(commands []*cli.Command, name string) *cli.Command {
	for _, cmd := range commands {
		if cmd != nil && cmd.Name == name {
			return cmd
		}
	}
	return nil
}

func TestAgentPrivateLinkCommandTree(t *testing.T) {
	agentCmd := findCommandByName(AgentCommands, "agent")
	require.NotNil(t, agentCmd, "top-level 'agent' command must exist")

	privateLinkCmd := findCommandByName(agentCmd.Commands, "private-link")
	require.NotNil(t, privateLinkCmd, "'agent private-link' command must exist")

	createCmd := findCommandByName(privateLinkCmd.Commands, "create")
	require.NotNil(t, createCmd, "'agent private-link create' command must exist")
	require.NotNil(t, createCmd.Action, "'agent private-link create' must have an action")

	listCmd := findCommandByName(privateLinkCmd.Commands, "list")
	require.NotNil(t, listCmd, "'agent private-link list' command must exist")
	require.NotNil(t, listCmd.Action, "'agent private-link list' must have an action")

	deleteCmd := findCommandByName(privateLinkCmd.Commands, "delete")
	require.NotNil(t, deleteCmd, "'agent private-link delete' command must exist")
	require.NotNil(t, deleteCmd.Action, "'agent private-link delete' must have an action")

	healthStatusCmd := findCommandByName(privateLinkCmd.Commands, "health-status")
	require.NotNil(t, healthStatusCmd, "'agent private-link health-status' command must exist")
	require.NotNil(t, healthStatusCmd.Action, "'agent private-link health-status' must have an action")
}

func TestBuildCreatePrivateLinkRequest_HappyPath(t *testing.T) {
	req := buildCreatePrivateLinkRequest("orders-db", "us-east-1", 6379, "com.amazonaws.vpce.us-east-1.vpce-svc-abc123")
	require.NotNil(t, req)

	assert.Equal(t, "orders-db", req.Name)
	assert.Equal(t, "us-east-1", req.Region)
	assert.Equal(t, uint32(6379), req.Port)

	aws := req.GetAws()
	require.NotNil(t, aws)
	assert.Equal(t, "com.amazonaws.vpce.us-east-1.vpce-svc-abc123", aws.Endpoint)
}

func TestPrivateLinkServiceDNS(t *testing.T) {
	assert.Equal(t, "orders-db-prj_123.plg.svc", privateLinkServiceDNS("orders-db", "prj_123"))
}

func TestBuildPrivateLinkListRows_EmptyList(t *testing.T) {
	rows := buildPrivateLinkListRows([]*lkproto.PrivateLink{}, map[string]*lkproto.PrivateLinkHealthStatus{}, map[string]error{})
	assert.Empty(t, rows)
}

func TestBuildPrivateLinkListRows_OnePrivateLink(t *testing.T) {
	links := []*lkproto.PrivateLink{
		{
			PrivateLinkId: "pl-1",
			Name:          "orders-db",
			Region:        "us-east-1",
			Port:          6379,
		},
	}

	now := time.Now().UTC()
	healthByID := map[string]*lkproto.PrivateLinkHealthStatus{
		"pl-1": {
			Status:    lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_HEALTHY,
			UpdatedAt: timestamppb.New(now),
		},
	}

	rows := buildPrivateLinkListRows(links, healthByID, map[string]error{})
	require.Len(t, rows, 1)
	assert.Equal(t, "pl-1", rows[0][0])
	assert.Equal(t, "orders-db", rows[0][1])
	assert.Equal(t, "us-east-1", rows[0][2])
	assert.Equal(t, "6379", rows[0][3])
	assert.Equal(t, lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_HEALTHY.String(), rows[0][4])
}

func TestBuildPrivateLinkListRows_TwoPrivateLinksDifferentRegions(t *testing.T) {
	links := []*lkproto.PrivateLink{
		{
			PrivateLinkId: "pl-1",
			Name:          "orders-db",
			Region:        "us-east-1",
			Port:          6379,
		},
		{
			PrivateLinkId: "pl-2",
			Name:          "cache",
			Region:        "eu-west-1",
			Port:          6380,
		},
	}

	healthByID := map[string]*lkproto.PrivateLinkHealthStatus{
		"pl-1": {
			Status: lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_HEALTHY,
		},
		"pl-2": {
			Status: lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_UNHEALTHY,
		},
	}

	rows := buildPrivateLinkListRows(links, healthByID, map[string]error{})
	require.Len(t, rows, 2)

	assert.Equal(t, "us-east-1", rows[0][2])
	assert.Equal(t, "eu-west-1", rows[1][2])
	assert.Equal(t, lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_HEALTHY.String(), rows[0][4])
	assert.Equal(t, lkproto.PrivateLinkHealthStatus_PRIVATE_LINK_ATTACHMENT_HEALTH_STATUS_UNHEALTHY.String(), rows[1][4])
}


