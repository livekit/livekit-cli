package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
)

// privateLinksClient is set, with agentsClient, by createAgentClient.
var privateLinksClient privateLinkAPI

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
					Usage:    "`NAME` for the private link.",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "region",
					Usage: "LiveKit agent `REGION` for the link. This is the LiveKit region, not the cloud provider region. If unset in interactive mode, the CLI shows a picker of available regions.",
				},
				&cli.UintFlag{
					Name:     "port",
					Usage:    "TCP `PORT` to reach on the target service. Must match the port your endpoint service listens on.",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "endpoint",
					Usage:    "The `ENDPOINT` to connect to: the AWS endpoint service name, the Azure Private Link Service alias, or the Azure Resource ID.",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "cloud-region",
					Usage: "Cloud provider `REGION` that hosts the resource. Required when --endpoint is an Azure Resource ID. For AWS, LiveKit derives the region from the endpoint service name, so you can omit it.",
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
					Usage:    "Private link `ID`.",
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
					Usage:    "Private link `ID`.",
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
		req.CloudRegion = new(cloudRegion)
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
		cloudRegion := link.CloudRegion
		if cloudRegion == "" {
			cloudRegion = "-"
		}

		rows = append(rows, []string{
			link.PrivateLinkId,
			link.Name,
			link.Region,
			cloudRegion,
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
	settingsMap, err := getClientSettings(ctx)
	if err != nil {
		return err
	}

	region, err := resolveRegion(cmd, settingsMap, "Select region for private link")
	if err != nil {
		return err
	}

	req := buildCreatePrivateLinkRequest(cmd.String("name"), region, uint32(cmd.Uint("port")), cmd.String("endpoint"), cmd.String("cloud-region"))
	resp, err := privateLinksClient.CreatePrivateLink(ctx, req)
	if err != nil {
		return formatPrivateLinkClientError("create", err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	if resp.PrivateLink == nil {
		out.Status("Private link created")
		return nil
	}

	out.Statusf("Created private link [%s]", util.Accented(resp.PrivateLink.PrivateLinkId))
	if resp.PrivateLink.Endpoint != "" {
		out.Statusf("Endpoint [%s]", util.Accented(resp.PrivateLink.Endpoint))
	}
	if resp.PrivateLink.ConnectionEndpoint != "" {
		out.Statusf("Gateway DNS [%s]", util.Accented(resp.PrivateLink.ConnectionEndpoint))
	}
	if resp.PrivateLink.CloudRegion != "" {
		out.Statusf("Cloud Region [%s]", util.Accented(resp.PrivateLink.CloudRegion))
	}
	return nil
}

// errHealthUnread is the health error of a link that the Public API lists
// without its health.
var errHealthUnread = errors.New("LiveKit could not read its health")

// privateLinksWithHealth lists the private links, with the health of each,
// or the error that reading it returned. A privateLinkHealthLister lists
// them with their health in one call; with any other privateLinkAPI, lk
// reads the health of each link with its own call.
func privateLinksWithHealth(ctx context.Context, api privateLinkAPI) ([]*lkproto.PrivateLink, map[string]*lkproto.PrivateLinkStatus, map[string]error, error) {
	healthByID := make(map[string]*lkproto.PrivateLinkStatus)
	healthErrByID := make(map[string]error)
	if lister, ok := api.(privateLinkHealthLister); ok {
		listed, err := lister.ListPrivateLinksWithHealth(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		links := make([]*lkproto.PrivateLink, 0, len(listed))
		for _, l := range listed {
			links = append(links, l.Link)
			if l.Status != nil {
				healthByID[l.Link.GetPrivateLinkId()] = l.Status
			} else {
				healthErrByID[l.Link.GetPrivateLinkId()] = errHealthUnread
			}
		}
		return links, healthByID, healthErrByID, nil
	}

	resp, err := api.ListPrivateLinks(ctx, &lkproto.ListPrivateLinksRequest{})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, link := range resp.Items {
		if link == nil || link.PrivateLinkId == "" {
			continue
		}
		health, healthErr := api.GetPrivateLinkStatus(ctx, &lkproto.GetPrivateLinkStatusRequest{
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
	return resp.Items, healthByID, healthErrByID, nil
}

func listPrivateLinks(ctx context.Context, cmd *cli.Command) error {
	links, healthByID, healthErrByID, err := privateLinksWithHealth(ctx, privateLinksClient)
	if err != nil {
		return formatPrivateLinkClientError("list", err)
	}

	if cmd.Bool("json") {
		type privateLinkWithHealth struct {
			PrivateLink *lkproto.PrivateLink       `json:"private_link"`
			Status      *lkproto.PrivateLinkStatus `json:"health"`
			HealthError string                     `json:"health_error,omitempty"`
		}
		items := make([]privateLinkWithHealth, 0, len(links))
		for _, link := range links {
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

	if len(links) == 0 {
		out.Status("No private links found")
		return nil
	}

	rows := buildPrivateLinkListRows(links, healthByID, healthErrByID)
	table := util.CreateTable().Headers("ID", "Name", "Region", "Cloud Region", "Port", "Endpoint", "DNS", "Health", "Updated At", "Reason").Rows(rows...)
	out.Result(table)
	return nil
}

func deletePrivateLink(ctx context.Context, cmd *cli.Command) error {
	privateLinkID := cmd.String("id")
	resp, err := privateLinksClient.DestroyPrivateLink(ctx, &lkproto.DestroyPrivateLinkRequest{
		PrivateLinkId: privateLinkID,
	})
	if err != nil {
		return formatPrivateLinkClientError("delete", err)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}
	out.Statusf("Deleted private link [%s]", util.Accented(privateLinkID))
	return nil
}

func getPrivateLinkHealthStatus(ctx context.Context, cmd *cli.Command) error {
	privateLinkID := cmd.String("id")
	resp, err := privateLinksClient.GetPrivateLinkStatus(ctx, &lkproto.GetPrivateLinkStatusRequest{
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
	out.Result(table)
	return nil
}
