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
			Name:   "number",
			Usage:  "Manage phone numbers",
			Hidden: false,
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
						jsonFlag,
					},
				},
				{
					Name:   "purchase",
					Usage:  "Purchase phone numbers from inventory",
					Action: purchasePhoneNumbers,
					Flags: []cli.Flag{
						&cli.StringSliceFlag{
							Name:     "numbers",
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
							Usage: "Maximum number of results per page (default: 50)",
							Value: 50,
						},
						&cli.IntFlag{
							Name:  "offset",
							Usage: "Offset for pagination (default: 0)",
							Value: 0,
						},
						&cli.StringSliceFlag{
							Name:  "status",
							Usage: "Filter by status(es) (active, pending, released, offline). Multiple values can be specified.",
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
							Name:  "number",
							Usage: "Use phone number string for lookup",
						},
					},
					ArgsUsage: "Either --id or --number must be provided",
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
							Name:  "number",
							Usage: "Use phone number string for lookup",
						},
						&cli.StringFlag{
							Name:  "sip-dispatch-rule-id",
							Usage: "SIP dispatch rule ID to assign to the phone number",
						},
					},
					ArgsUsage: "Either --id or --number must be provided",
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
							Name:  "numbers",
							Usage: "Use phone number strings for lookup",
						},
					},
					ArgsUsage: "Either --ids or --numbers must be provided",
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
		req.AreaCode = &val
	}
	if val := cmd.Int("limit"); val != 0 {
		limit := int32(val)
		req.Limit = &limit
	}

	resp, err := client.SearchPhoneNumbers(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	// Define the search function
	searchFunc := func(ctx context.Context, req *livekit.SearchPhoneNumbersRequest) (*livekit.SearchPhoneNumbersResponse, error) {
		return client.SearchPhoneNumbers(ctx, req)
	}

	// Define the column headers
	headers := []string{
		"E164", "Country", "Area Code", "Type", "Locality", "Region", "Capabilities",
	}

	// Define the row formatter
	rowFormatter := func(item *livekit.PhoneNumber) []string {
		return []string{
			item.E164Format,
			item.CountryCode,
			item.AreaCode,
			strings.TrimPrefix(item.NumberType.String(), "PHONE_NUMBER_TYPE_"),
			item.Locality,
			item.Region,
			strings.Join(item.Capabilities, ","),
		}
	}

	return listAndPrint(ctx, cmd, searchFunc, req, headers, rowFormatter)
}

func purchasePhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	phoneNumbers := cmd.StringSlice("numbers")
	if len(phoneNumbers) == 0 {
		return fmt.Errorf("at least one phone number must be provided")
	}

	dispatchRuleID := cmd.String("sip-dispatch-rule-id")

	req := &livekit.PurchasePhoneNumberRequest{
		PhoneNumbers: phoneNumbers,
	}
	if dispatchRuleID != "" {
		req.SipDispatchRuleId = &dispatchRuleID
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
		ruleInfo := ""
		if len(phoneNumber.SipDispatchRuleIds) > 0 {
			ruleInfo = fmt.Sprintf(" (SIP Dispatch Rules: %s)", strings.Join(phoneNumber.SipDispatchRuleIds, ", "))
		}
		fmt.Printf("  %s (%s) - %s%s\n", phoneNumber.E164Format, phoneNumber.Id, strings.TrimPrefix(phoneNumber.Status.String(), "PHONE_NUMBER_STATUS_"), ruleInfo)
	}

	return nil
}

func listPhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	req := &livekit.ListPhoneNumbersRequest{}
	limit := int32(cmd.Int("limit"))
	offset := int32(cmd.Int("offset"))

	// Encode offset and limit into a page token for pagination
	// Even if offset is 0, we encode it to include the limit in the token
	pageToken, err := livekit.EncodeTokenPagination(offset, limit)
	if err != nil {
		return fmt.Errorf("failed to encode pagination token: %w", err)
	}
	req.PageToken = pageToken

	if statuses := cmd.StringSlice("status"); len(statuses) > 0 {
		var phoneNumberStatuses []livekit.PhoneNumberStatus
		for _, status := range statuses {
			statusValue, ok := livekit.PhoneNumberStatus_value["PHONE_NUMBER_STATUS_"+strings.ToUpper(status)]
			if !ok {
				return fmt.Errorf("invalid status: %s", status)
			}
			phoneNumberStatuses = append(phoneNumberStatuses, livekit.PhoneNumberStatus(statusValue))
		}
		req.Statuses = phoneNumberStatuses
	}
	if val := cmd.String("sip-dispatch-rule-id"); val != "" {
		req.SipDispatchRuleId = &val
	}

	resp, err := client.ListPhoneNumbers(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	fmt.Printf("Total phone numbers: %d", resp.TotalCount)
	if resp.OfflineCount > 0 {
		fmt.Printf(" (%d offline)", resp.OfflineCount)
	}
	fmt.Printf("\n")

	// Show pagination info
	if offset > 0 {
		fmt.Printf("Showing results from offset %d\n", offset)
	}
	if resp.NextPageToken != nil {
		nextOffset, _, err := livekit.DecodeTokenPagination(resp.NextPageToken)
		if err == nil {
			fmt.Printf("More results available. Use --offset %d to see the next page.\n", nextOffset)
		}
	}

	return listAndPrint(ctx, cmd, func(ctx context.Context, req *livekit.ListPhoneNumbersRequest) (*livekit.ListPhoneNumbersResponse, error) {
		return client.ListPhoneNumbers(ctx, req)
	}, req, []string{
		"ID", "E164", "Country", "Area Code", "Type", "Locality", "Region", "Capabilities", "Status", "SIP Dispatch Rules",
	}, func(item *livekit.PhoneNumber) []string {
		dispatchRulesStr := ""
		if len(item.SipDispatchRuleIds) > 0 {
			dispatchRulesStr = strings.Join(item.SipDispatchRuleIds, ", ")
		} else {
			dispatchRulesStr = "-"
		}
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
			dispatchRulesStr,
		}
	})
}

func getPhoneNumber(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	id := cmd.String("id")
	phoneNumber := cmd.String("number")

	if id == "" && phoneNumber == "" {
		return fmt.Errorf("either --id or --number must be provided")
	}
	if id != "" && phoneNumber != "" {
		return fmt.Errorf("only one of --id or --number can be provided")
	}

	req := &livekit.GetPhoneNumberRequest{}
	if id != "" {
		req.Id = &id
	} else {
		req.PhoneNumber = &phoneNumber
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
	dispatchRulesStr := ""
	if len(item.SipDispatchRuleIds) > 0 {
		dispatchRulesStr = strings.Join(item.SipDispatchRuleIds, ", ")
	} else {
		dispatchRulesStr = "-"
	}

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
	fmt.Printf("  SIP Dispatch Rules: %s\n", dispatchRulesStr)
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
	phoneNumber := cmd.String("number")

	if id == "" && phoneNumber == "" {
		return fmt.Errorf("either --id or --number must be provided")
	}
	if id != "" && phoneNumber != "" {
		return fmt.Errorf("only one of --id or --number can be provided")
	}

	dispatchRuleID := cmd.String("sip-dispatch-rule-id")

	req := &livekit.UpdatePhoneNumberRequest{}
	if id != "" {
		req.Id = &id
	} else {
		req.PhoneNumber = &phoneNumber
	}
	if dispatchRuleID != "" {
		req.SipDispatchRuleId = &dispatchRuleID
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
	dispatchRulesStr := ""
	if len(item.SipDispatchRuleIds) > 0 {
		dispatchRulesStr = strings.Join(item.SipDispatchRuleIds, ", ")
	} else {
		dispatchRulesStr = "-"
	}

	fmt.Printf("Successfully updated phone number:\n")
	fmt.Printf("  ID: %s\n", item.Id)
	fmt.Printf("  E164 Format: %s\n", item.E164Format)
	fmt.Printf("  Status: %s\n", strings.TrimPrefix(item.Status.String(), "PHONE_NUMBER_STATUS_"))
	fmt.Printf("  SIP Dispatch Rules: %s\n", dispatchRulesStr)

	return nil
}

func releasePhoneNumbers(ctx context.Context, cmd *cli.Command) error {
	client, err := createPhoneNumberClient(ctx, cmd)
	if err != nil {
		return err
	}

	ids := cmd.StringSlice("ids")
	phoneNumbers := cmd.StringSlice("numbers")

	if len(ids) == 0 && len(phoneNumbers) == 0 {
		return fmt.Errorf("either --ids or --numbers must be provided")
	}
	if len(ids) > 0 && len(phoneNumbers) > 0 {
		return fmt.Errorf("only one of --ids or --numbers can be provided")
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
