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
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/emptypb"
)

//lint:file-ignore SA1019 we still support older APIs for compatibility

const (
	sipCategory            = "I/O"
	sipTrunkCategory       = "Trunks"
	sipDispatchCategory    = "Dispatch Rules"
	sipParticipantCategory = "Participants"
)

var (
	SIPCommands = []*cli.Command{
		{
			Name:     "sip",
			Usage:    "Manage SIP Trunks, Dispatch Rules, and Participants",
			Category: sipCategory,
			Commands: []*cli.Command{
				{
					Name:     "inbound",
					Aliases:  []string{"in", "inbound-trunk"},
					Usage:    "Inbound SIP Trunk management",
					Category: sipTrunkCategory,
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all inbound SIP Trunks",
							Action: listSipInboundTrunk,
						},
						{
							Name:      "create",
							Usage:     "Create an inbound SIP Trunk",
							Action:    createSIPInboundTrunk,
							ArgsUsage: RequestDesc[livekit.CreateSIPInboundTrunkRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete a SIP Trunk",
							Action:    deleteSIPTrunk,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "outbound",
					Aliases:  []string{"out", "outbound-trunk"},
					Usage:    "Outbound SIP Trunk management",
					Category: sipTrunkCategory,
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all outbound SIP Trunk",
							Action: listSipOutboundTrunk,
						},
						{
							Name:      "create",
							Usage:     "Create a outbound SIP Trunk",
							Action:    createSIPOutboundTrunk,
							ArgsUsage: RequestDesc[livekit.CreateSIPOutboundTrunkRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete SIP Trunk",
							Action:    deleteSIPTrunk,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "dispatch",
					Usage:    "SIP Dispatch Rule management",
					Aliases:  []string{"dispatch-rule"},
					Category: sipDispatchCategory,
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all SIP Dispatch Rule",
							Action: listSipDispatchRule,
						},
						{
							Name:      "create",
							Usage:     "Create a SIP Dispatch Rule",
							Action:    createSIPDispatchRule,
							ArgsUsage: RequestDesc[livekit.CreateSIPDispatchRuleRequest](),
						},
						{
							Name:      "delete",
							Usage:     "Delete SIP Dispatch Rule",
							Action:    deleteSIPDispatchRule,
							ArgsUsage: "SIPTrunk ID to delete",
						},
					},
				},
				{
					Name:     "participant",
					Usage:    "SIP Participant management",
					Category: sipParticipantCategory,
					Commands: []*cli.Command{
						{
							Name:      "create",
							Usage:     "Create a SIP Participant",
							Action:    createSIPParticipant,
							ArgsUsage: RequestDesc[livekit.CreateSIPParticipantRequest](),
						},
						{
							Name:      "transfer",
							Usage:     "Transfer a SIP Participant",
							Action:    transferSIPParticipant,
							ArgsUsage: RequestDesc[livekit.TransferSIPParticipantRequest](),
						},
					},
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden:   true, // deprecated: use `sip trunk create`
			Name:     "create-sip-trunk",
			Usage:    "Create a SIP Trunk",
			Action:   createSIPTrunkLegacy,
			Category: sipCategory,
			Flags: []cli.Flag{
				//lint:ignore SA1019 we still support it
				RequestFlag[livekit.CreateSIPTrunkRequest](),
			},
		},
		{
			Hidden:   true, // deprecated: use `sip trunk list`
			Name:     "list-sip-trunk",
			Usage:    "List all SIP trunk",
			Action:   listSipTrunk,
			Category: sipCategory,
		},
		{
			Hidden:   true, // deprecated: use `sip trunk delete`
			Name:     "delete-sip-trunk",
			Usage:    "Delete SIP Trunk",
			Action:   deleteSIPTrunkLegacy,
			Category: sipCategory,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPTrunk ID",
					Required: true,
				},
			},
		},
		{
			Hidden:   true, // deprecated: use `sip dispatch create`
			Name:     "create-sip-dispatch-rule",
			Usage:    "Create a SIP Dispatch Rule",
			Action:   createSIPDispatchRuleLegacy,
			Category: sipCategory,
			Flags: []cli.Flag{
				RequestFlag[livekit.CreateSIPDispatchRuleRequest](),
			},
		},
		{
			Hidden:   true, // deprecated: use `sip dispatch list`
			Name:     "list-sip-dispatch-rule",
			Usage:    "List all SIP Dispatch Rule",
			Action:   listSipDispatchRule,
			Category: sipCategory,
		},
		{
			Hidden:   true, // deprecated: use `sip dispatch delete`
			Name:     "delete-sip-dispatch-rule",
			Usage:    "Delete SIP Dispatch Rule",
			Action:   deleteSIPDispatchRuleLegacy,
			Category: sipCategory,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPDispatchRule ID",
					Required: true,
				},
			},
		},
		{
			Hidden:   true, // deprecated: use `sip participant create`
			Name:     "create-sip-participant",
			Usage:    "Create a SIP Participant",
			Action:   createSIPParticipantLegacy,
			Category: sipCategory,
			Flags: []cli.Flag{
				RequestFlag[livekit.CreateSIPParticipantRequest](),
			},
		},
	}
)

