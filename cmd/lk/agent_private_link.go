package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/proto"
)

var privateLinkCommands = &cli.Command{
	Name:  "private-link",
	Usage: "Manage private links for agents",
	Commands: []*cli.Command{
		{
			Name:  "create",
			Usage: "Create a private link",
			Description: "Creates a private link to a customer endpoint.\n\n" +
				"Supports Azure Private Link Service aliases and Azure Resource IDs for --endpoint.\n" +
				"Azure alias example: my-pls.12345678-abcd-1234-abcd-1234567890ab.eastus.azure.privatelinkservice\n" +
				"Azure Resource ID example: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/privateLinkServices/{name}\n" +
				"When using an Azure Resource ID, --cloud-region is required.",
			Before: createAgentClient,
			Action: createPrivateLink,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "name",
					Usage:    "Private link name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "region",
					Usage:    "LiveKit region",
					Required: true,
				},
				&cli.UintFlag{
					Name:     "port",
					Usage:    "Destination port",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "endpoint",
					Usage:    "Customer-provided endpoint identifier",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "cloud-region",
					Usage: "Cloud provider region (e.g. eastus, us-east-2). Required when --endpoint is an Azure Resource ID",
				},
				jsonFlag,
			},
		},
		{
			Name:   "list",
			Usage:  "List private links with health",
			Before: createAgentClient,
			Action: listPrivateLinks,
			Flags: []cli.Flag{
				jsonFlag,
			},
		},
		{
			Name:   "delete",
			Usage:  "Delete a private link",
			Before: createAgentClient,
			Action: deletePrivateLink,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Private link ID",
					Required: true,
				},
				jsonFlag,
			},
		},
		{
			Name:   "health-status",
			Usage:  "Get private link health status",
			Before: createAgentClient,
			Action: getPrivateLinkHealthStatus,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "Private link ID",
					Required: true,
				},
				jsonFlag,
			},
		},
	},
}

func buildCreatePrivateLinkRequest(name, region string, port uint32, endpoint, cloudRegion string) *lkproto.CreatePrivateLinkRequest {
	req := &lkproto.CreatePrivateLinkRequest{
		Name:     name,
		Region:   region,
		Port:     port,
		Endpoint: endpoint,
	}

	if cloudRegion != "" {
		req.CloudRegion = proto.String(cloudRegion)
	}

	return req
}

func buildPrivateLinkListRows(links []*lkproto.PrivateLink, healthByID map[string]*lkproto.PrivateLinkStatus, healthErrByID map[string]error) [][]string {
	var rows [][]string
	for _, link := range links {
		if link == nil {
			continue
		}

		status := formatPrivateLinkHealthStatus(lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_UNKNOWN)
		updatedAt := "-"
		reason := "-"

		if err, ok := healthErrByID[link.PrivateLinkId]; ok && err != nil {
			status = "Error"
			reason = err.Error()
		} else if health, ok := healthByID[link.PrivateLinkId]; ok && health != nil {
			status = formatPrivateLinkHealthStatus(health.Status)
			if health.UpdatedAt != nil {
				updatedAt = health.UpdatedAt.AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")
			}
			if health.Reason != "" {
				reason = health.Reason
			}
		}
		endpoint := link.Endpoint
		if endpoint == "" {
			endpoint = "-"
		}
		dns := link.ConnectionEndpoint
		if dns == "" {
			dns = "-"
		}

		rows = append(rows, []string{
			link.PrivateLinkId,
			link.Name,
			link.Region,
			strconv.FormatUint(uint64(link.Port), 10),
			endpoint,
			dns,
			status,
			updatedAt,
			reason,
		})
	}
	return rows
}

func formatPrivateLinkHealthStatus(status lkproto.PrivateLinkStatus_Status) string {
	switch status {
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_PROVISIONING:
		return "Provisioning"
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_PENDING_APPROVAL:
		return "Pending Approval"
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_APPROVED:
		return "Approved"
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_HEALTHY:
		return "Healthy"
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_UNHEALTHY:
		return "Unhealthy"
	case lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_UNKNOWN:
		return "Unknown"
	default:
		return status.String()
	}
}

func formatPrivateLinkClientError(action string, err error) error {
	if twerr, ok := err.(twirp.Error); ok {
		return fmt.Errorf("unable to %s private link: %s", action, twerr.Msg())
	}
	return fmt.Errorf("unable to %s private link: %w", action, err)
}

