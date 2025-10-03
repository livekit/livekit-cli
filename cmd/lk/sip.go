// Copyright 2023-2024 LiveKit, Inc.
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
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/durationpb"
)

//lint:file-ignore SA1019 we still support older APIs for compatibility

var (
	SIPCommands = []*cli.Command{
		{
			Name:  "sip",
			Usage: "Manage SIP Trunks, Dispatch Rules, and Participants",
			Commands: []*cli.Command{
				{
					Name:    "inbound",
					Aliases: []string{"in", "inbound-trunk"},
					Usage:   "Inbound SIP Trunk management",
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all inbound SIP Trunks",
							Action: listSipInboundTrunk,
							Flags:  []cli.Flag{jsonFlag},
						},
						{
							Name:      "create",
							Usage:     "Create an inbound SIP Trunk",
							Action:    createSIPInboundTrunk,
							ArgsUsage: RequestDesc[livekit.CreateSIPInboundTrunkRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the trunk",
								},
								&cli.StringSliceFlag{
									Name:  "numbers",
									Usage: "Sets a list of numbers for the trunk",
								},
								&cli.StringFlag{
									Name:  "media-enc",
									Usage: "Sets media encryption for the trunk",
								},
								&cli.StringFlag{
									Name:  "auth-user",
									Usage: "Set username for authentication",
								},
								&cli.StringFlag{
									Name:  "auth-pass",
									Usage: "Set password for authentication",
								},
							},
						},
						{
							Name:      "update",
							Usage:     "Update an inbound SIP Trunk",
							Action:    updateSIPInboundTrunk,
							ArgsUsage: RequestDesc[livekit.UpdateSIPInboundTrunkRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "id",
									Usage: "ID for the trunk to update",
								},
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the trunk",
								},
								&cli.StringSliceFlag{
									Name:  "numbers",
									Usage: "Sets a new list of numbers for the trunk",
								},
								&cli.StringFlag{
									Name:  "auth-user",
									Usage: "Set username for authentication",
								},
								&cli.StringFlag{
									Name:  "auth-pass",
									Usage: "Set password for authentication",
								},
							},
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
					Name:    "outbound",
					Aliases: []string{"out", "outbound-trunk"},
					Usage:   "Outbound SIP Trunk management",
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all outbound SIP Trunk",
							Action: listSipOutboundTrunk,
							Flags:  []cli.Flag{jsonFlag},
						},
						{
							Name:      "create",
							Usage:     "Create an outbound SIP Trunk",
							Action:    createSIPOutboundTrunk,
							ArgsUsage: RequestDesc[livekit.CreateSIPOutboundTrunkRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the trunk",
								},
								&cli.StringFlag{
									Name:  "address",
									Usage: "Sets a destination address for the trunk",
								},
								&cli.StringFlag{
									Name:  "transport",
									Usage: "Sets a transport for the trunk",
								},
								&cli.StringFlag{
									Name:  "destination-country",
									Usage: "Sets a destination country for the trunk as ISO 3166-1 alpha-2 (https://en.wikipedia.org/wiki/ISO_3166-1_alpha-2)",
								},
								&cli.StringFlag{
									Name:  "media-enc",
									Usage: "Sets media encryption for the trunk",
								},
								&cli.StringSliceFlag{
									Name:  "numbers",
									Usage: "Sets a list of numbers for the trunk",
								},
								&cli.StringFlag{
									Name:  "auth-user",
									Usage: "Set username for authentication",
								},
								&cli.StringFlag{
									Name:  "auth-pass",
									Usage: "Set password for authentication",
								},
							},
						},
						{
							Name:      "update",
							Usage:     "Update an outbound SIP Trunk",
							Action:    updateSIPOutboundTrunk,
							ArgsUsage: RequestDesc[livekit.UpdateSIPOutboundTrunkRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "id",
									Usage: "ID for the trunk to update",
								},
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the trunk",
								},
								&cli.StringFlag{
									Name:  "address",
									Usage: "Sets a new destination address for the trunk",
								},
								&cli.StringFlag{
									Name:  "transport",
									Usage: "Sets a new transport for the trunk",
								},
								&cli.StringFlag{
									Name:  "destination-country",
									Usage: "Sets a destination country for the trunk as ISO 3166-1 alpha-2 (https://en.wikipedia.org/wiki/ISO_3166-1_alpha-2)",
								},
								&cli.StringSliceFlag{
									Name:  "numbers",
									Usage: "Sets a new list of numbers for the trunk",
								},
								&cli.StringFlag{
									Name:  "auth-user",
									Usage: "Set username for authentication",
								},
								&cli.StringFlag{
									Name:  "auth-pass",
									Usage: "Set password for authentication",
								},
							},
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
					Name:    "dispatch",
					Usage:   "SIP Dispatch Rule management",
					Aliases: []string{"dispatch-rule"},
					Commands: []*cli.Command{
						{
							Name:   "list",
							Usage:  "List all SIP Dispatch Rule",
							Action: listSipDispatchRule,
							Flags:  []cli.Flag{jsonFlag},
						},
						{
							Name:      "create",
							Usage:     "Create a SIP Dispatch Rule",
							Action:    createSIPDispatchRule,
							ArgsUsage: RequestDesc[livekit.CreateSIPDispatchRuleRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the dispatch rule",
								},
								&cli.StringSliceFlag{
									Name:  "trunks",
									Usage: "Sets a list of trunks for the dispatch rule",
								},
							},
							MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
								{
									Flags: [][]cli.Flag{
										{
											&cli.StringFlag{
												Name:  "direct",
												Usage: "Sets a direct dispatch to a specified room",
											},
										},
										{
											&cli.StringFlag{
												Name:    "caller",
												Aliases: []string{"individual"},
												Usage:   "Sets a individual caller dispatch to a new room with a specific prefix",
											},
										},
										{
											&cli.StringFlag{
												Name:  "callee",
												Usage: "Sets a callee number dispatch to a new room with a specific prefix",
											},
											&cli.BoolFlag{
												Name:  "randomize",
												Usage: "Randomize room name",
											},
										},
									},
								},
							},
						},
						{
							Name:      "update",
							Usage:     "Update a SIP Dispatch Rule",
							Action:    updateSIPDispatchRule,
							ArgsUsage: RequestDesc[livekit.UpdateSIPDispatchRuleRequest](),
							Flags: []cli.Flag{
								&cli.StringFlag{
									Name:  "id",
									Usage: "ID for the rule to update",
								},
								&cli.StringFlag{
									Name:  "name",
									Usage: "Sets a new name for the rule",
								},
								&cli.StringSliceFlag{
									Name:  "trunks",
									Usage: "Sets a new list of trunk IDs",
								},
							},
							MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
								{
									Flags: [][]cli.Flag{
										{
											&cli.StringFlag{
												Name:  "direct",
												Usage: "Sets a direct dispatch to a specified room",
											},
										},
										{
											&cli.StringFlag{
												Name:    "caller",
												Aliases: []string{"individual"},
												Usage:   "Sets a individual caller dispatch to a new room with a specific prefix",
											},
										},
										{
											&cli.StringFlag{
												Name:  "callee",
												Usage: "Sets a callee number dispatch to a new room with a specific prefix",
											},
											&cli.BoolFlag{
												Name:  "randomize",
												Usage: "Randomize room name",
											},
										},
									},
								},
							},
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
					Name:  "participant",
					Usage: "SIP Participant management",
					Commands: []*cli.Command{
						{
							Name:      "create",
							Usage:     "Create a SIP Participant",
							Action:    createSIPParticipant,
							ArgsUsage: RequestDesc[livekit.CreateSIPParticipantRequest](),
							Flags: []cli.Flag{
								optional(roomFlag),
								optional(identityFlag),
								&cli.StringFlag{
									Name:  "trunk",
									Usage: "`SIP_TRUNK_ID` to use for the call (overrides json config)",
								},
								&cli.StringFlag{
									Name:  "number",
									Usage: "`SIP_NUMBER` to use for the call (overrides json config)",
								},
								&cli.StringFlag{
									Name:  "call",
									Usage: "`SIP_CALL_TO` number to use (overrides json config)",
								},
								&cli.StringFlag{
									Name:  "name",
									Usage: "`PARTICIPANT_NAME` to use (overrides json config)",
								},
								&cli.StringFlag{
									Name:  "display-name",
									Usage: "`DISPLAY_NAME` for the 'From' SIP header (overrides json config)",
								},
								&cli.BoolFlag{
									Name:  "no-display-name",
									Usage: "Avoid defaulting the display name, and do a CNAM lookup instead (overrides display-name setting)",
								},
								&cli.BoolFlag{
									Name:  "wait",
									Usage: "wait for the call to dial (overrides json config)",
								},
								&cli.DurationFlag{
									Name:  "timeout",
									Usage: "timeout for the call to dial",
									Value: 80 * time.Second,
								},
								&cli.StringSliceFlag{
									Name:  "header",
									Usage: "Custom SIP header in format 'Key:Value' (can be specified multiple times)",
								},
							},
						},
						{
							Name:   "transfer",
							Usage:  "Transfer a SIP Participant",
							Action: transferSIPParticipant,
							Flags: []cli.Flag{
								roomFlag,
								identityFlag,
								&cli.StringFlag{
									Name:     "to",
									Required: true,
									Usage:    "`SIP URL` to transfer the call to. Use 'tel:<phone number>' to transfer to a phone",
								},
								&cli.BoolFlag{
									Name:  "play-dialtone",
									Usage: "if set, a dial tone will be played to the SIP participant while the transfer is being attempted",
								},
							},
						},
					},
				},
			},
		},

		// Deprecated commands kept for compatibility
		{
			Hidden: true, // deprecated: use `sip trunk list`
			Name:   "list-sip-trunk",
			Usage:  "List all SIP trunk",
			Action: listSipTrunk,
		},
		{
			Hidden: true, // deprecated: use `sip trunk delete`
			Name:   "delete-sip-trunk",
			Usage:  "Delete SIP Trunk",
			Action: deleteSIPTrunkLegacy,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPTrunk ID",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `sip dispatch create`
			Name:   "create-sip-dispatch-rule",
			Usage:  "Create a SIP Dispatch Rule",
			Action: createSIPDispatchRuleLegacy,
			Flags: []cli.Flag{
				RequestFlag[livekit.CreateSIPDispatchRuleRequest](),
			},
		},
		{
			Hidden: true, // deprecated: use `sip dispatch list`
			Name:   "list-sip-dispatch-rule",
			Usage:  "List all SIP Dispatch Rule",
			Action: listSipDispatchRule,
		},
		{
			Hidden: true, // deprecated: use `sip dispatch delete`
			Name:   "delete-sip-dispatch-rule",
			Usage:  "Delete SIP Dispatch Rule",
			Action: deleteSIPDispatchRuleLegacy,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "SIPDispatchRule ID",
					Required: true,
				},
			},
		},
		{
			Hidden: true, // deprecated: use `sip participant create`
			Name:   "create-sip-participant",
			Usage:  "Create a SIP Participant",
			Action: createSIPParticipantLegacy,
			Flags: []cli.Flag{
				RequestFlag[livekit.CreateSIPParticipantRequest](),
			},
		},
	}
)

