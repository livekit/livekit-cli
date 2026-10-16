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
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/public/render"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	webhookNameFlag = &cli.StringFlag{Name: "name", Usage: "Human-readable `NAME` for the webhook"}
	webhookURLFlag  = &cli.StringFlag{Name: "url", Usage: "http(s) `URL` deliveries are POSTed to"}
	signingKeyFlag  = &cli.StringFlag{
		Name:  "signing-key",
		Usage: "`API_KEY` of this project whose secret signs deliveries",
	}
	includeEventFlag = &cli.StringSliceFlag{
		Name:  "include-event",
		Usage: "Only deliver `EVENT` (repeatable; see `lk project webhook events`)",
	}
	excludeEventFlag = &cli.StringSliceFlag{
		Name:  "exclude-event",
		Usage: "Never deliver `EVENT` (repeatable; see `lk project webhook events`)",
	}
)

// init appends the Public-API-only webhook subcommands to `lk project`. Like
// key/member/invite, they are Hidden and require --experimental-auth; the
// project comes from the global --project flag (or a cached alias).
func init() {
	webhookCommand := &cli.Command{
		Name:   "webhook",
		Usage:  "Manage webhooks of a LiveKit Cloud project (requires --experimental-auth)",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List project webhooks",
				UsageText: "lk project webhook list --project PROJECT --experimental-auth",
				Action:    listProjectWebhooks,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "create",
				Usage:     "Add a webhook to a project",
				UsageText: "lk project webhook create URL [--name NAME] [--signing-key API_KEY] [--include-event EVENT...] [--exclude-event EVENT...] --project PROJECT --experimental-auth",
				ArgsUsage: "URL",
				Action:    createProjectWebhook,
				Flags:     []cli.Flag{webhookNameFlag, signingKeyFlag, includeEventFlag, excludeEventFlag, jsonFlag},
			},
			{
				Name:      "update",
				Usage:     "Change a webhook's name, URL, signing key or event filter",
				UsageText: "lk project webhook update WEBHOOK_ID [--name NAME] [--url URL] [--signing-key API_KEY] [--include-event EVENT...] [--exclude-event EVENT...] [--all-events] --project PROJECT --experimental-auth",
				ArgsUsage: "WEBHOOK_ID",
				Action:    updateProjectWebhook,
				Flags: []cli.Flag{
					webhookNameFlag, webhookURLFlag, signingKeyFlag, includeEventFlag, excludeEventFlag,
					&cli.BoolFlag{Name: "all-events", Usage: "Clear the event filter so every event is delivered"},
					jsonFlag,
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove a webhook from a project",
				UsageText: "lk project webhook delete WEBHOOK_ID --project PROJECT --experimental-auth",
				ArgsUsage: "WEBHOOK_ID",
				Action:    deleteProjectWebhook,
				Flags:     []cli.Flag{jsonFlag},
			},
			{
				Name:      "test",
				Usage:     "Send a test delivery of an event to a webhook",
				UsageText: "lk project webhook test WEBHOOK_ID --event EVENT --project PROJECT --experimental-auth",
				ArgsUsage: "WEBHOOK_ID",
				Action:    testProjectWebhook,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "event",
						Usage:    "`EVENT` to send with sample data, regardless of the webhook's filter (see `lk project webhook events`)",
						Required: true,
					},
					jsonFlag,
				},
			},
			{
				Name:      "events",
				Usage:     "List the events a project's webhooks can deliver",
				UsageText: "lk project webhook events --project PROJECT --experimental-auth",
				Action:    listProjectWebhookEvents,
				Flags:     []cli.Flag{jsonFlag},
			},
		},
	}

	// ProjectCommands[0] is the `project` command.
	ProjectCommands[0].Commands = append(ProjectCommands[0].Commands, webhookCommand)
}

// webhookFilter builds the event filter from --include-event/--exclude-event,
// or nil when neither was given.
func webhookFilter(cmd *cli.Command) *oapi.LivekitFilterParams {
	include, exclude := cmd.StringSlice("include-event"), cmd.StringSlice("exclude-event")
	if len(include) == 0 && len(exclude) == 0 {
		return nil
	}
	fp := &oapi.LivekitFilterParams{}
	if len(include) > 0 {
		fp.IncludeEvents = &include
	}
	if len(exclude) > 0 {
		fp.ExcludeEvents = &exclude
	}
	return fp
}

