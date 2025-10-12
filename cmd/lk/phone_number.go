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
	"context"
	"fmt"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/urfave/cli/v3"
)

var (
	PhoneNumberCommands = []*cli.Command{
		{
			Name:   "phonenumber",
			Usage:  "Manage phone numbers",
			Hidden: true,
			Commands: []*cli.Command{
				{
					Name:   "search",
					Usage:  "Search available phone numbers in inventory",
					Action: searchPhoneNumbers,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "country-code",
							Usage: "Filter by country code (e.g., \"US\", \"CA\")",
						},
						&cli.StringFlag{
							Name:  "area-code",
							Usage: "Filter by area code (e.g., \"415\")",
						},
						&cli.IntFlag{
							Name:  "limit",
							Usage: "Maximum number of results (default: 50)",
							Value: 50,
						},
						&cli.StringFlag{
							Name:  "page-token",
							Usage: "Token for pagination (empty for first page)",
						},
						jsonFlag,
					},
				},
				{
					Name:   "purchase",
					Usage:  "Purchase phone numbers from inventory",
					Action: purchasePhoneNumbers,
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:     "phonenumbers",
							Usage:    "Phone numbers to purchase (e.g., \"+1234567890\", \"+1234567891\")",
							Required: true,
						},
						&cli.StringFlag{
							Name:  "sip-dispatch-rule-id",
							Usage: "SIP dispatch rule ID to apply to all purchased numbers",
						},
					},
				},
				{
					Name:   "list",
					Usage:  "List phone numbers for a project",
					Action: listPhoneNumbers,
					Flags: []cli.Flag{
						&cli.IntFlag{
							Name:  "limit",
							Usage: "Maximum number of results (default: 50)",
							Value: 50,
						},
						&cli.StringFlag{
							Name:  "status",
							Usage: "Filter by status (active, pending, released)",
						},
						&cli.StringFlag{
							Name:  "page-token",
							Usage: "Token for pagination (empty for first page)",
						},
						&cli.StringFlag{
							Name:  "sip-dispatch-rule-id",
							Usage: "Filter by SIP dispatch rule ID",
						},
						jsonFlag,
					},
				},
				{
					Name:   "get",
					Usage:  "Get a phone number from a project",
					Action: getPhoneNumber,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "id",
							Usage: "Use phone number ID for direct lookup",
						},
						&cli.StringFlag{
							Name:  "phonenumber",
							Usage: "Use phone number string for lookup",
						},
					},
					ArgsUsage: "Either --id or --phonenumber must be provided",
				},
				{
					Name:   "update",
					Usage:  "Update a phone number in a project",
					Action: updatePhoneNumber,
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "id",
							Usage: "Use phone number ID for direct lookup",
						},
						&cli.StringFlag{
							Name:  "phonenumber",
							Usage: "Use phone number string for lookup",
						},
						&cli.StringFlag{
							Name:  "sip-dispatch-rule-id",
							Usage: "SIP dispatch rule ID to assign to the phone number",
						},
					},
					ArgsUsage: "Either --id or --phonenumber must be provided",
				},
				{
					Name:   "release",
					Usage:  "Release phone numbers",
					Action: releasePhoneNumbers,
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:  "ids",
							Usage: "Use phone number IDs for direct lookup",
						},
						&cli.StringSliceFlag{
							Name:  "phonenumbers",
							Usage: "Use phone number strings for lookup",
						},
					},
					ArgsUsage: "Either --ids or --phonenumbers must be provided",
				},
			},
		},
	}
)

func createPhoneNumberClient(ctx context.Context, cmd *cli.Command) (*lksdk.PhoneNumberClient, error) {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return nil, err
	}

	// Debug: Print the URL being used
	if cmd.Bool("verbose") {
		fmt.Printf("Using phone number service URL: %s\n", project.URL)
	}

	return lksdk.NewPhoneNumberClient(project.URL, project.APIKey, project.APISecret, withDefaultClientOpts(project)...), nil
}

func searchPhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	req := &livekit.SearchPhoneNumbersRequest{}
	if val := cmd.String("country-code"); val != "" {
		req.CountryCode = val
	}
	if val := cmd.String("area-code"); val != "" {
		req.AreaCode = val
	}
	if val := cmd.Int("limit"); val != 0 {
		req.Limit = int32(val)
	}
	if val := cmd.String("page-token"); val != "" {
		req.PageToken = &livekit.TokenPagination{Token: val}
	}

	resp, err := client.SearchPhoneNumbers(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	return listAndPrint(ctx, cmd, func(ctx context.Context, req *livekit.SearchPhoneNumbersRequest) (*livekit.SearchPhoneNumbersResponse, error) {
		return client.SearchPhoneNumbers(ctx, req)
	}, req, []string{
		"E164", "Country", "Area Code", "Type", "Locality", "Region", "Capabilities",
	}, func(item *livekit.PhoneNumber) []string {
		return []string{
			item.E164Format,
			item.CountryCode,
			item.AreaCode,
			strings.TrimPrefix(item.NumberType.String(), "PHONE_NUMBER_TYPE_"),
			item.Locality,
			item.Region,
			strings.Join(item.Capabilities, ","),
		}
	})
}

func purchasePhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	phoneNumbers := cmd.StringSlice("phonenumbers")
	if len(phoneNumbers) == 0 {
		return fmt.Errorf("at least one phone number must be provided")
	}

	req := &livekit.PurchasePhoneNumberRequest{
		PhoneNumbers: phoneNumbers,
	}
	if val := cmd.String("sip-dispatch-rule-id"); val != "" {
		req.SipDispatchRuleId = val
	}

	resp, err := client.PurchasePhoneNumber(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	fmt.Printf("Successfully purchased %d phone numbers:\n", len(resp.PhoneNumbers))
	for _, phoneNumber := range resp.PhoneNumbers {
		fmt.Printf("  %s (%s) - %s\n", phoneNumber.E164Format, phoneNumber.Id, strings.TrimPrefix(phoneNumber.Status.String(), "PHONE_NUMBER_STATUS_"))
	}

	return nil
}

func listPhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	req := &livekit.ListPhoneNumbersRequest{}
	if val := cmd.Int("limit"); val != 0 {
		req.Limit = int32(val)
	}
	if val := cmd.String("status"); val != "" {
		status, ok := livekit.PhoneNumberStatus_value["PHONE_NUMBER_STATUS_"+strings.ToUpper(val)]
		if !ok {
			return fmt.Errorf("invalid status: %s", val)
		}
		req.Status = livekit.PhoneNumberStatus(status)
	}
	if val := cmd.String("page-token"); val != "" {
		req.PageToken = &livekit.TokenPagination{Token: val}
	}
	if val := cmd.String("sip-dispatch-rule-id"); val != "" {
		req.SipDispatchRuleId = val
	}

	resp, err := client.ListPhoneNumbers(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	fmt.Printf("Total phone numbers: %d\n", resp.TotalCount)
	return listAndPrint(ctx, cmd, func(ctx context.Context, req *livekit.ListPhoneNumbersRequest) (*livekit.ListPhoneNumbersResponse, error) {
		return client.ListPhoneNumbers(ctx, req)
	}, req, []string{
		"ID", "E164", "Country", "Area Code", "Type", "Locality", "Region", "Capabilities", "Status", "SIP Dispatch Rule",
	}, func(item *livekit.PhoneNumber) []string {
		return []string{
			item.Id,
			item.E164Format,
			item.CountryCode,
			item.AreaCode,
			strings.TrimPrefix(item.NumberType.String(), "PHONE_NUMBER_TYPE_"),
			item.Locality,
			item.Region,
			strings.Join(item.Capabilities, ","),
			strings.TrimPrefix(item.Status.String(), "PHONE_NUMBER_STATUS_"),
			item.SipDispatchRuleId,
		}
	})
}

