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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/livekit/protocol/livekit"
)

// recorded is one request a telephonyServer received.
type recorded struct {
	method, path, query, body string
}

// telephonyServer answers every request with status and body, and records
// the last request.
func telephonyServer(t *testing.T, status int, body string) (*Client, *recorded) {
	t.Helper()
	got := new(recorded)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = recorded{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	require.NoError(t, err)
	return c, got
}

// query parses the query of the request got.
func query(t *testing.T, got *recorded) url.Values {
	t.Helper()
	v, err := url.ParseQuery(got.query)
	require.NoError(t, err)
	return v
}

// A trunk goes out as the protojson lk reads from a request file, and comes
// back the same way: enums by name, durations as seconds, camelCase names.
func TestCreateSIPInboundTrunk(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK,
		`{"trunk":{"sipTrunkId":"ST_1","name":"in","authUsername":"u","authPassword":"********","ringingTimeout":"30s","media":{"encryption":"SIP_MEDIA_ENCRYPT_REQUIRE"},"aFieldFromANewerServer":1}}`)
	trunk, err := c.CreateSIPInboundTrunk(context.Background(), "p_1", &livekit.SIPInboundTrunkInfo{
		Name:           "in",
		Numbers:        []string{"+15550100"},
		AuthUsername:   "u",
		AuthPassword:   "secret",
		RingingTimeout: durationpb.New(30 * time.Second),
		Media:          &livekit.SIPMediaConfig{Encryption: livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_REQUIRE.Enum()},
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/sip/inbound-trunks", got.path)
	assert.JSONEq(t, `{"trunk":{"name":"in","numbers":["+15550100"],"authUsername":"u","authPassword":"secret","ringingTimeout":"30s","media":{"encryption":"SIP_MEDIA_ENCRYPT_REQUIRE"}}}`, got.body)
	assert.Equal(t, "ST_1", trunk.SipTrunkId)
	assert.Equal(t, "********", trunk.AuthPassword)
	assert.Equal(t, 30*time.Second, trunk.RingingTimeout.AsDuration())
	assert.Equal(t, livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_REQUIRE, trunk.Media.GetEncryption())
}

// An update sends its oneof as replace or update, and a ListUpdate as its
// own object.
func TestUpdateSIPTrunkActions(t *testing.T) {
	ctx := context.Background()
	c, got := telephonyServer(t, http.StatusOK, `{"trunk":{"sipTrunkId":"ST_1"}}`)

	_, err := c.UpdateSIPInboundTrunk(ctx, "p_1", &livekit.UpdateSIPInboundTrunkRequest{
		SipTrunkId: "ST_1",
		Action: &livekit.UpdateSIPInboundTrunkRequest_Update{Update: &livekit.SIPInboundTrunkUpdate{
			Name:    proto.String("renamed"),
			Numbers: &livekit.ListUpdate{Add: []string{"+15550101"}, Remove: []string{"+15550100"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, got.method)
	assert.Equal(t, "/v0/projects/p_1/sip/inbound-trunks/ST_1", got.path)
	assert.JSONEq(t, `{"update":{"name":"renamed","numbers":{"add":["+15550101"],"remove":["+15550100"]}}}`, got.body)

	_, err = c.UpdateSIPOutboundTrunk(ctx, "p_1", &livekit.UpdateSIPOutboundTrunkRequest{
		SipTrunkId: "ST_2",
		Action: &livekit.UpdateSIPOutboundTrunkRequest_Replace{Replace: &livekit.SIPOutboundTrunkInfo{
			Address: "sip.example.com", Transport: livekit.SIPTransport_SIP_TRANSPORT_TLS,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/sip/outbound-trunks/ST_2", got.path)
	assert.JSONEq(t, `{"replace":{"address":"sip.example.com","transport":"SIP_TRANSPORT_TLS"}}`, got.body)

	_, err = c.UpdateSIPDispatchRule(ctx, "p_1", &livekit.UpdateSIPDispatchRuleRequest{SipDispatchRuleId: "SDR_1"})
	require.EqualError(t, err, "an update needs replace or update")
}

// A dispatch rule read from a request file, which may use the proto's own
// field names, goes out under the JSON names the API takes.
func TestCreateSIPDispatchRuleFromRequestFile(t *testing.T) {
	var req livekit.CreateSIPDispatchRuleRequest
	require.NoError(t, protojson.Unmarshal([]byte(`{"dispatch_rule":{"name":"rule","trunk_ids":["PN_1"],"rule":{"dispatch_rule_individual":{"room_prefix":"call-"}},"room_config":{"agents":[{"agent_name":"bot"}]}}}`), &req))
	c, got := telephonyServer(t, http.StatusOK, `{"dispatchRule":{"sipDispatchRuleId":"SDR_1","name":"rule"}}`)
	rule, err := c.CreateSIPDispatchRule(context.Background(), "p_1", req.DispatchRule)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/sip/dispatch-rules", got.path)
	assert.JSONEq(t, `{"dispatchRule":{"name":"rule","trunkIds":["PN_1"],"rule":{"dispatchRuleIndividual":{"roomPrefix":"call-"}},"roomConfig":{"agents":[{"agentName":"bot"}]}}}`, got.body)
	assert.Equal(t, "SDR_1", rule.SipDispatchRuleId)
}

func TestListSIPLists(t *testing.T) {
	ctx := context.Background()
	c, got := telephonyServer(t, http.StatusOK, `{"items":[{"sipTrunkId":"ST_1"},{"sipTrunkId":"ST_2"}],"pageInfo":{}}`)
	trunks, err := c.ListSIPInboundTrunks(ctx, "p_1", []string{"+15550100"})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/sip/inbound-trunks", got.path)
	assert.Equal(t, "numbers=%2B15550100", got.query)
	require.Len(t, trunks, 2)
	assert.Equal(t, "ST_2", trunks[1].SipTrunkId)

	c, got = telephonyServer(t, http.StatusOK, `{"items":[{"sipDispatchRuleId":"SDR_1"}]}`)
	rules, err := c.ListSIPDispatchRules(ctx, "p_1", []string{"ST_1", "PN_1"})
	require.NoError(t, err)
	assert.Equal(t, "trunkIds=ST_1&trunkIds=PN_1", got.query)
	require.Len(t, rules, 1)
}

// A call the far end refused is still the APIError it was, and carries the
// SIP status that lk prints.
func TestCreateSIPParticipantRefused(t *testing.T) {
	c, _ := telephonyServer(t, http.StatusBadRequest,
		`{"code":9,"message":"the call failed with SIP status 486","details":[{"@type":"type.googleapis.com/livekit.SIPStatus","code":"SIP_STATUS_BUSY_HERE","status":"Busy Here"}]}`)
	_, err := c.CreateSIPParticipant(context.Background(), "p_1", &livekit.CreateSIPParticipantRequest{SipTrunkId: "ST_1", SipCallTo: "+15550100", RoomName: "r"})
	require.EqualError(t, err, "the call failed with SIP status 486")
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Status)
	status := livekit.SIPStatusFrom(err)
	require.NotNil(t, status)
	assert.Equal(t, livekit.SIPStatusCode_SIP_STATUS_BUSY_HERE, status.Code)
	assert.Equal(t, "Busy Here", status.Status)

	// A failed transfer carries the status inside a SIPTransferError.
	c, _ = telephonyServer(t, http.StatusBadRequest,
		`{"code":9,"message":"the call failed with SIP status 603","details":[{"@type":"type.googleapis.com/livekit.SIPTransferError","transferId":"ST_x","reason":"STR_REJECTED","sipStatus":{"code":"SIP_STATUS_GLOBAL_DECLINE","status":"Decline"}}]}`)
	_, err = c.TransferSIPParticipant(context.Background(), "p_1", &livekit.TransferSIPParticipantRequest{RoomName: "r"})
	require.EqualError(t, err, "the call failed with SIP status 603")
	status = livekit.SIPStatusFrom(err)
	require.NotNil(t, status)
	assert.Equal(t, livekit.SIPStatusCode_SIP_STATUS_GLOBAL_DECLINE, status.Code)

	// One with no SIP status reads as its message, with no status.
	c, _ = telephonyServer(t, http.StatusBadRequest,
		`{"code":9,"message":"the transfer failed: ringing timeout","details":[{"@type":"type.googleapis.com/livekit.SIPTransferError","transferId":"ST_x","reason":"STR_RINGING_TIMEOUT"}]}`)
	_, err = c.TransferSIPParticipant(context.Background(), "p_1", &livekit.TransferSIPParticipantRequest{RoomName: "r"})
	require.EqualError(t, err, "the transfer failed: ringing timeout")
	assert.Nil(t, livekit.SIPStatusFrom(err))

	c, _ = telephonyServer(t, http.StatusForbidden, `{"code":7,"message":"this needs WRITE on the project"}`)
	_, err = c.TransferSIPParticipant(context.Background(), "p_1", &livekit.TransferSIPParticipantRequest{RoomName: "r"})
	assert.True(t, IsPermissionDenied(err))
	assert.Nil(t, livekit.SIPStatusFrom(err))
}

func TestCreateSIPParticipantBody(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"participant":{"participantId":"PA_1","sipCallId":"SCL_1"}}`)
	info, err := c.CreateSIPParticipant(context.Background(), "p_1", &livekit.CreateSIPParticipantRequest{
		SipTrunkId: "ST_1", SipCallTo: "+15550100", RoomName: "r", WaitUntilAnswered: true,
		RingingTimeout: durationpb.New(20 * time.Second), Headers: map[string]string{"X-A": "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/sip/participants", got.path)
	assert.JSONEq(t, `{"participant":{"sipTrunkId":"ST_1","sipCallTo":"+15550100","roomName":"r","waitUntilAnswered":true,"ringingTimeout":"20s","headers":{"X-A":"b"}}}`, got.body)
	assert.Equal(t, "SCL_1", info.SipCallId)

	c, got = telephonyServer(t, http.StatusOK, `{"transfer":{"transferId":"TR_1"}}`)
	res, err := c.TransferSIPParticipant(context.Background(), "p_1", &livekit.TransferSIPParticipantRequest{RoomName: "r", ParticipantIdentity: "sip_1", TransferTo: "tel:+15550101"})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/sip/participants:transfer", got.path)
	assert.JSONEq(t, `{"transfer":{"roomName":"r","participantIdentity":"sip_1","transferTo":"tel:+15550101"}}`, got.body)
	assert.Equal(t, "TR_1", res.TransferId)
}

func TestListPhoneNumbersPages(t *testing.T) {
	c, got := telephonyServer(t, http.StatusOK, `{"items":[{"id":"PN_1","e164Format":"+15550100","status":"PHONE_NUMBER_STATUS_ACTIVE"}],"pageInfo":{"nextCursor":"next","hasMore":true}}`)
	numbers, next, err := c.ListPhoneNumbers(context.Background(), "p_1", PhoneNumberListOptions{
		Statuses:          []livekit.PhoneNumberStatus{livekit.PhoneNumberStatus_PHONE_NUMBER_STATUS_ACTIVE, livekit.PhoneNumberStatus_PHONE_NUMBER_STATUS_PENDING},
		SipDispatchRuleID: "SDR_1",
		PageSize:          1,
		Cursor:            "c1",
	})
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/phone-numbers", got.path)
	assert.Equal(t, url.Values{
		"page.cursor": {"c1"}, "page.pageSize": {"1"}, "sipDispatchRuleId": {"SDR_1"},
		"statuses": {"PHONE_NUMBER_STATUS_ACTIVE", "PHONE_NUMBER_STATUS_PENDING"},
	}, query(t, got))
	assert.Equal(t, "next", next)
	require.Len(t, numbers, 1)
	assert.Equal(t, livekit.PhoneNumberStatus_PHONE_NUMBER_STATUS_ACTIVE, numbers[0].Status)

	c, _ = telephonyServer(t, http.StatusOK, `{"items":[],"pageInfo":{"hasMore":false}}`)
	_, next, err = c.ListPhoneNumbers(context.Background(), "p_1", PhoneNumberListOptions{})
	require.NoError(t, err)
	assert.Empty(t, next, "the last page has no cursor")
}

func TestPhoneNumberListOptionsValidate(t *testing.T) {
	require.NoError(t, PhoneNumberListOptions{}.Validate())
	require.Error(t, PhoneNumberListOptions{Statuses: []livekit.PhoneNumberStatus{livekit.PhoneNumberStatus_PHONE_NUMBER_STATUS_UNSPECIFIED}}.Validate())
	require.Error(t, PhoneNumberListOptions{PageSize: -1}.Validate())
}

func TestPhoneNumberCalls(t *testing.T) {
	ctx := context.Background()
	c, got := telephonyServer(t, http.StatusOK, `{"phoneNumber":{"id":"PN_1","e164Format":"+15550100"}}`)

	number, err := c.PurchasePhoneNumber(ctx, "p_1", "+15550100", "SDR_1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v0/projects/p_1/phone-numbers", got.path)
	assert.JSONEq(t, `{"phoneNumber":"+15550100","sipDispatchRuleId":"SDR_1"}`, got.body)
	assert.Equal(t, "PN_1", number.Id)

	// An E.164 number works as the ID.
	_, err = c.GetPhoneNumber(ctx, "p_1", "+15550100")
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/phone-numbers/+15550100", got.path)

	_, err = c.UpdatePhoneNumber(ctx, "p_1", "PN_1", proto.String("front desk"), nil)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, got.method)
	assert.JSONEq(t, `{"name":"front desk"}`, got.body)

	c, got = telephonyServer(t, http.StatusOK, `{"items":[{"e164Format":"+14155550100"}]}`)
	found, err := c.SearchPhoneNumbers(ctx, "p_1", "US", "415", 10)
	require.NoError(t, err)
	assert.Equal(t, "/v0/projects/p_1/phone-numbers:search", got.path)
	assert.Equal(t, url.Values{"countryCode": {"US"}, "areaCode": {"415"}, "page.pageSize": {"10"}}, query(t, got))
	require.Len(t, found, 1)

	c, got = telephonyServer(t, http.StatusOK, `{}`)
	require.NoError(t, c.ReleasePhoneNumber(ctx, "p_1", "PN_1"))
	assert.Equal(t, http.MethodDelete, got.method)
	assert.Equal(t, "/v0/projects/p_1/phone-numbers/PN_1", got.path)

	c, _ = telephonyServer(t, http.StatusNotFound, `{"code":5,"message":"phone number not found"}`)
	_, err = c.GetPhoneNumber(ctx, "p_1", "PN_9")
	assert.True(t, IsNotFound(err))
	assert.EqualError(t, err, "phone number not found")
}
