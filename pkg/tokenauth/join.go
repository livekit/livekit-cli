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

package tokenauth

import (
	"context"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// Tokens the CLI signs itself (participant tokens for joining a room, links to
// LiveKit Meet, …) go through Sign, JoinRoom or ConnectToRoom. Each takes a
// *CachingTokenSource: nil signs locally with the API key and secret as before;
// non-nil fetches the token from the source instead, for clients without a
// secret.

// Sign returns a JWT carrying grants, valid for about ttl. With src nil it is
// signed with apiKey and apiSecret (ttl 0 means the AccessToken default);
// otherwise it is fetched from src (ttl 0 means the source's default).
func Sign(ctx context.Context, src *CachingTokenSource, apiKey, apiSecret string, grants *auth.ClaimGrants, ttl time.Duration) (string, error) {
	if src != nil {
		token, _, err := src.Fetch(ctx, grants, ttl)
		return token, err
	}
	at := auth.NewAccessToken(apiKey, apiSecret)
	if ttl > 0 {
		at.SetValidFor(ttl)
	}
	*at.GetGrants() = *grants.Clone()
	return at.ToJWT()
}

// ParticipantGrants returns the grants lksdk signs into the participant token
// when joining with info (see lksdk.Room.Join).
func ParticipantGrants(info lksdk.ConnectInfo) *auth.ClaimGrants {
	at := auth.NewAccessToken(info.APIKey, info.APISecret).
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: info.RoomName}).
		SetIdentity(info.ParticipantIdentity).
		SetMetadata(info.ParticipantMetadata).
		SetAttributes(info.ParticipantAttributes).
		SetName(info.ParticipantName).
		SetKind(livekit.ParticipantInfo_Kind(info.ParticipantKind))
	return at.GetGrants()
}

// JoinRoom is room.Join, fetching the participant token from src when src is
// non-nil (info's credentials are then ignored).
func JoinRoom(ctx context.Context, room *lksdk.Room, src *CachingTokenSource, url string, info lksdk.ConnectInfo, opts ...lksdk.ConnectOption) error {
	if src == nil {
		return room.Join(url, info, opts...)
	}
	token, _, err := src.Fetch(ctx, ParticipantGrants(info), 0)
	if err != nil {
		return err
	}
	return room.JoinWithContextAndToken(ctx, url, token, opts...)
}

// ConnectToRoom is lksdk.ConnectToRoom with JoinRoom's token handling.
func ConnectToRoom(ctx context.Context, src *CachingTokenSource, url string, info lksdk.ConnectInfo, callback *lksdk.RoomCallback, opts ...lksdk.ConnectOption) (*lksdk.Room, error) {
	room := lksdk.NewRoom(callback)
	if err := JoinRoom(ctx, room, src, url, info, opts...); err != nil {
		return nil, err
	}
	return room, nil
}
