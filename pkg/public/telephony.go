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

package public

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/protocol/livekit"
)

// TelephonyService's data shapes are LiveKit's own messages (livekit.
// SIPInboundTrunkInfo, livekit.PhoneNumber, ...), which the API imports and
// sends as protojson. So unlike the rest of this package, these methods take
// and return the livekit types: lk's sip and number commands already build
// and print them, and a protojson round trip keeps every field, enum, oneof
// and duration exactly, where the generated types would need a mapping for
// each. The generated client still makes each request, so its paths and
// query parameters come from the spec.

var (
	telephonyMarshal   = protojson.MarshalOptions{}
	telephonyUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// jsonBody returns the request body {"field": msg}, with msg as protojson.
func jsonBody(field string, msg proto.Message) (*bytes.Reader, error) {
	inner, err := telephonyMarshal.Marshal(msg)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]json.RawMessage{field: inner})
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

// decodeField decodes the field of a 200 response body into msg. A missing
// field is an error, because every telephony response that has a payload
// sends one.
func decodeField(status int, body []byte, field string, msg proto.Message) error {
	if status != http.StatusOK {
		return responseError(status, body)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("unexpected response from server: %w", err)
	}
	raw, ok := env[field]
	if !ok {
		return fmt.Errorf("unexpected response from server: missing %s", field)
	}
	return telephonyUnmarshal.Unmarshal(raw, msg)
}

