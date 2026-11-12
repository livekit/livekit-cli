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
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/livekit/livekit-cli/v2/pkg/public"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// The sip commands call the SIP API through server-sdk-go with an API key.
// Under --user-auth they call the Public API's TelephonyService instead,
// which checks the user's role on the project itself. Each command takes the
// interface below, so its flags, request files and output are the same in
// both modes: the SDK client implements it with an API key, and publicSIP
// under --user-auth.

// sipAPI is what the sip commands call.
type sipAPI interface {
	CreateSIPInboundTrunk(ctx context.Context, req *livekit.CreateSIPInboundTrunkRequest) (*livekit.SIPInboundTrunkInfo, error)
	UpdateSIPInboundTrunk(ctx context.Context, req *livekit.UpdateSIPInboundTrunkRequest) (*livekit.SIPInboundTrunkInfo, error)
	ListSIPInboundTrunk(ctx context.Context, req *livekit.ListSIPInboundTrunkRequest) (*livekit.ListSIPInboundTrunkResponse, error)
	CreateSIPOutboundTrunk(ctx context.Context, req *livekit.CreateSIPOutboundTrunkRequest) (*livekit.SIPOutboundTrunkInfo, error)
	UpdateSIPOutboundTrunk(ctx context.Context, req *livekit.UpdateSIPOutboundTrunkRequest) (*livekit.SIPOutboundTrunkInfo, error)
	ListSIPOutboundTrunk(ctx context.Context, req *livekit.ListSIPOutboundTrunkRequest) (*livekit.ListSIPOutboundTrunkResponse, error)
	DeleteSIPTrunk(ctx context.Context, req *livekit.DeleteSIPTrunkRequest) (*livekit.SIPTrunkInfo, error)
	CreateSIPDispatchRule(ctx context.Context, req *livekit.CreateSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error)
	UpdateSIPDispatchRule(ctx context.Context, req *livekit.UpdateSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error)
	ListSIPDispatchRule(ctx context.Context, req *livekit.ListSIPDispatchRuleRequest) (*livekit.ListSIPDispatchRuleResponse, error)
	DeleteSIPDispatchRule(ctx context.Context, req *livekit.DeleteSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error)
	CreateSIPParticipant(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error)
	TransferSIPParticipant(ctx context.Context, req *livekit.TransferSIPParticipantRequest) (*livekit.TransferSIPParticipantResponse, error)
}

var (
	_ sipAPI = (*lksdk.SIPClient)(nil)
	_ sipAPI = (*publicSIP)(nil)
)

// telephonyProject resolves the signed-in user's project (--project, the
// default project, or a picker) and a Public API client for it.
func telephonyProject(ctx context.Context, cmd *cli.Command) (*public.Client, string, error) {
	client, conf, user, err := newCloudAPIClient(cmd)
	if err != nil {
		return nil, "", err
	}
	projectID, err := resolveProjectRef(ctx, cmd, conf, user, "")
	if err != nil {
		return nil, "", err
	}
	return client, projectID, nil
}

// refuseUnderUserAuth is the Before of a hidden legacy SIP command, which the
// Public API has no RPC for: under --user-auth it points to the command that
// replaces it.
func refuseUnderUserAuth(use string) cli.BeforeFunc {
	return func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		if userAuthEnabled(cmd) {
			return nil, fmt.Errorf("%s is not supported with --user-auth; use %s", cmd.Name, use)
		}
		return nil, nil
	}
}

// publicSIP is sipAPI on the Public API, for one project.
type publicSIP struct {
	client    *public.Client
	projectID string
}

func (p *publicSIP) CreateSIPInboundTrunk(ctx context.Context, req *livekit.CreateSIPInboundTrunkRequest) (*livekit.SIPInboundTrunkInfo, error) {
	info, err := p.client.CreateSIPInboundTrunk(ctx, p.projectID, req.GetTrunk())
	return info, cloudAPIError(err)
}

func (p *publicSIP) UpdateSIPInboundTrunk(ctx context.Context, req *livekit.UpdateSIPInboundTrunkRequest) (*livekit.SIPInboundTrunkInfo, error) {
	info, err := p.client.UpdateSIPInboundTrunk(ctx, p.projectID, req)
	return info, cloudAPIError(err)
}

func (p *publicSIP) ListSIPInboundTrunk(ctx context.Context, req *livekit.ListSIPInboundTrunkRequest) (*livekit.ListSIPInboundTrunkResponse, error) {
	items, err := p.client.ListSIPInboundTrunks(ctx, p.projectID, req.GetNumbers())
	if err != nil {
		return nil, cloudAPIError(err)
	}
	return &livekit.ListSIPInboundTrunkResponse{Items: items}, nil
}

