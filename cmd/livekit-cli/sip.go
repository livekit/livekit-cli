// Copyright 2023 LiveKit, Inc.
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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
)

//lint:file-ignore SA1019 we still support older APIs for compatibility

const sipCategory = "SIP"

var (
	SIPCommands = []*cli.Command{
		{
			Name:     "sip",
			Usage:    "SIP management",
			Category: sipCategory,
			Subcommands: []*cli.Command{
				{
					Name:     "inbound",
					Aliases:  []string{"in", "inbound-trunk"},
					Usage:    "Inbound SIP Trunk management",
					Category: sipCategory,
					Subcommands: []*cli.Command{
						{
							Name:     "list",
							Usage:    "List all inbound SIP Trunk",
							Before:   createSIPClient,
							Action:   listSipInboundTrunk,
							Category: sipCategory,
							Flags:    withDefaultFlags(),
						},
						{
							Name:     "create",
							Usage:    "Create a inbound SIP Trunk",
							Before:   createSIPClient,
							Action:   createSIPInboundTrunk,
							Category: sipCategory,
							Flags: withDefaultFlags(
								RequestFlag[livekit.CreateSIPInboundTrunkRequest](),
							),
						},
						{
							Name:     "delete",
							Usage:    "Delete inbound SIP Trunk",
							Before:   createSIPClient,
							Action:   deleteSIPTrunk,
							Category: sipCategory,
							Flags: withDefaultFlags(
								&cli.StringFlag{
									Name:     "id",
									Usage:    "SIPTrunk ID",
									Required: true,
								},
							),
						},
					},
				},
				{
					Name:     "outbound",
					Aliases:  []string{"out", "outbound-trunk"},
					Usage:    "Outbound SIP Trunk management",
					Category: sipCategory,
					Subcommands: []*cli.Command{
						{
							Name:     "list",
							Usage:    "List all outbound SIP Trunk",
							Before:   createSIPClient,
							Action:   listSipOutboundTrunk,
							Category: sipCategory,
							Flags:    withDefaultFlags(),
						},
						{
							Name:     "create",
							Usage:    "Create a outbound SIP Trunk",
							Before:   createSIPClient,
							Action:   createSIPOutboundTrunk,
							Category: sipCategory,
							Flags: withDefaultFlags(
								RequestFlag[livekit.CreateSIPOutboundTrunkRequest](),
							),
						},
						{
							Name:     "delete",
							Usage:    "Delete outbound SIP Trunk",
							Before:   createSIPClient,
							Action:   deleteSIPTrunk,
							Category: sipCategory,
							Flags: withDefaultFlags(
								&cli.StringFlag{
									Name:     "id",
									Usage:    "SIPTrunk ID",
									Required: true,
								},
							),
						},
					},
				},
				{
					Name:     "dispatch",
					Usage:    "SIP Dispatch Rule management",
					Aliases:  []string{"dispatch-rule"},
					Category: sipCategory,
					Subcommands: []*cli.Command{
						{
							Name:     "create",
							Usage:    "Create a SIP Dispatch Rule",
							Before:   createSIPClient,
							Action:   createSIPDispatchRule,
							Category: sipCategory,
							Flags: withDefaultFlags(
								RequestFlag[livekit.CreateSIPDispatchRuleRequest](),
							),
						},
						{
							Name:     "list",
							Usage:    "List all SIP Dispatch Rule",
							Before:   createSIPClient,
							Action:   listSipDispatchRule,
							Category: sipCategory,
							Flags:    withDefaultFlags(),
						},
						{
							Name:     "delete",
							Usage:    "Delete SIP Dispatch Rule",
							Before:   createSIPClient,
							Action:   deleteSIPDispatchRule,
							Category: sipCategory,
							Flags: withDefaultFlags(
								&cli.StringFlag{
									Name:     "id",
									Usage:    "SIPDispatchRule ID",
									Required: true,
								},
							),
						},
					},
				},
				{
					Name:     "participant",
					Usage:    "SIP Participant management",
					Category: sipCategory,
					Subcommands: []*cli.Command{
						{
							Name:     "create",
							Usage:    "Create a SIP Participant",
							Before:   createSIPClient,
							Action:   createSIPParticipant,
							Category: sipCategory,
							Flags: withDefaultFlags(
								RequestFlag[livekit.CreateSIPParticipantRequest](),
							),
						},
					},
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden:   true, // deprecated: use "sip trunk create"
			Name:     "create-sip-trunk",
			Usage:    "Create a SIP Trunk",
			Before:   createSIPClient,
			Action:   createSIPTrunk,
			Category: sipCategory,
			Flags: withDefaultFlags(
				//lint:ignore SA1019 we still support it
				RequestFlag[livekit.CreateSIPTrunkRequest](),
			),
		},
		{
			Hidden:   true, // deprecated: use "sip trunk list"
			Name:     "list-sip-trunk",
			Usage:    "List all SIP trunk",
			Before:   createSIPClient,
			Action:   listSipTrunk,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
			Hidden:   true, // deprecated: use "sip trunk delete"
			Name:     "delete-sip-trunk",
			Usage:    "Delete SIP Trunk",
			Before:   createSIPClient,
			Action:   deleteSIPTrunk,
			Category: sipCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPTrunk ID",
					Required: true,
				},
			),
		},
		{
			Hidden:   true, // deprecated: use "sip dispatch create"
			Name:     "create-sip-dispatch-rule",
			Usage:    "Create a SIP Dispatch Rule",
			Before:   createSIPClient,
			Action:   createSIPDispatchRule,
			Category: sipCategory,
			Flags: withDefaultFlags(
				RequestFlag[livekit.CreateSIPDispatchRuleRequest](),
			),
		},
		{
			Hidden:   true, // deprecated: use "sip dispatch list"
			Name:     "list-sip-dispatch-rule",
			Usage:    "List all SIP Dispatch Rule",
			Before:   createSIPClient,
			Action:   listSipDispatchRule,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
			Hidden:   true, // deprecated: use "sip dispatch delete"
			Name:     "delete-sip-dispatch-rule",
			Usage:    "Delete SIP Dispatch Rule",
			Before:   createSIPClient,
			Action:   deleteSIPDispatchRule,
			Category: sipCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPDispatchRule ID",
					Required: true,
				},
			),
		},
		{
			Hidden:   true, // deprecated: use "sip participant create"
			Name:     "create-sip-participant",
			Usage:    "Create a SIP Participant",
			Before:   createSIPClient,
			Action:   createSIPParticipant,
			Category: sipCategory,
			Flags: withDefaultFlags(
				RequestFlag[livekit.CreateSIPParticipantRequest](),
			),
		},
	}

	sipClient *lksdk.SIPClient
)