// decodeList decodes the items of a 200 list response body, and returns the
// cursor of the next page, or "" on the last page.
func decodeList[T any, P interface {
	*T
	proto.Message
}](status int, body []byte) ([]P, string, error) {
	if status != http.StatusOK {
		return nil, "", responseError(status, body)
	}
	var env struct {
		Items    []json.RawMessage `json:"items"`
		PageInfo struct {
			NextCursor string `json:"nextCursor"`
			HasMore    bool   `json:"hasMore"`
		} `json:"pageInfo"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, "", fmt.Errorf("unexpected response from server: %w", err)
	}
	out := make([]P, 0, len(env.Items))
	for _, raw := range env.Items {
		var item P = new(T)
		if err := telephonyUnmarshal.Unmarshal(raw, item); err != nil {
			return nil, "", err
		}
		out = append(out, item)
	}
	if !env.PageInfo.HasMore {
		return out, "", nil
	}
	return out, env.PageInfo.NextCursor, nil
}

const jsonContentType = "application/json"

// ListSIPInboundTrunks returns the project's inbound trunks, in ID order.
// With numbers, it returns only the trunks that accept a call to one of them.
// The list is always one page.
func (c *Client) ListSIPInboundTrunks(ctx context.Context, projectID string, numbers []string) ([]*livekit.SIPInboundTrunkInfo, error) {
	params := &oapi.TelephonyServiceListInboundTrunksParams{}
	if len(numbers) > 0 {
		params.Numbers = &numbers
	}
	resp, err := c.gen.TelephonyServiceListInboundTrunksWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	items, _, err := decodeList[livekit.SIPInboundTrunkInfo](resp.StatusCode(), resp.Body)
	return items, err
}

// CreateSIPInboundTrunk creates trunk. The API makes its ID.
func (c *Client) CreateSIPInboundTrunk(ctx context.Context, projectID string, trunk *livekit.SIPInboundTrunkInfo) (*livekit.SIPInboundTrunkInfo, error) {
	body, err := jsonBody("trunk", trunk)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceCreateInboundTrunkWithBodyWithResponse(ctx, projectID, jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPInboundTrunkInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "trunk", out)
}

// UpdateSIPInboundTrunk replaces the trunk req names, or changes some of its
// fields, by req's action.
func (c *Client) UpdateSIPInboundTrunk(ctx context.Context, projectID string, req *livekit.UpdateSIPInboundTrunkRequest) (*livekit.SIPInboundTrunkInfo, error) {
	var body *bytes.Reader
	var err error
	switch a := req.GetAction().(type) {
	case *livekit.UpdateSIPInboundTrunkRequest_Replace:
		body, err = jsonBody("replace", a.Replace)
	case *livekit.UpdateSIPInboundTrunkRequest_Update:
		body, err = jsonBody("update", a.Update)
	default:
		return nil, errors.New("an update needs replace or update")
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceUpdateInboundTrunkWithBodyWithResponse(ctx, projectID, req.GetSipTrunkId(), jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPInboundTrunkInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "trunk", out)
}

// DeleteSIPInboundTrunk deletes the inbound trunk with id. An outbound
// trunk's ID is NotFound.
func (c *Client) DeleteSIPInboundTrunk(ctx context.Context, projectID, id string) error {
	resp, err := c.gen.TelephonyServiceDeleteInboundTrunkWithResponse(ctx, projectID, id)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListSIPOutboundTrunks returns the project's outbound trunks, in ID order.
// With numbers, it returns only the trunks that call from one of them. The
// list is always one page.
func (c *Client) ListSIPOutboundTrunks(ctx context.Context, projectID string, numbers []string) ([]*livekit.SIPOutboundTrunkInfo, error) {
	params := &oapi.TelephonyServiceListOutboundTrunksParams{}
	if len(numbers) > 0 {
		params.Numbers = &numbers
	}
	resp, err := c.gen.TelephonyServiceListOutboundTrunksWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	items, _, err := decodeList[livekit.SIPOutboundTrunkInfo](resp.StatusCode(), resp.Body)
	return items, err
}

// CreateSIPOutboundTrunk creates trunk. The API makes its ID.
func (c *Client) CreateSIPOutboundTrunk(ctx context.Context, projectID string, trunk *livekit.SIPOutboundTrunkInfo) (*livekit.SIPOutboundTrunkInfo, error) {
	body, err := jsonBody("trunk", trunk)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceCreateOutboundTrunkWithBodyWithResponse(ctx, projectID, jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPOutboundTrunkInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "trunk", out)
}

// UpdateSIPOutboundTrunk replaces the trunk req names, or changes some of its
// fields, by req's action.
func (c *Client) UpdateSIPOutboundTrunk(ctx context.Context, projectID string, req *livekit.UpdateSIPOutboundTrunkRequest) (*livekit.SIPOutboundTrunkInfo, error) {
	var body *bytes.Reader
	var err error
	switch a := req.GetAction().(type) {
	case *livekit.UpdateSIPOutboundTrunkRequest_Replace:
		body, err = jsonBody("replace", a.Replace)
	case *livekit.UpdateSIPOutboundTrunkRequest_Update:
		body, err = jsonBody("update", a.Update)
	default:
		return nil, errors.New("an update needs replace or update")
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceUpdateOutboundTrunkWithBodyWithResponse(ctx, projectID, req.GetSipTrunkId(), jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPOutboundTrunkInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "trunk", out)
}

// DeleteSIPOutboundTrunk deletes the outbound trunk with id. An inbound
// trunk's ID is NotFound.
func (c *Client) DeleteSIPOutboundTrunk(ctx context.Context, projectID, id string) error {
	resp, err := c.gen.TelephonyServiceDeleteOutboundTrunkWithResponse(ctx, projectID, id)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// ListSIPDispatchRules returns the project's dispatch rules, in ID order.
// With trunkIDs, it returns only the rules for one of those trunks or phone
// numbers. The list is always one page.
func (c *Client) ListSIPDispatchRules(ctx context.Context, projectID string, trunkIDs []string) ([]*livekit.SIPDispatchRuleInfo, error) {
	params := &oapi.TelephonyServiceListDispatchRulesParams{}
	if len(trunkIDs) > 0 {
		params.TrunkIds = &trunkIDs
	}
	resp, err := c.gen.TelephonyServiceListDispatchRulesWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	items, _, err := decodeList[livekit.SIPDispatchRuleInfo](resp.StatusCode(), resp.Body)
	return items, err
}

// CreateSIPDispatchRule creates rule. The API makes its ID.
func (c *Client) CreateSIPDispatchRule(ctx context.Context, projectID string, rule *livekit.SIPDispatchRuleInfo) (*livekit.SIPDispatchRuleInfo, error) {
	body, err := jsonBody("dispatchRule", rule)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceCreateDispatchRuleWithBodyWithResponse(ctx, projectID, jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPDispatchRuleInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "dispatchRule", out)
}

// UpdateSIPDispatchRule replaces the rule req names, or changes some of its
// fields, by req's action.
func (c *Client) UpdateSIPDispatchRule(ctx context.Context, projectID string, req *livekit.UpdateSIPDispatchRuleRequest) (*livekit.SIPDispatchRuleInfo, error) {
	var body *bytes.Reader
	var err error
	switch a := req.GetAction().(type) {
	case *livekit.UpdateSIPDispatchRuleRequest_Replace:
		body, err = jsonBody("replace", a.Replace)
	case *livekit.UpdateSIPDispatchRuleRequest_Update:
		body, err = jsonBody("update", a.Update)
	default:
		return nil, errors.New("an update needs replace or update")
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceUpdateDispatchRuleWithBodyWithResponse(ctx, projectID, req.GetSipDispatchRuleId(), jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPDispatchRuleInfo)
	return out, decodeField(resp.StatusCode(), resp.Body, "dispatchRule", out)
}

// DeleteSIPDispatchRule deletes the dispatch rule with id.
func (c *Client) DeleteSIPDispatchRule(ctx context.Context, projectID, id string) error {
	resp, err := c.gen.TelephonyServiceDeleteDispatchRuleWithResponse(ctx, projectID, id)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}

// CreateSIPParticipant places the call req describes. When the far end
// refuses the call, the error also wraps its *livekit.SIPStatus, so
// livekit.SIPStatusFrom finds it.
func (c *Client) CreateSIPParticipant(ctx context.Context, projectID string, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
	body, err := jsonBody("participant", req)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceCreateSIPParticipantWithBodyWithResponse(ctx, projectID, jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.SIPParticipantInfo)
	return out, withSIPStatus(decodeField(resp.StatusCode(), resp.Body, "participant", out))
}

// TransferSIPParticipant transfers the call req names, and returns when the
// transfer ends. A refusal wraps its SIP status, as CreateSIPParticipant's
// does.
func (c *Client) TransferSIPParticipant(ctx context.Context, projectID string, req *livekit.TransferSIPParticipantRequest) (*livekit.TransferSIPParticipantResponse, error) {
	body, err := jsonBody("transfer", req)
	if err != nil {
		return nil, err
	}
	resp, err := c.gen.TelephonyServiceTransferSIPParticipantWithBodyWithResponse(ctx, projectID, jsonContentType, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.TransferSIPParticipantResponse)
	return out, withSIPStatus(decodeField(resp.StatusCode(), resp.Body, "transfer", out))
}

// The names of the error details that carry the far end's SIP status: a
// refused call has a SIPStatus, and a failed transfer a SIPTransferError,
// which holds the status when the far end sent one.
const (
	sipStatusType        = "livekit.SIPStatus"
	sipTransferErrorType = "livekit.SIPTransferError"
)

// withSIPStatus wraps err with the SIP status in its details, when it has
// one.
func withSIPStatus(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	for _, raw := range apiErr.details {
		var typed struct {
			Type string `json:"@type"`
		}
		if json.Unmarshal(raw, &typed) != nil {
			continue
		}
		var status *livekit.SIPStatus
		switch typed.Type[strings.LastIndex(typed.Type, "/")+1:] {
		case sipStatusType:
			status = new(livekit.SIPStatus)
			if telephonyUnmarshal.Unmarshal(raw, status) != nil {
				return err
			}
		case sipTransferErrorType:
			transfer := new(livekit.SIPTransferError)
			if telephonyUnmarshal.Unmarshal(raw, transfer) != nil {
				return err
			}
			status = transfer.GetSipStatus()
		}
		if status != nil {
			return &sipCallError{api: apiErr, status: status}
		}
	}
	return err
}

// sipCallError is an APIError for a call that the far end refused. It reads
// as the APIError, and unwraps to both it and the SIP status.
type sipCallError struct {
	api    *APIError
	status *livekit.SIPStatus
}

func (e *sipCallError) Error() string   { return e.api.Error() }
func (e *sipCallError) Unwrap() []error { return []error{e.api, e.status} }

// SearchPhoneNumbers returns up to limit phone numbers for sale in the
// country, and in the area code when it is set. The API clamps limit to 100;
// 0 takes its default.
func (c *Client) SearchPhoneNumbers(ctx context.Context, projectID, countryCode, areaCode string, limit int32) ([]*livekit.PhoneNumber, error) {
	params := &oapi.TelephonyServiceSearchPhoneNumbersParams{CountryCode: &countryCode}
	if areaCode != "" {
		params.AreaCode = &areaCode
	}
	if limit > 0 {
		params.PagePageSize = &limit
	}
	resp, err := c.gen.TelephonyServiceSearchPhoneNumbersWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	items, _, err := decodeList[livekit.PhoneNumber](resp.StatusCode(), resp.Body)
	return items, err
}

// PurchasePhoneNumber buys one phone number, in E.164 format, and adds it to
// the dispatch rule ruleID when that is set.
func (c *Client) PurchasePhoneNumber(ctx context.Context, projectID, e164, ruleID string) (*livekit.PhoneNumber, error) {
	body := oapi.TelephonyServicePurchasePhoneNumberJSONRequestBody{PhoneNumber: &e164}
	if ruleID != "" {
		body.SipDispatchRuleId = &ruleID
	}
	resp, err := c.gen.TelephonyServicePurchasePhoneNumberWithResponse(ctx, projectID, body)
	if err != nil {
		return nil, err
	}
	out := new(livekit.PhoneNumber)
	return out, decodeField(resp.StatusCode(), resp.Body, "phoneNumber", out)
}

// PhoneNumberListOptions narrow and page ListPhoneNumbers. The zero value
// lists the first page of active numbers, at the API's default page size.
type PhoneNumberListOptions struct {
	Statuses          []livekit.PhoneNumberStatus
	SipDispatchRuleID string
	PageSize          int32
	Cursor            string
}

// Validate refuses a status the API doesn't take.
func (o PhoneNumberListOptions) Validate() error {
	for _, s := range o.Statuses {
		if s == livekit.PhoneNumberStatus_PHONE_NUMBER_STATUS_UNSPECIFIED {
			return errors.New("a phone number status filter must name a status")
		}
	}
	if o.PageSize < 0 {
		return errors.New("the page size can't be negative")
	}
	return nil
}

// ListPhoneNumbers returns a page of the project's phone numbers, newest
// first, and the cursor of the next page, or "" on the last page.
func (c *Client) ListPhoneNumbers(ctx context.Context, projectID string, opts PhoneNumberListOptions) ([]*livekit.PhoneNumber, string, error) {
	if err := opts.Validate(); err != nil {
		return nil, "", err
	}
	params := &oapi.TelephonyServiceListPhoneNumbersParams{}
	if len(opts.Statuses) > 0 {
		statuses := make([]oapi.LivekitPhoneNumberStatus, len(opts.Statuses))
		for i, s := range opts.Statuses {
			statuses[i] = oapi.LivekitPhoneNumberStatus(s.String())
		}
		params.Statuses = &statuses
	}
	if opts.SipDispatchRuleID != "" {
		params.SipDispatchRuleId = &opts.SipDispatchRuleID
	}
	if opts.PageSize > 0 {
		params.PagePageSize = &opts.PageSize
	}
	if opts.Cursor != "" {
		params.PageCursor = &opts.Cursor
	}
	resp, err := c.gen.TelephonyServiceListPhoneNumbersWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, "", err
	}
	return decodeList[livekit.PhoneNumber](resp.StatusCode(), resp.Body)
}

// GetPhoneNumber returns the project's phone number with id: its ID (PN_...)
// or the number in E.164 format.
func (c *Client) GetPhoneNumber(ctx context.Context, projectID, id string) (*livekit.PhoneNumber, error) {
	resp, err := c.gen.TelephonyServiceGetPhoneNumberWithResponse(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	out := new(livekit.PhoneNumber)
	return out, decodeField(resp.StatusCode(), resp.Body, "phoneNumber", out)
}

// UpdatePhoneNumber renames the phone number with id when name is set, and
// adds it to the dispatch rule ruleID when that is set.
func (c *Client) UpdatePhoneNumber(ctx context.Context, projectID, id string, name, ruleID *string) (*livekit.PhoneNumber, error) {
	resp, err := c.gen.TelephonyServiceUpdatePhoneNumberWithResponse(ctx, projectID, id, oapi.TelephonyServiceUpdatePhoneNumberJSONRequestBody{
		Name:              name,
		SipDispatchRuleId: ruleID,
	})
	if err != nil {
		return nil, err
	}
	out := new(livekit.PhoneNumber)
	return out, decodeField(resp.StatusCode(), resp.Body, "phoneNumber", out)
}

// ReleasePhoneNumber releases the phone number with id, its ID or the number
// in E.164 format.
func (c *Client) ReleasePhoneNumber(ctx context.Context, projectID, id string) error {
	resp, err := c.gen.TelephonyServiceReleasePhoneNumberWithResponse(ctx, projectID, id)
	if err != nil {
		return err
	}
	return okOrError(resp.StatusCode(), resp.Body)
}
