package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
)

var privateLinkCommands = &cli.Command{
	Name:  "private-link",
	Usage: "Manage private links for agents",
	Commands: []*cli.Command{
		{
			Name:  "create",
			Usage: "Create a private link",
			Description: "Creates a private link to a customer endpoint.\n\n" +
				"Currently expects an AWS VPC Endpoint Service Name for --endpoint.\n" +
				"Example: com.amazonaws.vpce.us-east-1.vpce-svc-123123a1c43abc123",
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

func buildCreatePrivateLinkRequest(name, region string, port uint32, awsEndpoint string) *lkproto.CreatePrivateLinkRequest {
	return &lkproto.CreatePrivateLinkRequest{
		Name:   name,
		Region: region,
		Port:   port,
		Config: &lkproto.CreatePrivateLinkRequest_Aws{
			Aws: &lkproto.CreatePrivateLinkRequest_AWSCreateConfig{
				Endpoint: awsEndpoint,
			},
		},
	}
}

func privateLinkServiceDNS(name, projectID string) string {
	return fmt.Sprintf("%s-%s.plg.svc", name, projectID)
}

func buildPrivateLinkListRows(links []*lkproto.PrivateLink, healthByID map[string]*lkproto.PrivateLinkStatus, healthErrByID map[string]error) [][]string {
	var rows [][]string
	for _, link := range links {
		if link == nil {
			continue
		}

		status := lkproto.PrivateLinkStatus_PRIVATE_LINK_STATUS_UNKNOWN.String()
		updatedAt := "-"

		if err, ok := healthErrByID[link.PrivateLinkId]; ok && err != nil {
			status = "ERROR"
			updatedAt = err.Error()
		} else if health, ok := healthByID[link.PrivateLinkId]; ok && health != nil {
			status = health.Status.String()
			if health.UpdatedAt != nil {
				updatedAt = health.UpdatedAt.AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")
			}
		}

		rows = append(rows, []string{
			link.PrivateLinkId,
			link.Name,
			link.Region,
			strconv.FormatUint(uint64(link.Port), 10),
			status,
			updatedAt,
		})
	}
	return rows
}

func formatPrivateLinkClientError(action string, err error) error {
	if twerr, ok := err.(twirp.Error); ok {
		return fmt.Errorf("unable to %s private link: %s", action, twerr.Msg())
	}
	return fmt.Errorf("unable to %s private link: %w", action, err)
}

func createPrivateLink(ctx context.Context, cmd *cli.Command) error {
	req := buildCreatePrivateLinkRequest(cmd.String("name"), cmd.String("region"), uint32(cmd.Uint("port")), cmd.String("endpoint"))
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
	if project != nil && project.ProjectId != "" {
		fmt.Printf("Gateway DNS [%s]\n", util.Accented(privateLinkServiceDNS(req.Name, project.ProjectId)))
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
	table := util.CreateTable().Headers("ID", "Name", "Region", "Port", "Health", "Updated At").Rows(rows...)
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
	table := util.CreateTable().
		Headers("ID", "Health", "Updated At").
		Row(privateLinkID, resp.Value.Status.String(), updatedAt)
	fmt.Println(table)
	return nil
}