func createSIPClient(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	sipClient = lksdk.NewSIPClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...)
	return nil
}

func createSIPTrunk(c *cli.Context) error {
	//lint:ignore SA1019 we still support it
	req, err := ReadRequest[livekit.CreateSIPTrunkRequest](c)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	//lint:ignore SA1019 we still support it
	info, err := sipClient.CreateSIPTrunk(c.Context, req)
	if err != nil {
		return err
	}

	printSIPTrunkID(info)
	return nil
}

func createSIPInboundTrunk(c *cli.Context) error {
	req, err := ReadRequest[livekit.CreateSIPInboundTrunkRequest](c)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := sipClient.CreateSIPInboundTrunk(c.Context, req)
	if err != nil {
		return err
	}

	printSIPTrunkID(info)
	return nil
}

func createSIPOutboundTrunk(c *cli.Context) error {
	req, err := ReadRequest[livekit.CreateSIPOutboundTrunkRequest](c)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := sipClient.CreateSIPOutboundTrunk(c.Context, req)
	if err != nil {
		return err
	}

	printSIPTrunkID(info)
	return nil
}

func userPass(user string, hasPass bool) string {
	if user == "" && !hasPass {
		return ""
	}
	passStr := ""
	if hasPass {
		passStr = "****"
	}
	return user + " / " + passStr
}