func listUpdateFlag(cmd *cli.Command, setName string) *livekit.ListUpdate {
	if !cmd.IsSet(setName) {
		return nil
	}
	val := cmd.StringSlice(setName)
	if len(val) == 1 && val[0] == "" {
		val = []string{}
	}
	return &livekit.ListUpdate{Set: val}
}

func listSetFlag(cmd *cli.Command, setName string) ([]string, bool) {
	if !cmd.IsSet(setName) {
		return nil, false
	}
	val := cmd.StringSlice(setName)
	if len(val) == 1 && val[0] == "" {
		val = nil
	}
	return val, true
}

func createSIPClient(ctx context.Context, cmd *cli.Command) (*lksdk.SIPClient, error) {
	_, err := requireProject(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return lksdk.NewSIPClient(project.URL, project.APIKey, project.APISecret, withDefaultClientOpts(project)...), nil
}

func createSIPInboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(req *livekit.CreateSIPInboundTrunkRequest) error {
		if req.Trunk == nil {
			req.Trunk = new(livekit.SIPInboundTrunkInfo)
		}
		p := req.Trunk
		if val := cmd.String("name"); val != "" {
			p.Name = val
		}
		if val, ok := listSetFlag(cmd, "numbers"); ok {
			p.Numbers = val
		}
		if val := cmd.String("media-enc"); val != "" {
			val = strings.ToUpper(val)
			v, ok := livekit.SIPMediaEncryption_value[val]
			if !ok {
				v, ok = livekit.SIPMediaEncryption_value["SIP_MEDIA_ENCRYPT_"+val]
			}
			if !ok {
				return fmt.Errorf("invalid value for SIP media encryption: %q", val)
			}
			p.MediaEncryption = livekit.SIPMediaEncryption(v)
		}
		if val := cmd.String("auth-user"); val != "" {
			p.AuthUsername = val
		}
		if val := cmd.String("auth-pass"); val != "" {
			p.AuthPassword = val
		}
		return nil
	}, cli.CreateSIPInboundTrunk, printSIPInboundTrunkID)
}

func updateSIPInboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	id := cmd.String("id")
	if cmd.Args().Len() > 1 {
		return errors.New("expected one JSON file or flags")
	}
	if cmd.Args().Len() == 1 {
		// Update from the JSON
		req, err := ReadRequestFileOrLiteral[livekit.SIPInboundTrunkInfo](cmd.Args().First())
		if err != nil {
			return fmt.Errorf("could not read request: %w", err)
		}
		if id == "" {
			id = req.SipTrunkId
		}
		req.SipTrunkId = ""
		if id == "" {
			return errors.New("no ID specified, use flag or set it in JSON")
		}
		info, err := cli.UpdateSIPInboundTrunk(ctx, &livekit.UpdateSIPInboundTrunkRequest{
			SipTrunkId: id,
			Action: &livekit.UpdateSIPInboundTrunkRequest_Replace{
				Replace: req,
			},
		})
		if err != nil {
			return err
		}
		printSIPInboundTrunkID(info)
		return err
	}
	// Update from flags
	if id == "" {
		return errors.New("no ID specified")
	}
	req := &livekit.SIPInboundTrunkUpdate{}
	if val := cmd.String("name"); val != "" {
		req.Name = &val
	}
	if val := cmd.String("auth-user"); val != "" {
		req.AuthUsername = &val
	}
	if val := cmd.String("auth-pass"); val != "" {
		req.AuthPassword = &val
	}
	req.Numbers = listUpdateFlag(cmd, "numbers")
	info, err := cli.UpdateSIPInboundTrunk(ctx, &livekit.UpdateSIPInboundTrunkRequest{
		SipTrunkId: id,
		Action: &livekit.UpdateSIPInboundTrunkRequest_Update{
			Update: req,
		},
	})
	if err != nil {
		return err
	}
	printSIPInboundTrunkID(info)
	return err
}

func createSIPOutboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(req *livekit.CreateSIPOutboundTrunkRequest) error {
		if req.Trunk == nil {
			req.Trunk = new(livekit.SIPOutboundTrunkInfo)
		}
		p := req.Trunk
		if val := cmd.String("name"); val != "" {
			p.Name = val
		}
		if val := cmd.String("address"); val != "" {
			p.Address = val
		}
		if val := cmd.String("transport"); val != "" {
			val = strings.ToUpper(val)
			v, ok := livekit.SIPTransport_value[val]
			if !ok {
				v, ok = livekit.SIPTransport_value["SIP_TRANSPORT_"+val]
			}
			if !ok {
				return fmt.Errorf("invalid value for SIP transport: %q", val)
			}
			p.Transport = livekit.SIPTransport(v)
		}
		if val := cmd.String("destination-country"); val != "" {
			p.DestinationCountry = val
		}
		if val := cmd.String("media-enc"); val != "" {
			val = strings.ToUpper(val)
			v, ok := livekit.SIPMediaEncryption_value[val]
			if !ok {
				v, ok = livekit.SIPMediaEncryption_value["SIP_MEDIA_ENCRYPT_"+val]
			}
			if !ok {
				return fmt.Errorf("invalid value for SIP media encryption: %q", val)
			}
			p.MediaEncryption = livekit.SIPMediaEncryption(v)
		}
		if val, ok := listSetFlag(cmd, "numbers"); ok {
			p.Numbers = val
		}
		if val := cmd.String("auth-user"); val != "" {
			p.AuthUsername = val
		}
		if val := cmd.String("auth-pass"); val != "" {
			p.AuthPassword = val
		}
		return nil
	}, cli.CreateSIPOutboundTrunk, printSIPOutboundTrunkID)
}

func updateSIPOutboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	id := cmd.String("id")
	if cmd.Args().Len() > 1 {
		return errors.New("expected one JSON file or flags")
	}
	if cmd.Args().Len() == 1 {
		// Update from the JSON
		req, err := ReadRequestFileOrLiteral[livekit.SIPOutboundTrunkInfo](cmd.Args().First())
		if err != nil {
			return fmt.Errorf("could not read request: %w", err)
		}
		if id == "" {
			id = req.SipTrunkId
		}
		req.SipTrunkId = ""
		if id == "" {
			return errors.New("no ID specified, use flag or set it in JSON")
		}
		info, err := cli.UpdateSIPOutboundTrunk(ctx, &livekit.UpdateSIPOutboundTrunkRequest{
			SipTrunkId: id,
			Action: &livekit.UpdateSIPOutboundTrunkRequest_Replace{
				Replace: req,
			},
		})
		if err != nil {
			return err
		}
		printSIPOutboundTrunkID(info)
		return err
	}
	// Update from flags
	if id == "" {
		return errors.New("no ID specified")
	}
	req := &livekit.SIPOutboundTrunkUpdate{}
	if val := cmd.String("name"); val != "" {
		req.Name = &val
	}
	if val := cmd.String("address"); val != "" {
		req.Address = &val
	}
	if val := cmd.String("transport"); val != "" {
		val = strings.ToUpper(val)
		if !strings.HasPrefix(val, "SIP_TRANSPORT_") {
			val = "SIP_TRANSPORT_" + val
		}
		trv, ok := livekit.SIPTransport_value[val]
		if !ok {
			return fmt.Errorf("unsupported transport: %q", val)
		}
		tr := livekit.SIPTransport(trv)
		req.Transport = &tr
	}
	if val := cmd.String("destination-country"); val != "" {
		req.DestinationCountry = &val
	}
	if val := cmd.String("auth-user"); val != "" {
		req.AuthUsername = &val
	}
	if val := cmd.String("auth-pass"); val != "" {
		req.AuthPassword = &val
	}
	req.Numbers = listUpdateFlag(cmd, "numbers")
	info, err := cli.UpdateSIPOutboundTrunk(ctx, &livekit.UpdateSIPOutboundTrunkRequest{
		SipTrunkId: id,
		Action: &livekit.UpdateSIPOutboundTrunkRequest_Update{
			Update: req,
		},
	})
	if err != nil {
		return err
	}
	printSIPOutboundTrunkID(info)
	return err
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

func printHeaders(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	var buf strings.Builder
	for i, key := range keys {
		if i != 0 {
			buf.WriteString("\n")
		}
		v := m[key]
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(v)
	}
	return buf.String()
}

func printHeaderMaps(arr ...map[string]string) string {
	var out []string
	for _, m := range arr {
		s := printHeaders(m)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n")
}

func listSipTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
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
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPInboundTrunk, &livekit.ListSIPInboundTrunkRequest{}, []string{
		"SipTrunkID", "Name", "Numbers",
		"AllowedAddresses", "AllowedNumbers",
		"Authentication",
		"Encryption",
		"Headers",
		"Metadata",
	}, func(item *livekit.SIPInboundTrunkInfo) []string {
		return []string{
			item.SipTrunkId, item.Name, strings.Join(item.Numbers, ","),
			strings.Join(item.AllowedAddresses, ","), strings.Join(item.AllowedNumbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			strings.TrimPrefix(item.MediaEncryption.String(), "SIP_MEDIA_ENCRYPT_"),
			printHeaderMaps(item.Headers, item.HeadersToAttributes),
			item.Metadata,
		}
	})
}

func listSipOutboundTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPOutboundTrunk, &livekit.ListSIPOutboundTrunkRequest{}, []string{
		"SipTrunkID", "Name",
		"Address", "Transport",
		"Numbers",
		"Authentication",
		"Encryption",
		"Headers",
		"Metadata",
	}, func(item *livekit.SIPOutboundTrunkInfo) []string {
		return []string{
			item.SipTrunkId, item.Name,
			item.Address, strings.TrimPrefix(item.Transport.String(), "SIP_TRANSPORT_"),
			strings.Join(item.Numbers, ","),
			userPass(item.AuthUsername, item.AuthPassword != ""),
			strings.TrimPrefix(item.MediaEncryption.String(), "SIP_MEDIA_ENCRYPT_"),
			printHeaderMaps(item.Headers, item.HeadersToAttributes),
			item.Metadata,
		}
	})
}

