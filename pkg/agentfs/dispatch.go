package agentfs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

// GenerateAgentDispatchToken creates a token with agent dispatch configuration
// agentName: name of the agent to dispatch
// roomName: optional room name (generates one if empty)
// apiKey: LiveKit API key
// apiSecret: LiveKit API secret
// identity: participant identity (generates one if empty)
// metadata: optional metadata JSON string to attach to the agent dispatch
func GenerateAgentDispatchToken(agentName, roomName, apiKey, apiSecret, identity, metadata string) (token string, finalRoomName string, err error) {
	// Generate room name if not provided
	if roomName == "" {
		roomName = fmt.Sprintf("room-%d", time.Now().Unix())
	}

	// Generate identity if not provided
	if identity == "" {
		identity = fmt.Sprintf("participant-%d", time.Now().UnixNano())
	}

	// Create video grant with room join permission
	grant := &auth.VideoGrant{
		Room:     roomName,
		RoomJoin: true,
	}

	// Create access token
	at := auth.NewAccessToken(apiKey, apiSecret).
		SetVideoGrant(grant).
		SetIdentity(identity).
		SetName(fmt.Sprintf("dev-%s", agentName)).  // Set participant name to dev-{agentName}
		SetValidFor(5 * time.Minute) // Default 5 minutes validity

	// Create room configuration with agent dispatch
	agentDispatch := &livekit.RoomAgentDispatch{
		AgentName: agentName,
		Metadata:  metadata,
	}

	roomConfig := &livekit.RoomConfiguration{
		EmptyTimeout:     5 * 60, // 5 minutes in seconds
		DepartureTimeout: 5 * 60, // 5 minutes in seconds
		Agents:           []*livekit.RoomAgentDispatch{agentDispatch},
	}

	// Set room configuration on token
	at.SetRoomConfig(roomConfig)

	// Generate JWT token
	token, err = at.ToJWT()
	if err != nil {
		return
	}

	finalRoomName = roomName
	return
}

// GenerateAgentDispatchTokenWithMetadata creates a token with agent dispatch and structured metadata
func GenerateAgentDispatchTokenWithMetadata(agentName, roomName, apiKey, apiSecret, identity string, metadata map[string]any) (token string, finalRoomName string, err error) {
	metadataJSON := ""
	if metadata != nil {
		var data []byte
		data, err = json.Marshal(metadata)
		if err != nil {
			err = fmt.Errorf("failed to marshal metadata: %w", err)
			return
		}
		metadataJSON = string(data)
	}

	return GenerateAgentDispatchToken(agentName, roomName, apiKey, apiSecret, identity, metadataJSON)
}

// CreateJoinURL creates a join URL for LiveKit Meet or a custom frontend
// livekitURL: the LiveKit server URL (e.g., "wss://example.livekit.cloud")
// token: the access token
// useMeet: if true, generates URL for meet.livekit.io, otherwise for custom frontend
func CreateJoinURL(livekitURL, token string, useMeet bool) (joinURL string, err error) {
	if livekitURL == "" {
		err = fmt.Errorf("livekit URL is required")
		return
	}
	if token == "" {
		err = fmt.Errorf("token is required")
		return
	}

	if useMeet {
		// For LiveKit Meet
		params := url.Values{}
		params.Set("liveKitUrl", livekitURL)
		params.Set("token", token)

		joinURL = fmt.Sprintf("https://meet.livekit.io/custom?%s", params.Encode())
		return
	}

	// For custom frontend, you might want to adjust this based on your needs
	// This is a generic example
	var baseURL *url.URL
	baseURL, err = url.Parse(livekitURL)
	if err != nil {
		err = fmt.Errorf("invalid livekit URL: %w", err)
		return
	}

	// Convert wss:// to https:// for web frontend
	if baseURL.Scheme == "wss" {
		baseURL.Scheme = "https"
	} else if baseURL.Scheme == "ws" {
		baseURL.Scheme = "http"
	}

	params := url.Values{}
	params.Set("token", token)

	joinURL = fmt.Sprintf("%s/join?%s", baseURL.String(), params.Encode())
	return
}

// QuickGenerateAgentToken is a convenience function that generates a token with minimal parameters
// and creates a join URL for LiveKit Meet
func QuickGenerateAgentToken(agentName, livekitURL, apiKey, apiSecret string) (token string, roomName string, joinURL string, err error) {
	// Generate token with agent dispatch
	token, roomName, err = GenerateAgentDispatchToken(agentName, "", apiKey, apiSecret, "", "")
	if err != nil {
		return
	}

	// Create join URL for LiveKit Meet
	joinURL, err = CreateJoinURL(livekitURL, token, true)
	if err != nil {
		// Keep partial results
		return
	}

	return
}

// QuickGenerateAgentTokenFromEnv is a convenience function that reads credentials from environment variables
// It expects LIVEKIT_URL, LIVEKIT_API_KEY, and LIVEKIT_API_SECRET to be set
func QuickGenerateAgentTokenFromEnv(agentName string) (token string, roomName string, joinURL string, err error) {
	livekitURL := os.Getenv("LIVEKIT_URL")
	apiKey := os.Getenv("LIVEKIT_API_KEY")
	apiSecret := os.Getenv("LIVEKIT_API_SECRET")

	if livekitURL == "" {
		err = fmt.Errorf("LIVEKIT_URL environment variable not set")
		return
	}
	if apiKey == "" {
		err = fmt.Errorf("LIVEKIT_API_KEY environment variable not set")
		return
	}
	if apiSecret == "" {
		err = fmt.Errorf("LIVEKIT_API_SECRET environment variable not set")
		return
	}

	return QuickGenerateAgentToken(agentName, livekitURL, apiKey, apiSecret)
}