func listSipTrunk(c *cli.Context) error {
	//lint:ignore SA1019 we still support it
	res, err := sipClient.ListSIPTrunk(c.Context, &livekit.ListSIPTrunkRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"SipTrunkId", "Name", "Kind", "Number",
		"AllowAddresses", "AllowNumbers", "InboundAuth",
		"OutboundAddress", "OutboundAuth",
		"Metadata",
	})
	for _, item := range res.Items {
		if item == nil {
			continue
		}
		inboundNumbers := item.InboundNumbers
		for _, re := range item.InboundNumbersRegex {
			inboundNumbers = append(inboundNumbers, "regexp("+re+")")
		}

		table.Append([]string{
			item.SipTrunkId, item.Name, strings.TrimPrefix(item.Kind.String(), "TRUNK_"), item.OutboundNumber,
			strings.Join(item.InboundAddresses, ","), strings.Join(inboundNumbers, ","), userPass(item.InboundUsername, item.InboundPassword != ""),
			item.OutboundAddress, userPass(item.OutboundUsername, item.OutboundPassword != ""),
			item.Metadata,
		})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func listSipInboundTrunk(c *cli.Context) error {
	res, err := sipClient.ListSIPInboundTrunk(c.Context, &livekit.ListSIPInboundTrunkRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"SipTrunkId", "Name", "Numbers",
		"AllowedAddresses", "AllowedNumbers",
		"Authentication",
		"Metadata",
	})
	for _, item := range res.Items {
		if item == nil {
			continue
		}
		table.Append([]string{
			item.SipTrunkId, item.Name, strings.Join(item.Numbers, ","),
			strings.Join(item.AllowedAddresses, ","), strings.Join(item.AllowedNumbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			item.Metadata,
		})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func listSipOutboundTrunk(c *cli.Context) error {
	res, err := sipClient.ListSIPOutboundTrunk(c.Context, &livekit.ListSIPOutboundTrunkRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"SipTrunkId", "Name",
		"Address", "Transport",
		"Numbers",
		"Authentication",
		"Metadata",
	})
	for _, item := range res.Items {
		if item == nil {
			continue
		}
		table.Append([]string{
			item.SipTrunkId, item.Name,
			item.Address, strings.TrimPrefix(item.Transport.String(), "SIP_TRANSPORT_"),
			strings.Join(item.Numbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			item.Metadata,
		})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func deleteSIPTrunk(c *cli.Context) error {
	info, err := sipClient.DeleteSIPTrunk(c.Context, &livekit.DeleteSIPTrunkRequest{
		SipTrunkId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printSIPTrunkID(info)
	return nil
}

func printSIPTrunkID(info interface{ GetSipTrunkId() string }) {
	fmt.Printf("SIPTrunkID: %v\n", info.GetSipTrunkId())
}

func createSIPDispatchRule(c *cli.Context) error {
	req, err := ReadRequest[livekit.CreateSIPDispatchRuleRequest](c)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := sipClient.CreateSIPDispatchRule(c.Context, req)
	if err != nil {
		return err
	}

	printSIPDispatchRuleID(info)
	return nil
}

func listSipDispatchRule(c *cli.Context) error {
	res, err := sipClient.ListSIPDispatchRule(c.Context, &livekit.ListSIPDispatchRuleRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"SipDispatchRuleId", "Name", "SipTrunks", "Type", "RoomName", "Pin", "HidePhone", "Metadata"})
	for _, item := range res.Items {
		if item == nil {
			continue
		}
		var room, typ, pin string
		switch r := item.GetRule().GetRule().(type) {
		case *livekit.SIPDispatchRule_DispatchRuleDirect:
			room = r.DispatchRuleDirect.RoomName
			pin = r.DispatchRuleDirect.Pin
			typ = "Direct"
		case *livekit.SIPDispatchRule_DispatchRuleIndividual:
			room = r.DispatchRuleIndividual.RoomPrefix + "*"
			pin = r.DispatchRuleIndividual.Pin
			typ = "Individual"
		}
		trunks := strings.Join(item.TrunkIds, ",")
		if trunks == "" {
			trunks = "<any>"
		}
		table.Append([]string{item.SipDispatchRuleId, item.Name, trunks, typ, room, pin, strconv.FormatBool(item.HidePhoneNumber), item.Metadata})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func deleteSIPDispatchRule(c *cli.Context) error {
	info, err := sipClient.DeleteSIPDispatchRule(c.Context, &livekit.DeleteSIPDispatchRuleRequest{
		SipDispatchRuleId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printSIPDispatchRuleID(info)
	return nil
}

func printSIPDispatchRuleID(info *livekit.SIPDispatchRuleInfo) {
	fmt.Printf("SIPDispatchRuleID: %v\n", info.SipDispatchRuleId)
}

func createSIPParticipant(c *cli.Context) error {
	req, err := ReadRequest[livekit.CreateSIPParticipantRequest](c)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
	// Default deadline is too short, thus, we must set a higher deadline for it.
	ctx, cancel := context.WithTimeout(c.Context, 30*time.Second)
	defer cancel()

	info, err := sipClient.CreateSIPParticipant(ctx, req)
	if err != nil {
		return err
	}

	printSIPParticipantInfo(info)
	return nil
}

func printSIPParticipantInfo(info *livekit.SIPParticipantInfo) {
	fmt.Printf("SIPCallID: %v\n", info.SipCallId)
	fmt.Printf("ParticipantID: %v\n", info.ParticipantId)
	fmt.Printf("ParticipantIdentity: %v\n", info.ParticipantIdentity)
	fmt.Printf("RoomName: %v\n", info.RoomName)
}
