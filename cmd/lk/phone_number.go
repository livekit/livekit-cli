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
							Usage: "Maximum number of results (default: 50)",
							Value: 50,
						},
						&cli.StringSliceFlag{
							Name:  "status",
							Usage: "Filter by status(es) (active, pending, released). Mutliple values can be specified.",
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

// getPhoneNumberToDispatchRulesMap fetches all dispatch rules and maps phone number IDs to their associated dispatch rule IDs
// Returns a map where key is phone number ID and value is a slice of dispatch rule IDs
func getPhoneNumberToDispatchRulesMap(ctx context.Context, cmd *cli.Command) (map[string][]string, error) {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	sipClient := lksdk.NewSIPClient(project.URL, project.APIKey, project.APISecret, withDefaultClientOpts(project)...)

	// List all dispatch rules
	resp, err := sipClient.ListSIPDispatchRule(ctx, &livekit.ListSIPDispatchRuleRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list dispatch rules: %w", err)
	}

	// Build map: phone number ID -> []dispatch rule IDs
	phoneNumberToRules := make(map[string][]string)
	for _, rule := range resp.Items {
		for _, trunkID := range rule.TrunkIds {
			// Check if trunkID is a phone number ID (starts with PN_PPN_)
			if strings.HasPrefix(trunkID, "PN_PPN_") {
				phoneNumberToRules[trunkID] = append(phoneNumberToRules[trunkID], rule.SipDispatchRuleId)
			}
		}
	}

	return phoneNumberToRules, nil
}

// appendPhoneNumberToDispatchRule appends a phone number ID to the trunk_ids of a dispatch rule
func appendPhoneNumberToDispatchRule(ctx context.Context, cmd *cli.Command, dispatchRuleID, phoneNumberID string) error {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	sipClient := lksdk.NewSIPClient(project.URL, project.APIKey, project.APISecret, withDefaultClientOpts(project)...)

	// Get the current dispatch rule to check if phone number ID is already in trunk_ids
	rules, err := sipClient.GetSIPDispatchRulesByIDs(ctx, []string{dispatchRuleID})
	if err != nil {
		return fmt.Errorf("failed to get dispatch rule: %w", err)
	}
	if len(rules) == 0 {
		return fmt.Errorf("dispatch rule %s not found", dispatchRuleID)
	}
	currentRule := rules[0]

	// Check if phone number ID is already in trunk_ids
	for _, trunkID := range currentRule.TrunkIds {
		if trunkID == phoneNumberID {
			// Already in the list, no need to update
			return nil
		}
	}

	// Append phone number ID to trunk_ids using Update action
	updateReq := &livekit.UpdateSIPDispatchRuleRequest{
		SipDispatchRuleId: dispatchRuleID,
		Action: &livekit.UpdateSIPDispatchRuleRequest_Update{
			Update: &livekit.SIPDispatchRuleUpdate{
				TrunkIds: &livekit.ListUpdate{
					Add: []string{phoneNumberID},
				},
			},
		},
	}

	_, err = sipClient.UpdateSIPDispatchRule(ctx, updateReq)
	if err != nil {
		return fmt.Errorf("failed to update dispatch rule: %w", err)
	}

	return nil
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

	// Call purchase and get dispatch rules in parallel
	type purchaseResult struct {
		resp *livekit.PurchasePhoneNumberResponse
		err  error
	}
	type dispatchRulesResult struct {
		rules map[string][]string
		err   error
	}

	purchaseChan := make(chan purchaseResult, 1)
	dispatchRulesChan := make(chan dispatchRulesResult, 1)

	// Purchase phone numbers
	go func() {
		resp, err := client.PurchasePhoneNumber(ctx, req)
		purchaseChan <- purchaseResult{resp: resp, err: err}
	}()

	// Get dispatch rules mapping in parallel
	go func() {
		rules, err := getPhoneNumberToDispatchRulesMap(ctx, cmd)
		dispatchRulesChan <- dispatchRulesResult{rules: rules, err: err}
	}()

	// Wait for purchase to complete
	purchaseRes := <-purchaseChan
	if purchaseRes.err != nil {
		return purchaseRes.err
	}
	resp := purchaseRes.resp

	// If dispatch rule ID was provided, append each purchased phone number ID to the dispatch rule's trunk_ids
	dispatchRuleAdded := make(map[string]bool)
	if dispatchRuleID != "" {
		for _, phoneNumber := range resp.PhoneNumbers {
			if err := appendPhoneNumberToDispatchRule(ctx, cmd, dispatchRuleID, phoneNumber.Id); err != nil {
				// Log error but don't fail the purchase operation
				fmt.Fprintf(cmd.ErrWriter, "Warning: failed to add phone number %s to dispatch rule %s: %v\n", phoneNumber.Id, dispatchRuleID, err)
			} else {
				dispatchRuleAdded[phoneNumber.Id] = true
			}
		}
	}

	// Wait for dispatch rules (ignore errors, we'll just not show them)
	dispatchRulesRes := <-dispatchRulesChan
	phoneNumberToRules := dispatchRulesRes.rules
	if dispatchRulesRes.err != nil {
		// Log but don't fail
		if cmd.Bool("verbose") {
			fmt.Fprintf(cmd.ErrWriter, "Warning: failed to get dispatch rules: %v\n", dispatchRulesRes.err)
		}
		phoneNumberToRules = make(map[string][]string)
	}

	// Update the mapping with newly added dispatch rules
	if dispatchRuleID != "" {
		for _, phoneNumber := range resp.PhoneNumbers {
			if dispatchRuleAdded[phoneNumber.Id] {
				phoneNumberToRules[phoneNumber.Id] = append(phoneNumberToRules[phoneNumber.Id], dispatchRuleID)
			}
		}
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	fmt.Printf("Successfully purchased %d phone numbers:\n", len(resp.PhoneNumbers))
	for _, phoneNumber := range resp.PhoneNumbers {
		ruleInfo := ""
		rules := phoneNumberToRules[phoneNumber.Id]
		if len(rules) > 0 {
			ruleInfo = fmt.Sprintf(" (SIP Dispatch Rules: %s)", strings.Join(rules, ", "))
		} else if phoneNumber.SipDispatchRuleId != "" {
			ruleInfo = fmt.Sprintf(" (SIP Dispatch Rule: %s)", phoneNumber.SipDispatchRuleId)
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
	if val := cmd.Int("limit"); val != 0 {
		limit := int32(val)
		req.Limit = &limit
	}
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

	// Call list and get dispatch rules in parallel
	type listResult struct {
		resp *livekit.ListPhoneNumbersResponse
		err  error
	}
	type dispatchRulesResult struct {
		rules map[string][]string
		err   error
	}

	listChan := make(chan listResult, 1)
	dispatchRulesChan := make(chan dispatchRulesResult, 1)

	// List phone numbers
	go func() {
		resp, err := client.ListPhoneNumbers(ctx, req)
		listChan <- listResult{resp: resp, err: err}
	}()

	// Get dispatch rules mapping in parallel
	go func() {
		rules, err := getPhoneNumberToDispatchRulesMap(ctx, cmd)
		dispatchRulesChan <- dispatchRulesResult{rules: rules, err: err}
	}()

	// Wait for list to complete
	listRes := <-listChan
	if listRes.err != nil {
		return listRes.err
	}
	resp := listRes.resp

	// Wait for dispatch rules (ignore errors, we'll just not show them)
	dispatchRulesRes := <-dispatchRulesChan
	phoneNumberToRules := dispatchRulesRes.rules
	if dispatchRulesRes.err != nil {
		// Log but don't fail
		if cmd.Bool("verbose") {
			fmt.Fprintf(cmd.ErrWriter, "Warning: failed to get dispatch rules: %v\n", dispatchRulesRes.err)
		}
		phoneNumberToRules = make(map[string][]string)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	fmt.Printf("Total phone numbers: %d\n", resp.TotalCount)
	return listAndPrint(ctx, cmd, func(ctx context.Context, req *livekit.ListPhoneNumbersRequest) (*livekit.ListPhoneNumbersResponse, error) {
		return client.ListPhoneNumbers(ctx, req)
	}, req, []string{
		"ID", "E164", "Country", "Area Code", "Type", "Locality", "Region", "Capabilities", "Status", "SIP Dispatch Rules",
	}, func(item *livekit.PhoneNumber) []string {
		rules := phoneNumberToRules[item.Id]
		dispatchRulesStr := ""
		if len(rules) > 0 {
			dispatchRulesStr = strings.Join(rules, ", ")
		} else if item.SipDispatchRuleId != "" {
			dispatchRulesStr = item.SipDispatchRuleId
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

	// Call get and get dispatch rules in parallel
	type getResult struct {
		resp *livekit.GetPhoneNumberResponse
		err  error
	}
	type dispatchRulesResult struct {
		rules map[string][]string
		err   error
	}

	getChan := make(chan getResult, 1)
	dispatchRulesChan := make(chan dispatchRulesResult, 1)

	// Get phone number
	go func() {
		resp, err := client.GetPhoneNumber(ctx, req)
		getChan <- getResult{resp: resp, err: err}
	}()

	// Get dispatch rules mapping in parallel
	go func() {
		rules, err := getPhoneNumberToDispatchRulesMap(ctx, cmd)
		dispatchRulesChan <- dispatchRulesResult{rules: rules, err: err}
	}()

	// Wait for get to complete
	getRes := <-getChan
	if getRes.err != nil {
		return getRes.err
	}
	resp := getRes.resp

	// Wait for dispatch rules (ignore errors, we'll just not show them)
	dispatchRulesRes := <-dispatchRulesChan
	phoneNumberToRules := dispatchRulesRes.rules
	if dispatchRulesRes.err != nil {
		// Log but don't fail
		if cmd.Bool("verbose") {
			fmt.Fprintf(cmd.ErrWriter, "Warning: failed to get dispatch rules: %v\n", dispatchRulesRes.err)
		}
		phoneNumberToRules = make(map[string][]string)
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	item := resp.PhoneNumber
	rules := phoneNumberToRules[item.Id]
	dispatchRulesStr := ""
	if len(rules) > 0 {
		dispatchRulesStr = strings.Join(rules, ", ")
	} else if item.SipDispatchRuleId != "" {
		dispatchRulesStr = item.SipDispatchRuleId
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

	// Call update and get dispatch rules in parallel
	type updateResult struct {
		resp *livekit.UpdatePhoneNumberResponse
		err  error
	}
	type dispatchRulesResult struct {
		rules map[string][]string
		err   error
	}

	updateChan := make(chan updateResult, 1)
	dispatchRulesChan := make(chan dispatchRulesResult, 1)

	// Update phone number
	go func() {
		resp, err := client.UpdatePhoneNumber(ctx, req)
		updateChan <- updateResult{resp: resp, err: err}
	}()

	// Get dispatch rules mapping in parallel
	go func() {
		rules, err := getPhoneNumberToDispatchRulesMap(ctx, cmd)
		dispatchRulesChan <- dispatchRulesResult{rules: rules, err: err}
	}()

	// Wait for update to complete
	updateRes := <-updateChan
	if updateRes.err != nil {
		return updateRes.err
	}
	resp := updateRes.resp

	// If dispatch rule ID was provided, append the phone number ID to the dispatch rule's trunk_ids
	dispatchRuleAdded := false
	if dispatchRuleID != "" {
		phoneNumberID := resp.PhoneNumber.Id
		if err := appendPhoneNumberToDispatchRule(ctx, cmd, dispatchRuleID, phoneNumberID); err != nil {
			// Log error but don't fail the update operation
			fmt.Fprintf(cmd.ErrWriter, "Warning: failed to add phone number %s to dispatch rule %s: %v\n", phoneNumberID, dispatchRuleID, err)
		} else {
			dispatchRuleAdded = true
		}
	}

	// Wait for dispatch rules (ignore errors, we'll just not show them)
	dispatchRulesRes := <-dispatchRulesChan
	phoneNumberToRules := dispatchRulesRes.rules
	if dispatchRulesRes.err != nil {
		// Log but don't fail
		if cmd.Bool("verbose") {
			fmt.Fprintf(cmd.ErrWriter, "Warning: failed to get dispatch rules: %v\n", dispatchRulesRes.err)
		}
		phoneNumberToRules = make(map[string][]string)
	}

	// Update the mapping with newly added dispatch rule if it was successfully added
	if dispatchRuleAdded && dispatchRuleID != "" {
		phoneNumberID := resp.PhoneNumber.Id
		// Check if it's already in the map (from the parallel fetch)
		if _, exists := phoneNumberToRules[phoneNumberID]; !exists {
			phoneNumberToRules[phoneNumberID] = []string{}
		}
		// Check if dispatchRuleID is already in the list
		found := false
		for _, ruleID := range phoneNumberToRules[phoneNumberID] {
			if ruleID == dispatchRuleID {
				found = true
				break
			}
		}
		if !found {
			phoneNumberToRules[phoneNumberID] = append(phoneNumberToRules[phoneNumberID], dispatchRuleID)
		}
	}

	if cmd.Bool("json") {
		util.PrintJSON(resp)
		return nil
	}

	item := resp.PhoneNumber
	rules := phoneNumberToRules[item.Id]
	dispatchRulesStr := ""
	if len(rules) > 0 {
		dispatchRulesStr = strings.Join(rules, ", ")
	} else if item.SipDispatchRuleId != "" {
		dispatchRulesStr = item.SipDispatchRuleId
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
