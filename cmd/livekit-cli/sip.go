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
	"strconv"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/urfave/cli/v2"
)

//lint:file-ignore SA1019 we still support older APIs for compatibility

const (
	sipCategory            = "SIP"
	sipTrunkCategory       = "Trunks"
	sipDispatchCategory    = "Dispatch Rules"
	sipParticipantCategory = "Participants"
)

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
					Category: sipTrunkCategory,
					Subcommands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all inbound SIP Trunk",
							Action: listSipInboundTrunk,
							Flags:  withDefaultFlags(),
						},
						{
							Name:      "create",
							Usage:     "Create a inbound SIP Trunk",
							Action:    createSIPInboundTrunk,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: RequestDesc[livekit.CreateSIPInboundTrunkRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete SIP Trunk",
							Action:    deleteSIPTrunk,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "outbound",
					Aliases:  []string{"out", "outbound-trunk"},
					Usage:    "Outbound SIP Trunk management",
					Category: sipTrunkCategory,
					Subcommands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all outbound SIP Trunk",
							Action: listSipOutboundTrunk,
							Flags:  withDefaultFlags(),
						},
						{
							Name:      "create",
							Usage:     "Create a outbound SIP Trunk",
							Action:    createSIPOutboundTrunk,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: RequestDesc[livekit.CreateSIPOutboundTrunkRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete SIP Trunk",
							Action:    deleteSIPTrunk,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "dispatch",
					Usage:    "SIP Dispatch Rule management",
					Aliases:  []string{"dispatch-rule"},
					Category: sipDispatchCategory,
					Subcommands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all SIP Dispatch Rule",
							Action: listSipDispatchRule,
							Flags:  withDefaultFlags(),
						},
						{
							Name:      "create",
							Usage:     "Create a SIP Dispatch Rule",
							Action:    createSIPDispatchRule,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: RequestDesc[livekit.CreateSIPDispatchRuleRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete SIP Dispatch Rule",
							Action:    deleteSIPDispatchRule,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "participant",
					Usage:    "SIP Participant management",
					Category: sipParticipantCategory,
					Subcommands: []*cli.Command{
						{
							Name:      "create",
							Usage:     "Create a SIP Participant",
							Action:    createSIPParticipant,
							Flags:     withDefaultFlags(),
							Args:      true,
							ArgsUsage: RequestDesc[livekit.CreateSIPParticipantRequest](),
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
			Action:   createSIPTrunkLegacy,
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
			Action:   listSipTrunk,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
			Hidden:   true, // deprecated: use "sip trunk delete"
			Name:     "delete-sip-trunk",
			Usage:    "Delete SIP Trunk",
			Action:   deleteSIPTrunkLegacy,
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
			Action:   createSIPDispatchRuleLegacy,
			Category: sipCategory,
			Flags: withDefaultFlags(
				RequestFlag[livekit.CreateSIPDispatchRuleRequest](),
			),
		},
		{
			Hidden:   true, // deprecated: use "sip dispatch list"
			Name:     "list-sip-dispatch-rule",
			Usage:    "List all SIP Dispatch Rule",
			Action:   listSipDispatchRule,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
			Hidden:   true, // deprecated: use "sip dispatch delete"
			Name:     "delete-sip-dispatch-rule",
			Usage:    "Delete SIP Dispatch Rule",
			Action:   deleteSIPDispatchRuleLegacy,
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
			Action:   createSIPParticipantLegacy,
			Category: sipCategory,
			Flags: withDefaultFlags(
				RequestFlag[livekit.CreateSIPParticipantRequest](),
			),
		},
	}
)

func createSIPClient(c *cli.Context) (*lksdk.SIPClient, error) {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return nil, err
	}
	return lksdk.NewSIPClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...), nil
}

func createSIPTrunkLegacy(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	//lint:ignore SA1019 we still support it
	return createAndPrintLegacy(c, cli.CreateSIPTrunk, printSIPTrunkID)
}

func createSIPInboundTrunk(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintReqs(c, cli.CreateSIPInboundTrunk, printSIPInboundTrunkID)
}

func createSIPOutboundTrunk(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintReqs(c, cli.CreateSIPOutboundTrunk, printSIPOutboundTrunkID)
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
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	//lint:ignore SA1019 we still support it
	return listAndPrint(c, cli.ListSIPTrunk, &livekit.ListSIPTrunkRequest{}, []string{
		"SipTrunkId", "Name", "Kind", "Number",
		"AllowAddresses", "AllowNumbers", "InboundAuth",
		"OutboundAddress", "OutboundAuth",
		"Metadata",
	}, func(item *livekit.SIPTrunkInfo) []string {
		inboundNumbers := item.InboundNumbers
		for _, re := range item.InboundNumbersRegex {
			inboundNumbers = append(inboundNumbers, "regexp("+re+")")
		}
		return []string{
			item.SipTrunkId, item.Name, strings.TrimPrefix(item.Kind.String(), "TRUNK_"), item.OutboundNumber,
			strings.Join(item.InboundAddresses, ","), strings.Join(inboundNumbers, ","), userPass(item.InboundUsername, item.InboundPassword != ""),
			item.OutboundAddress, userPass(item.OutboundUsername, item.OutboundPassword != ""),
			item.Metadata,
		}
	})
}

func listSipInboundTrunk(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return listAndPrint(c, cli.ListSIPInboundTrunk, &livekit.ListSIPInboundTrunkRequest{}, []string{
		"SipTrunkId", "Name", "Numbers",
		"AllowedAddresses", "AllowedNumbers",
		"Authentication",
		"Metadata",
	}, func(item *livekit.SIPInboundTrunkInfo) []string {
		return []string{
			item.SipTrunkId, item.Name, strings.Join(item.Numbers, ","),
			strings.Join(item.AllowedAddresses, ","), strings.Join(item.AllowedNumbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			item.Metadata,
		}
	})
}

func listSipOutboundTrunk(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return listAndPrint(c, cli.ListSIPOutboundTrunk, &livekit.ListSIPOutboundTrunkRequest{}, []string{
		"SipTrunkId", "Name",
		"Address", "Transport",
		"Numbers",
		"Authentication",
		"Metadata",
	}, func(item *livekit.SIPOutboundTrunkInfo) []string {
		return []string{
			item.SipTrunkId, item.Name,
			item.Address, strings.TrimPrefix(item.Transport.String(), "SIP_TRANSPORT_"),
			strings.Join(item.Numbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			item.Metadata,
		}
	})
}

func deleteSIPTrunk(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return forEachID(c, func(ctx context.Context, id string) error {
		info, err := cli.DeleteSIPTrunk(c.Context, &livekit.DeleteSIPTrunkRequest{
			SipTrunkId: id,
		})
		if err != nil {
			return err
		}
		printSIPTrunkID(info)
		return nil
	})
}

func deleteSIPTrunkLegacy(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	info, err := cli.DeleteSIPTrunk(c.Context, &livekit.DeleteSIPTrunkRequest{
		SipTrunkId: c.String("id"),
	})
	if err != nil {
		return err
	}
	printSIPTrunkID(info)
	return nil
}

func printSIPTrunkID(info *livekit.SIPTrunkInfo) {
	fmt.Printf("SIPTrunkID: %v\n", info.GetSipTrunkId())
}

func printSIPInboundTrunkID(info *livekit.SIPInboundTrunkInfo) {
	fmt.Printf("SIPTrunkID: %v\n", info.GetSipTrunkId())
}

func printSIPOutboundTrunkID(info *livekit.SIPOutboundTrunkInfo) {
	fmt.Printf("SIPTrunkID: %v\n", info.GetSipTrunkId())
}

func createSIPDispatchRule(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintReqs(c, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func createSIPDispatchRuleLegacy(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintLegacy(c, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func listSipDispatchRule(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return listAndPrint(c, cli.ListSIPDispatchRule, &livekit.ListSIPDispatchRuleRequest{}, []string{
		"SipDispatchRuleId", "Name", "SipTrunks", "Type", "RoomName", "Pin", "HidePhone", "Metadata",
	}, func(item *livekit.SIPDispatchRuleInfo) []string {
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
		return []string{item.SipDispatchRuleId, item.Name, trunks, typ, room, pin, strconv.FormatBool(item.HidePhoneNumber), item.Metadata}
	})
}

func deleteSIPDispatchRule(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return forEachID(c, func(ctx context.Context, id string) error {
		info, err := cli.DeleteSIPDispatchRule(c.Context, &livekit.DeleteSIPDispatchRuleRequest{
			SipDispatchRuleId: id,
		})
		if err != nil {
			return err
		}
		printSIPDispatchRuleID(info)
		return nil
	})
}

func deleteSIPDispatchRuleLegacy(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	info, err := cli.DeleteSIPDispatchRule(c.Context, &livekit.DeleteSIPDispatchRuleRequest{
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
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintReqs(c, func(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		return cli.CreateSIPParticipant(ctx, req)
	}, printSIPParticipantInfo)
}

func createSIPParticipantLegacy(c *cli.Context) error {
	cli, err := createSIPClient(c)
	if err != nil {
		return err
	}
	return createAndPrintLegacy(c, func(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		return cli.CreateSIPParticipant(ctx, req)
	}, printSIPParticipantInfo)
}

func printSIPParticipantInfo(info *livekit.SIPParticipantInfo) {
	fmt.Printf("SIPCallID: %v\n", info.SipCallId)
	fmt.Printf("ParticipantID: %v\n", info.ParticipantId)
	fmt.Printf("ParticipantIdentity: %v\n", info.ParticipantIdentity)
	fmt.Printf("RoomName: %v\n", info.RoomName)
}