func (p *publicSIP) CreateSIPOutboundTrunk(ctx context.Context, req *livekit.CreateSIPOutboundTrunkRequest) (*livekit.SIPOutboundTrunkInfo, error) {
	info, err := p.client.CreateSIPOutboundTrunk(ctx, p.projectID, req.GetTrunk())
	return info, cloudAPIError(err)
}

func (p *publicSIP) UpdateSIPOutboundTrunk(ctx context.Context, req *livekit.UpdateSIPOutboundTrunkRequest) (*livekit.SIPOutboundTrunkInfo, error) {
	info, err := p.client.UpdateSIPOutboundTrunk(ctx, p.projectID, req)
	return info, cloudAPIError(err)
}

func (p *publicSIP) ListSIPOutboundTrunk(ctx context.Context, req *livekit.ListSIPOutboundTrunkRequest) (*livekit.ListSIPOutboundTrunkResponse, error) {
	items, err := p.client.ListSIPOutboundTrunks(ctx, p.projectID, req.GetNumbers())
	if err != nil {
		return nil, cloudAPIError(err)
	}
	return &livekit.ListSIPOutboundTrunkResponse{Items: items}, nil
}

// DeleteSIPTrunk deletes a trunk of either kind by its ID, as the SIP API
// does: `lk sip inbound delete` and `lk sip outbound delete` share it. The
// Public API names the kind, so an ID that isn't an inbound trunk is tried as
// an outbound one.
func (p *publicSIP) DeleteSIPTrunk(ctx context.Context, req *livekit.DeleteSIPTrunkRequest) (*livekit.SIPTrunkInfo, error) {
	id := req.GetSipTrunkId()
	err := p.client.DeleteSIPInboundTrunk(ctx, p.projectID, id)
	if public.IsNotFound(err) {
		err = p.client.DeleteSIPOutboundTrunk(ctx, p.projectID, id)
	}
	if err != nil {
		return nil, cloudAPIError(err)
	}
	return &livekit.SIPTrunkInfo{SipTrunkId: id}, nil
}

// CreateSIPDispatchRule takes the rule from dispatch_rule, or from the
// request's deprecated top-level fields, as the SIP API does, so an older
// request file still works.
func (p *publicSIP) CreateSIPDispatchRule(ctx context.Context, req *livekit.CreateSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error) {
	info, err := p.client.CreateSIPDispatchRule(ctx, p.projectID, req.DispatchRuleInfo())
	return info, cloudAPIError(err)
}

func (p *publicSIP) UpdateSIPDispatchRule(ctx context.Context, req *livekit.UpdateSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error) {
	info, err := p.client.UpdateSIPDispatchRule(ctx, p.projectID, req)
	return info, cloudAPIError(err)
}

func (p *publicSIP) ListSIPDispatchRule(ctx context.Context, req *livekit.ListSIPDispatchRuleRequest) (*livekit.ListSIPDispatchRuleResponse, error) {
	items, err := p.client.ListSIPDispatchRules(ctx, p.projectID, req.GetTrunkIds())
	if err != nil {
		return nil, cloudAPIError(err)
	}
	return &livekit.ListSIPDispatchRuleResponse{Items: items}, nil
}

func (p *publicSIP) DeleteSIPDispatchRule(ctx context.Context, req *livekit.DeleteSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error) {
	if err := p.client.DeleteSIPDispatchRule(ctx, p.projectID, req.GetSipDispatchRuleId()); err != nil {
		return nil, cloudAPIError(err)
	}
	return &livekit.SIPDispatchRuleInfo{SipDispatchRuleId: req.GetSipDispatchRuleId()}, nil
}

// CreateSIPParticipant's error unwraps to the far end's SIP status, when it
// sent one, so createSIPParticipant prints it in both modes.
func (p *publicSIP) CreateSIPParticipant(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
	info, err := p.client.CreateSIPParticipant(ctx, p.projectID, req)
	return info, cloudAPIError(err)
}

func (p *publicSIP) TransferSIPParticipant(ctx context.Context, req *livekit.TransferSIPParticipantRequest) (*livekit.TransferSIPParticipantResponse, error) {
	res, err := p.client.TransferSIPParticipant(ctx, p.projectID, req)
	return res, cloudAPIError(err)
}