func getPhoneNumber(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	id := cmd.String("id")
	phoneNumber := cmd.String("phonenumber")

	if id == "" && phoneNumber == "" {
		return fmt.Errorf("either --id or --phonenumber must be provided")
	}
	if id != "" && phoneNumber != "" {
		return fmt.Errorf("only one of --id or --phonenumber can be provided")
	}

	req := &livekit.GetPhoneNumberRequest{}
	if id != "" {
		req.Id = id
	} else {
		req.PhoneNumber = phoneNumber
	}

	resp, err := client.GetPhoneNumber(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	item := resp.PhoneNumber
	fmt.Printf("Phone Number Details:\n")
	fmt.Printf("  ID: %s\n", item.Id)
	fmt.Printf("  E164 Format: %s\n", item.E164Format)
	fmt.Printf("  Country: %s\n", item.CountryCode)
	fmt.Printf("  Area Code: %s\n", item.AreaCode)
	fmt.Printf("  Type: %s\n", strings.TrimPrefix(item.NumberType.String(), "PHONE_NUMBER_TYPE_"))
	fmt.Printf("  Locality: %s\n", item.Locality)
	fmt.Printf("  Region: %s\n", item.Region)
	fmt.Printf("  Capabilities: %s\n", strings.Join(item.Capabilities, ","))
	fmt.Printf("  Status: %s\n", strings.TrimPrefix(item.Status.String(), "PHONE_NUMBER_STATUS_"))
	fmt.Printf("  SIP Dispatch Rule: %s\n", item.SipDispatchRuleId)
	if item.ReleasedAt != nil {
		fmt.Printf("  Released At: %s\n", item.ReleasedAt.AsTime().Format("2006-01-02 15:04:05"))
	}

	return nil
}

func updatePhoneNumber(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	id := cmd.String("id")
	phoneNumber := cmd.String("phonenumber")

	if id == "" && phoneNumber == "" {
		return fmt.Errorf("either --id or --phonenumber must be provided")
	}
	if id != "" && phoneNumber != "" {
		return fmt.Errorf("only one of --id or --phonenumber can be provided")
	}

	req := &livekit.UpdatePhoneNumberRequest{}
	if id != "" {
		req.Id = id
	} else {
		req.PhoneNumber = phoneNumber
	}
	if val := cmd.String("sip-dispatch-rule-id"); val != "" {
		req.SipDispatchRuleId = val
	}

	resp, err := client.UpdatePhoneNumber(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	item := resp.PhoneNumber
	fmt.Printf("Successfully updated phone number:\n")
	fmt.Printf("  ID: %s\n", item.Id)
	fmt.Printf("  E164 Format: %s\n", item.E164Format)
	fmt.Printf("  Status: %s\n", strings.TrimPrefix(item.Status.String(), "PHONE_NUMBER_STATUS_"))
	fmt.Printf("  SIP Dispatch Rule: %s\n", item.SipDispatchRuleId)

	return nil
}

func releasePhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	ids := cmd.StringSlice("ids")
	phoneNumbers := cmd.StringSlice("phonenumbers")

	if len(ids) == 0 && len(phoneNumbers) == 0 {
		return fmt.Errorf("either --ids or --phonenumbers must be provided")
	}
	if len(ids) > 0 && len(phoneNumbers) > 0 {
		return fmt.Errorf("only one of --ids or --phonenumbers can be provided")
	}

	req := &livekit.ReleasePhoneNumbersRequest{}
	if len(ids) > 0 {
		req.Ids = ids
	} else {
		req.PhoneNumbers = phoneNumbers
	}

	_, err = client.ReleasePhoneNumbers(ctx, req)
	if err != nil {
		return err
	}

	if len(ids) > 0 {
		fmt.Printf("Successfully released %d phone numbers by ID: %s\n", len(ids), strings.Join(ids, ", "))
	} else {
		fmt.Printf("Successfully released %d phone numbers: %s\n", len(phoneNumbers), strings.Join(phoneNumbers, ", "))
	}

	return nil
}
