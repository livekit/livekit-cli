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

	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const sipCategory = "SIP"

var (
	SIPCommands = []*cli.Command{
		{
			Name:     "create-sip-trunk",
			Usage:    "Create a SIP Trunk",
			Before:   createSIPClient,
			Action:   createSIPTrunk,
			Category: sipCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateSIPTrunkRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "list-sip-trunk",
			Usage:    "List all SIP trunk",
			Before:   createSIPClient,
			Action:   listSipTrunk,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
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
			Name:     "create-sip-dispatch-rule",
			Usage:    "Create a SIP Dispatch Rule",
			Before:   createSIPClient,
			Action:   createSIPDispatchRule,
			Category: sipCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateSIPDispatchRuleRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
			),
		},
		{
			Name:     "list-sip-dispatch-rule",
			Usage:    "List all SIP Dispatch Rule",
			Before:   createSIPClient,
			Action:   listSipDispatchRule,
			Category: sipCategory,
			Flags:    withDefaultFlags(),
		},
		{
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
			Name:     "create-sip-participant",
			Usage:    "Create a SIP Participant",
			Before:   createSIPClient,
			Action:   createSIPParticipant,
			Category: sipCategory,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "request",
					Usage:    "CreateSIPParticipantRequest as json file (see livekit-cli/examples)",
					Required: true,
				},
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

	sipClient = lksdk.NewSIPClient(pc.URL, pc.APIKey, pc.APISecret)
	return nil
}

func createSIPTrunk(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := os.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.CreateSIPTrunkRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := sipClient.CreateSIPTrunk(context.Background(), req)
	if err != nil {
		return err
	}

	printSIPTrunkInfo(info)
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
	res, err := sipClient.ListSIPTrunk(context.Background(), &livekit.ListSIPTrunkRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"SipTrunkId",
		"InboundAddresses", "InboundNumbers", "InboundAuth",
		"OutboundAddress", "OutboundNumber", "OutboundAuth",
	})
	for _, item := range res.Items {
		if item == nil {
			continue
		}
		inboundNumbers := item.InboundNumbers
		//lint:ignore SA1019 we still want to display old ones
		for _, re := range item.InboundNumbersRegex {
			inboundNumbers = append(inboundNumbers, "regexp("+re+")")
		}

		table.Append([]string{
			item.SipTrunkId,
			strings.Join(item.InboundAddresses, ","), strings.Join(inboundNumbers, ","), userPass(item.InboundUsername, item.InboundPassword != ""),
			item.OutboundAddress, item.OutboundNumber, userPass(item.OutboundUsername, item.OutboundPassword != ""),
		})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func deleteSIPTrunk(c *cli.Context) error {
	info, err := sipClient.DeleteSIPTrunk(context.Background(), &livekit.DeleteSIPTrunkRequest{
		SipTrunkId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printSIPTrunkInfo(info)
	return nil
}

func printSIPTrunkInfo(info *livekit.SIPTrunkInfo) {
	fmt.Printf("SIPTrunkID: %v\n", info.SipTrunkId)
}

func createSIPDispatchRule(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := os.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.CreateSIPDispatchRuleRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	info, err := sipClient.CreateSIPDispatchRule(context.Background(), req)
	if err != nil {
		return err
	}

	printSIPDispatchRuleInfo(info)
	return nil
}

func listSipDispatchRule(c *cli.Context) error {
	res, err := sipClient.ListSIPDispatchRule(context.Background(), &livekit.ListSIPDispatchRuleRequest{})
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"SipDispatchRuleId", "SipTrunks", "Type", "RoomName", "Pin", "HidePhone"})
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
		table.Append([]string{item.SipDispatchRuleId, trunks, typ, room, pin, strconv.FormatBool(item.HidePhoneNumber)})
	}
	table.Render()

	if c.Bool("verbose") {
		PrintJSON(res)
	}

	return nil
}

func deleteSIPDispatchRule(c *cli.Context) error {
	info, err := sipClient.DeleteSIPDispatchRule(context.Background(), &livekit.DeleteSIPDispatchRuleRequest{
		SipDispatchRuleId: c.String("id"),
	})
	if err != nil {
		return err
	}

	printSIPDispatchRuleInfo(info)
	return nil
}

func printSIPDispatchRuleInfo(info *livekit.SIPDispatchRuleInfo) {
	fmt.Printf("SIPDispatchRuleID: %v\n", info.SipDispatchRuleId)
}

func createSIPParticipant(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := os.ReadFile(reqFile)
	if err != nil {
		return err
	}

	req := &livekit.CreateSIPParticipantRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
	// Thus, we must set a higher deadline for it.
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
	fmt.Printf("ParticipantID: %v\n", info.ParticipantId)
	fmt.Printf("ParticipantIdentity: %v\n", info.ParticipantIdentity)
	fmt.Printf("RoomName: %v\n", info.RoomName)
}