func createSIPClient(cmd *cli.Command) (*lksdk.SIPClient, error) {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return nil, err
	}
	return lksdk.NewSIPClient(pc.URL, pc.APIKey, pc.APISecret, withDefaultClientOpts(pc)...), nil
}

func createSIPTrunkLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	//lint:ignore SA1019 we still support it
	return createAndPrintLegacy(ctx, cmd, cli.CreateSIPTrunk, printSIPTrunkID)
}

func createSIPInboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, cli.CreateSIPInboundTrunk, printSIPInboundTrunkID)
}

func createSIPOutboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, cli.CreateSIPOutboundTrunk, printSIPOutboundTrunkID)
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

func listSipTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	//lint:ignore SA1019 we still support it
	return listAndPrint(ctx, cmd, cli.ListSIPTrunk, &livekit.ListSIPTrunkRequest{}, []string{
		"SipTrunkID", "Name", "Kind", "Number",
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

func listSipInboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPInboundTrunk, &livekit.ListSIPInboundTrunkRequest{}, []string{
		"SipTrunkID", "Name", "Numbers",
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

func listSipOutboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPOutboundTrunk, &livekit.ListSIPOutboundTrunkRequest{}, []string{
		"SipTrunkID", "Name",
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

func deleteSIPTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return forEachID(ctx, cmd, func(ctx context.Context, id string) error {
		info, err := cli.DeleteSIPTrunk(ctx, &livekit.DeleteSIPTrunkRequest{
			SipTrunkId: id,
		})
		if err != nil {
			return err
		}
		printSIPTrunkID(info)
		return nil
	})
}

func deleteSIPTrunkLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	info, err := cli.DeleteSIPTrunk(ctx, &livekit.DeleteSIPTrunkRequest{
		SipTrunkId: cmd.String("id"),
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

func createSIPDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func createSIPDispatchRuleLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintLegacy(ctx, cmd, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func listSipDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPDispatchRule, &livekit.ListSIPDispatchRuleRequest{}, []string{
		"SipDispatchRuleID", "Name", "SipTrunks", "Type", "RoomName", "Pin", "HidePhone", "Metadata",
	}, func(item *livekit.SIPDispatchRuleInfo) []string {
		var room, typ, pin string
		switch r := item.GetRule().GetRule().(type) {
		case *livekit.SIPDispatchRule_DispatchRuleDirect:
			room = r.DispatchRuleDirect.RoomName
			pin = r.DispatchRuleDirect.Pin
			typ = "Direct"
		case *livekit.SIPDispatchRule_DispatchRuleIndividual:
			room = r.DispatchRuleIndividual.RoomPrefix + "_<caller>_<random>"
			pin = r.DispatchRuleIndividual.Pin
			typ = "Individual (Caller)"
		case *livekit.SIPDispatchRule_DispatchRuleCallee:
			room = r.DispatchRuleCallee.RoomPrefix + "<callee>"
			if r.DispatchRuleCallee.Randomize {
				room += "_<random>"
			}
			pin = r.DispatchRuleCallee.Pin
			typ = "Callee"
		}
		trunks := strings.Join(item.TrunkIds, ",")
		if trunks == "" {
			trunks = "<any>"
		}
		return []string{item.SipDispatchRuleId, item.Name, trunks, typ, room, pin, strconv.FormatBool(item.HidePhoneNumber), item.Metadata}
	})
}

func deleteSIPDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return forEachID(ctx, cmd, func(ctx context.Context, id string) error {
		info, err := cli.DeleteSIPDispatchRule(ctx, &livekit.DeleteSIPDispatchRuleRequest{
			SipDispatchRuleId: id,
		})
		if err != nil {
			return err
		}
		printSIPDispatchRuleID(info)
		return nil
	})
}

func deleteSIPDispatchRuleLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	info, err := cli.DeleteSIPDispatchRule(ctx, &livekit.DeleteSIPDispatchRuleRequest{
		SipDispatchRuleId: cmd.String("id"),
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

func createSIPParticipant(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		return cli.CreateSIPParticipant(ctx, req)
	}, printSIPParticipantInfo)
}

func createSIPParticipantLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintLegacy(ctx, cmd, func(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		return cli.CreateSIPParticipant(ctx, req)
	}, printSIPParticipantInfo)
}

func transferSIPParticipant(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(ctx context.Context, req *livekit.TransferSIPParticipantRequest) (*emptypb.Empty, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		return cli.TransferSIPParticipant(ctx, req)
	}, func(r *emptypb.Empty) {})
}

func printSIPParticipantInfo(info *livekit.SIPParticipantInfo) {
	fmt.Printf("SIPCallID: %v\n", info.SipCallId)
	fmt.Printf("ParticipantID: %v\n", info.ParticipantId)
	fmt.Printf("ParticipantIdentity: %v\n", info.ParticipantIdentity)
	fmt.Printf("RoomName: %v\n", info.RoomName)
}