func createPrivateLink(ctx context.Context, cmd *cli.Command) error {
	req := buildCreatePrivateLinkRequest(cmd.String("name"), cmd.String("region"), uint32(cmd.Uint("port")), cmd.String("endpoint"), cmd.String("cloud-region"))
	resp, err := agentsClient.CreatePrivateLink(ctx, req)
	if err != nil {
		return formatPrivateLinkClientError("create", err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	if resp.PrivateLink == nil {
		fmt.Println("Private link created")
		return nil
	}

	fmt.Printf("Created private link [%s]\n", util.Accented(resp.PrivateLink.PrivateLinkId))
	if resp.PrivateLink.Endpoint != "" {
		fmt.Printf("Endpoint [%s]\n", util.Accented(resp.PrivateLink.Endpoint))
	}
	if resp.PrivateLink.ConnectionEndpoint != "" {
		fmt.Printf("Gateway DNS [%s]\n", util.Accented(resp.PrivateLink.ConnectionEndpoint))
	}
	return nil
}

func listPrivateLinks(ctx context.Context, cmd *cli.Command) error {
	resp, err := agentsClient.ListPrivateLinks(ctx, &lkproto.ListPrivateLinksRequest{})
	if err != nil {
		return formatPrivateLinkClientError("list", err)
	}

	healthByID := make(map[string]*lkproto.PrivateLinkStatus, len(resp.Items))
	healthErrByID := make(map[string]error)
	for _, link := range resp.Items {
		if link == nil || link.PrivateLinkId == "" {
			continue
		}
		health, healthErr := agentsClient.GetPrivateLinkStatus(ctx, &lkproto.GetPrivateLinkStatusRequest{
			PrivateLinkId: link.PrivateLinkId,
		})
		if healthErr != nil {
			healthErrByID[link.PrivateLinkId] = healthErr
			continue
		}
		if health != nil {
			healthByID[link.PrivateLinkId] = health.Value
		}
	}

	if cmd.Bool("json") {
		type privateLinkWithHealth struct {
			PrivateLink *lkproto.PrivateLink       `json:"private_link"`
			Status      *lkproto.PrivateLinkStatus `json:"health"`
			HealthError string                     `json:"health_error,omitempty"`
		}
		items := make([]privateLinkWithHealth, 0, len(resp.Items))
		for _, link := range resp.Items {
			if link == nil {
				continue
			}
			entry := privateLinkWithHealth{
				PrivateLink: link,
				Status:      healthByID[link.PrivateLinkId],
			}
			if err := healthErrByID[link.PrivateLinkId]; err != nil {
				entry.HealthError = err.Error()
			}
			items = append(items, entry)
		}
		util.PrintJSON(map[string]any{"items": items})
		return nil
	}

	if len(resp.Items) == 0 {
		fmt.Println("No private links found")
		return nil
	}

	rows := buildPrivateLinkListRows(resp.Items, healthByID, healthErrByID)
	table := util.CreateTable().Headers("ID", "Name", "Region", "Port", "Endpoint", "DNS", "Health", "Updated At", "Reason").Rows(rows...)
	fmt.Println(table)
	return nil
}

func deletePrivateLink(ctx context.Context, cmd *cli.Command) error {
	privateLinkID := cmd.String("id")
	resp, err := agentsClient.DestroyPrivateLink(ctx, &lkproto.DestroyPrivateLinkRequest{
		PrivateLinkId: privateLinkID,
	})
	if err != nil {
		return formatPrivateLinkClientError("delete", err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}
	fmt.Printf("Deleted private link [%s]\n", util.Accented(privateLinkID))
	return nil
}

func getPrivateLinkHealthStatus(ctx context.Context, cmd *cli.Command) error {
	privateLinkID := cmd.String("id")
	resp, err := agentsClient.GetPrivateLinkStatus(ctx, &lkproto.GetPrivateLinkStatusRequest{
		PrivateLinkId: privateLinkID,
	})
	if err != nil {
		return formatPrivateLinkClientError("get health status for", err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}
	if resp == nil || resp.Value == nil {
		return fmt.Errorf("health status unavailable for private link [%s]", privateLinkID)
	}
	updatedAt := "-"
	if resp.Value.UpdatedAt != nil {
		updatedAt = resp.Value.UpdatedAt.AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	reason := "-"
	if resp.Value.Reason != "" {
		reason = resp.Value.Reason
	}
	table := util.CreateTable().
		Headers("ID", "Health", "Updated At", "Reason").
		Row(privateLinkID, formatPrivateLinkHealthStatus(resp.Value.Status), updatedAt, reason)
	fmt.Println(table)
	return nil
}