// optionalString returns the flag's value when it was set, else nil, so an
// update leaves the field unchanged.
func optionalString(cmd *cli.Command, name string) *string {
	if !cmd.IsSet(name) {
		return nil
	}
	v := cmd.String(name)
	return &v
}

func listProjectWebhooks(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	webhooks, err := client.ListWebhooks(ctx, projectID)
	if err != nil {
		return cloudAPIError(err)
	}
	return render.Webhooks(out, cmd.Bool("json"), webhooks)
}

func createProjectWebhook(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	url, err := requireArg(cmd, "URL")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	webhook, err := client.CreateWebhook(ctx, projectID, oapi.ProjectServiceCreateWebhookJSONRequestBody{
		Url:          &url,
		Name:         optionalString(cmd, "name"),
		SigningKey:   optionalString(cmd, "signing-key"),
		FilterParams: webhookFilter(cmd),
	})
	if err != nil {
		return cloudAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Created webhook %s", util.Accented(util.Deref(webhook.Id)))
	}
	return render.Webhook(out, cmd.Bool("json"), *webhook)
}

func updateProjectWebhook(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	webhookID, err := requireArg(cmd, "webhook ID")
	if err != nil {
		return err
	}
	body := oapi.ProjectServiceUpdateWebhookJSONRequestBody{
		Name:         optionalString(cmd, "name"),
		Url:          optionalString(cmd, "url"),
		SigningKey:   optionalString(cmd, "signing-key"),
		FilterParams: webhookFilter(cmd),
	}
	if cmd.Bool("all-events") {
		if body.FilterParams != nil {
			return errors.New("--all-events cannot be combined with --include-event or --exclude-event")
		}
		// An empty (but present) filter replaces the old one with "deliver everything".
		body.FilterParams = &oapi.LivekitFilterParams{}
	}
	if body.Name == nil && body.Url == nil && body.SigningKey == nil && body.FilterParams == nil {
		_ = cli.ShowSubcommandHelp(cmd)
		return errors.New("nothing to update: pass --name, --url, --signing-key, --include-event, --exclude-event or --all-events")
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	webhook, err := client.UpdateWebhook(ctx, projectID, webhookID, body)
	if err != nil {
		return cloudAPIError(err)
	}
	if !cmd.Bool("json") {
		out.Statusf("Updated webhook %s", util.Accented(webhookID))
	}
	return render.Webhook(out, cmd.Bool("json"), *webhook)
}

func deleteProjectWebhook(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	webhookID, err := requireArg(cmd, "webhook ID")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	if !confirmDestroy(ctx, cmd, fmt.Sprintf("Delete webhook %s? It will stop receiving events.", webhookID)) {
		return errors.New("aborted")
	}
	if err := client.DeleteWebhook(ctx, projectID, webhookID); err != nil {
		return cloudAPIError(err)
	}
	return util.RenderAction(out, cmd.Bool("json"), "webhookId", webhookID, "deleted", fmt.Sprintf("Deleted webhook %s", util.Accented(webhookID)))
}

func testProjectWebhook(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	webhookID, err := requireArg(cmd, "webhook ID")
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	event := cmd.String("event")
	result, err := client.SendTestWebhook(ctx, projectID, webhookID, event)
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		util.PrintJSON(result)
	}
	// The server reports a failed delivery in the result rather than as an RPC
	// error; surface it as a failure so scripts can check the exit status.
	status := util.Deref(result.StatusCode)
	switch {
	case status == 0:
		reason := util.Deref(result.Error)
		if reason == "" {
			reason = "no response"
		}
		return fmt.Errorf("test delivery of %s to webhook %s failed: %s", event, webhookID, reason)
	case status < 200 || status >= 300:
		return fmt.Errorf("test delivery of %s to webhook %s failed: receiver responded HTTP %d", event, webhookID, status)
	}
	if !cmd.Bool("json") {
		out.Statusf("Delivered %s to webhook %s (HTTP %d)", util.Accented(event), util.Accented(webhookID), status)
	}
	return nil
}

func listProjectWebhookEvents(ctx context.Context, cmd *cli.Command) error {
	client, conf, user, err := requireCloudClient(cmd)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return err
	}
	events, err := client.ListWebhookEvents(ctx, projectID)
	if err != nil {
		return cloudAPIError(err)
	}
	if cmd.Bool("json") {
		if events == nil {
			events = []string{}
		}
		util.PrintJSON(events)
		return nil
	}
	for _, e := range events {
		out.Resultf("%s\n", e)
	}
	return nil
}