func deleteSIPTrunk(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
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
	cli, err := createSIPClient(ctx, cmd)
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
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(req *livekit.CreateSIPDispatchRuleRequest) error {
		if req.DispatchRule == nil {
			req.DispatchRule = new(livekit.SIPDispatchRuleInfo)
		}
		p := req.DispatchRule
		if val := cmd.String("name"); val != "" {
			p.Name = val
		}
		if val, ok := listSetFlag(cmd, "trunks"); ok {
			p.TrunkIds = val
		}
		if val := cmd.String("direct"); val != "" {
			if p.Rule != nil {
				return fmt.Errorf("only one dispatch rule type is allowed")
			}
			p.Rule = &livekit.SIPDispatchRule{
				Rule: &livekit.SIPDispatchRule_DispatchRuleDirect{
					DispatchRuleDirect: &livekit.SIPDispatchRuleDirect{
						RoomName: val,
					},
				},
			}
		}
		if val := cmd.String("caller"); val != "" {
			if p.Rule != nil {
				return fmt.Errorf("only one dispatch rule type is allowed")
			}
			p.Rule = &livekit.SIPDispatchRule{
				Rule: &livekit.SIPDispatchRule_DispatchRuleIndividual{
					DispatchRuleIndividual: &livekit.SIPDispatchRuleIndividual{
						RoomPrefix: val,
					},
				},
			}
		}
		if val := cmd.String("callee"); val != "" {
			if p.Rule != nil {
				return fmt.Errorf("only one dispatch rule type is allowed")
			}
			p.Rule = &livekit.SIPDispatchRule{
				Rule: &livekit.SIPDispatchRule_DispatchRuleCallee{
					DispatchRuleCallee: &livekit.SIPDispatchRuleCallee{
						RoomPrefix: val,
						Randomize:  cmd.Bool("randomize"),
					},
				},
			}
		}
		return nil
	}, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func createSIPDispatchRuleLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return createAndPrintLegacy(ctx, cmd, cli.CreateSIPDispatchRule, printSIPDispatchRuleID)
}

func updateSIPDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	id := cmd.String("id")
	if cmd.Args().Len() > 1 {
		return errors.New("expected one JSON file or flags")
	}
	if cmd.Args().Len() == 1 {
		// Update from the JSON
		req, err := ReadRequestFileOrLiteral[livekit.SIPDispatchRuleInfo](cmd.Args().First())
		if err != nil {
			return fmt.Errorf("could not read request: %w", err)
		}
		if id == "" {
			id = req.SipDispatchRuleId
		}
		req.SipDispatchRuleId = ""
		if id == "" {
			return errors.New("no ID specified, use flag or set it in JSON")
		}
		info, err := cli.UpdateSIPDispatchRule(ctx, &livekit.UpdateSIPDispatchRuleRequest{
			SipDispatchRuleId: id,
			Action: &livekit.UpdateSIPDispatchRuleRequest_Replace{
				Replace: req,
			},
		})
		if err != nil {
			return err
		}
		printSIPDispatchRuleID(info)
		return err
	}
	// Update from flags
	if id == "" {
		return errors.New("no ID specified")
	}
	req := &livekit.SIPDispatchRuleUpdate{}
	if val := cmd.String("name"); val != "" {
		req.Name = &val
	}
	req.TrunkIds = listUpdateFlag(cmd, "trunks")
	if val := cmd.String("direct"); val != "" {
		if req.Rule != nil {
			return fmt.Errorf("only one dispatch rule type is allowed")
		}
		req.Rule = &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleDirect{
				DispatchRuleDirect: &livekit.SIPDispatchRuleDirect{
					RoomName: val,
				},
			},
		}
	}
	if val := cmd.String("caller"); val != "" {
		if req.Rule != nil {
			return fmt.Errorf("only one dispatch rule type is allowed")
		}
		req.Rule = &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleIndividual{
				DispatchRuleIndividual: &livekit.SIPDispatchRuleIndividual{
					RoomPrefix: val,
				},
			},
		}
	}
	if val := cmd.String("callee"); val != "" {
		if req.Rule != nil {
			return fmt.Errorf("only one dispatch rule type is allowed")
		}
		req.Rule = &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleCallee{
				DispatchRuleCallee: &livekit.SIPDispatchRuleCallee{
					RoomPrefix: val,
					Randomize:  cmd.Bool("randomize"),
				},
			},
		}
	}
	info, err := cli.UpdateSIPDispatchRule(ctx, &livekit.UpdateSIPDispatchRuleRequest{
		SipDispatchRuleId: id,
		Action: &livekit.UpdateSIPDispatchRuleRequest_Update{
			Update: req,
		},
	})
	if err != nil {
		return err
	}
	printSIPDispatchRuleID(info)
	return err
}

func listSipDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return listAndPrint(ctx, cmd, cli.ListSIPDispatchRule, &livekit.ListSIPDispatchRuleRequest{}, []string{
		"SipDispatchRuleID", "Name", "SipTrunks", "Type", "RoomName", "Pin",
		"Attributes", "Agents",
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
		var agents []string
		if item.RoomConfig != nil {
			for _, agent := range item.RoomConfig.Agents {
				agents = append(agents, agent.AgentName)
			}
		}
		return []string{
			item.SipDispatchRuleId, item.Name, trunks, typ, room, pin,
			fmt.Sprintf("%v", item.Attributes), strings.Join(agents, ","),
		}
	})
}

func deleteSIPDispatchRule(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
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
	cli, err := createSIPClient(ctx, cmd)
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
	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}
	return createAndPrintReqs(ctx, cmd, func(req *livekit.CreateSIPParticipantRequest) error {
		if v := cmd.String("trunk"); v != "" {
			req.SipTrunkId = v
		}
		if v := cmd.String("number"); v != "" {
			req.SipNumber = v
		}
		if v := cmd.String("call"); v != "" {
			req.SipCallTo = v
		}
		if v := cmd.String("room"); v != "" {
			req.RoomName = v
		}
		if v := cmd.String("identity"); v != "" {
			req.ParticipantIdentity = v
		}
		if v := cmd.String("name"); v != "" {
			req.ParticipantName = v
		}
		if cmd.Bool("no-display-name") {
			emptyStr := ""
			req.DisplayName = &emptyStr
		} else if v := cmd.String("display-name"); v != "" {
			req.DisplayName = &v
		} else {
			req.DisplayName = nil
		}
		if cmd.Bool("wait") {
			req.WaitUntilAnswered = true
		}

		// Parse headers from repeatable "header" flag
		if headers := cmd.StringSlice("header"); len(headers) > 0 {
			if req.Headers == nil {
				req.Headers = make(map[string]string)
			}
			for _, header := range headers {
				parts := strings.SplitN(header, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid header format '%s', expected 'Key:Value'", header)
				}
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				if key == "" {
					return fmt.Errorf("header key cannot be empty in '%s'", header)
				}
				req.Headers[key] = value
			}
		}

		return req.Validate()
	}, func(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
		// CreateSIPParticipant will wait for LiveKit Participant to be created and that can take some time.
		// Default deadline is too short, thus, we must set a higher deadline for it.
		timeout := 30 * time.Second
		if req.WaitUntilAnswered {
			if dt := cmd.Duration("timeout"); dt != 0 {
				timeout = dt
			}
			req.RingingTimeout = durationpb.New(timeout - 500*time.Millisecond)
		} else {
			// For async API we should use a default timeout for the RPC,
			// and set a ringing timeout for the call instead.
			if dt := cmd.Duration("timeout"); dt != 0 {
				req.RingingTimeout = durationpb.New(dt)
			}
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		resp, err := cli.CreateSIPParticipant(ctx, req)
		if e := lksdk.SIPStatusFrom(err); e != nil {
			msg := e.Status
			if msg == "" {
				msg = e.Code.ShortName()
			}
			fmt.Printf("SIPStatusCode: %d\n", e.Code)
			fmt.Printf("SIPStatus: %s\n", msg)
		}
		return resp, err
	}, printSIPParticipantInfo)
}

func createSIPParticipantLegacy(ctx context.Context, cmd *cli.Command) error {
	cli, err := createSIPClient(ctx, cmd)
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
	roomName, identity := participantInfoFromArgOrFlags(cmd)
	to := cmd.String("to")
	dialtone := cmd.Bool("play-dialtone")

	req := livekit.TransferSIPParticipantRequest{
		RoomName:            roomName,
		ParticipantIdentity: identity,
		TransferTo:          to,
		PlayDialtone:        dialtone,
	}

	cli, err := createSIPClient(ctx, cmd)
	if err != nil {
		return err
	}

	_, err = cli.TransferSIPParticipant(ctx, &req)
	if err != nil {
		return err
	}

	return nil
}

func printSIPParticipantInfo(info *livekit.SIPParticipantInfo) {
	fmt.Printf("SIPCallID: %v\n", info.SipCallId)
	fmt.Printf("ParticipantID: %v\n", info.ParticipantId)
	fmt.Printf("ParticipantIdentity: %v\n", info.ParticipantIdentity)
	fmt.Printf("RoomName: %v\n", info.RoomName)
}
