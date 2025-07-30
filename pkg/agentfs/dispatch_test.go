package agentfs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateAgentDispatchToken(t *testing.T) {
	// Test basic token generation
	token, roomName, err := GenerateAgentDispatchToken(
		"test-agent",
		"test-room",
		"test-api-key",
		"test-api-secret",
		"test-participant",
		`{"user_id": "12345"}`,
	)

	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	if token == "" {
		t.Error("Expected non-empty token")
	}

	if roomName != "test-room" {
		t.Errorf("Expected room name 'test-room', got '%s'", roomName)
	}

	// Token should be a JWT (has 3 parts separated by dots)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("Expected JWT token with 3 parts, got %d", len(parts))
	}
}

func TestGenerateAgentDispatchTokenWithMetadata(t *testing.T) {
	metadata := map[string]any{
		"user_id":      "12345",
		"session_type": "support",
		"priority":     1,
	}

	token, roomName, err := GenerateAgentDispatchTokenWithMetadata(
		"support-agent",
		"", // Auto-generate room name
		"test-api-key",
		"test-api-secret",
		"", // Auto-generate identity
		metadata,
	)

	if err != nil {
		t.Fatalf("Failed to generate token with metadata: %v", err)
	}

	if token == "" {
		t.Error("Expected non-empty token")
	}

	if roomName == "" {
		t.Error("Expected auto-generated room name")
	}

	// Verify room name was auto-generated with correct prefix
	if !strings.HasPrefix(roomName, "room-") {
		t.Errorf("Expected room name to start with 'room-', got '%s'", roomName)
	}
}

func TestCreateJoinURL(t *testing.T) {
	testToken := "test-token-123"

	tests := []struct {
		name         string
		livekitURL   string
		useMeet      bool
		wantContains []string
		wantErr      bool
	}{
		{
			name:       "LiveKit Meet URL",
			livekitURL: "wss://example.livekit.cloud",
			useMeet:    true,
			wantContains: []string{
				"https://meet.livekit.io/custom",
				"liveKitUrl=wss",
				"token=test-token-123",
			},
		},
		{
			name:       "Custom Frontend WSS",
			livekitURL: "wss://example.livekit.cloud",
			useMeet:    false,
			wantContains: []string{
				"https://example.livekit.cloud/join",
				"token=test-token-123",
			},
		},
		{
			name:       "Custom Frontend WS",
			livekitURL: "ws://localhost:7880",
			useMeet:    false,
			wantContains: []string{
				"http://localhost:7880/join",
				"token=test-token-123",
			},
		},
		{
			name:       "Empty URL Error",
			livekitURL: "",
			useMeet:    true,
			wantErr:    true,
		},
		{
			name:       "Empty Token Error",
			livekitURL: "wss://example.livekit.cloud",
			useMeet:    true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenToUse := testToken
			if tt.name == "Empty Token Error" {
				tokenToUse = ""
			}

			joinURL, err := CreateJoinURL(tt.livekitURL, tokenToUse, tt.useMeet)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(joinURL, want) {
					t.Errorf("Expected URL to contain '%s', got: %s", want, joinURL)
				}
			}
		})
	}
}

func ExampleGenerateAgentDispatchToken() {
	// Example: Generate a token for a support agent
	token, roomName, err := GenerateAgentDispatchToken(
		"support-agent",          // Agent name
		"",                       // Auto-generate room name
		"your-api-key",           // LiveKit API key
		"your-api-secret",        // LiveKit API secret
		"customer-123",           // Participant identity
		`{"ticket_id": "T-456"}`, // Metadata for the agent
	)

	if err != nil {
		panic(err)
	}

	// Create join URL
	joinURL, _ := CreateJoinURL("wss://your-server.livekit.cloud", token, true)

	// Use the generated values
	_ = token
	_ = roomName
	_ = joinURL
}

func ExampleGenerateAgentDispatchTokenWithMetadata() {
	// Example: Generate a token with structured metadata
	metadata := map[string]any{
		"user_id":      "user-789",
		"session_type": "technical-support",
		"language":     "en",
		"priority":     2,
	}

	token, roomName, err := GenerateAgentDispatchTokenWithMetadata(
		"tech-support-agent",  // Agent name
		"support-session-123", // Specific room name
		"your-api-key",        // LiveKit API key
		"your-api-secret",     // LiveKit API secret
		"user-789",            // Participant identity
		metadata,              // Structured metadata
	)

	if err != nil {
		panic(err)
	}

	// The metadata will be JSON-encoded and passed to the agent
	_ = token
	_ = roomName
}

func TestQuickGenerateAgentToken(t *testing.T) {
	// This test demonstrates the convenience function
	// In real usage, you'd use actual API credentials
	t.Skip("Skipping as it requires real API credentials")

	token, roomName, joinURL, err := QuickGenerateAgentToken(
		"demo-agent",
		"wss://your-project.livekit.cloud",
		"your-api-key",
		"your-api-secret",
	)

	if err != nil {
		t.Fatalf("Failed to generate quick token: %v", err)
	}

	// Verify all outputs are populated
	if token == "" || roomName == "" || joinURL == "" {
		t.Error("Expected all outputs to be non-empty")
	}

	// The function should generate a complete workflow
	t.Logf("Token: %s", token)
	t.Logf("Room: %s", roomName)
	t.Logf("Join URL: %s", joinURL)
}

// TestMetadataMarshaling verifies that complex metadata structures are properly handled
func TestMetadataMarshaling(t *testing.T) {
	complexMetadata := map[string]any{
		"user": map[string]any{
			"id":   "12345",
			"name": "John Doe",
			"tags": []string{"premium", "support"},
		},
		"session": map[string]any{
			"type":     "chat",
			"priority": 1,
			"features": map[string]bool{
				"screen_share": true,
				"file_upload":  false,
			},
		},
	}

	token, _, err := GenerateAgentDispatchTokenWithMetadata(
		"complex-agent",
		"test-room",
		"test-key",
		"test-secret",
		"test-user",
		complexMetadata,
	)

	if err != nil {
		t.Fatalf("Failed to generate token with complex metadata: %v", err)
	}

	if token == "" {
		t.Error("Expected non-empty token")
	}

	// Verify the metadata can be marshaled to JSON
	jsonData, err := json.Marshal(complexMetadata)
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}

	// Verify it's valid JSON
	var decoded map[string]any
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}
}

func TestQuickGenerateAgentTokenFromEnv(t *testing.T) {
	// Test error when environment variables are not set
	token, roomName, joinURL, err := QuickGenerateAgentTokenFromEnv("test-agent")

	// Should fail because environment variables are not set
	if err == nil {
		t.Error("Expected error when environment variables are not set")
	}

	// All outputs should be empty when there's an error
	if token != "" || roomName != "" || joinURL != "" {
		t.Error("Expected empty outputs when there's an error")
	}
}

func ExampleQuickGenerateAgentToken() {
	// Example: Quickly generate everything needed for agent dispatch
	token, roomName, joinURL, err := QuickGenerateAgentToken(
		"support-agent",                  // Agent name
		"wss://my-project.livekit.cloud", // LiveKit URL
		"your-api-key",                   // API key
		"your-api-secret",                // API secret
	)

	if err != nil {
		panic(err)
	}

	// Use the generated values
	_ = token    // JWT token for authentication
	_ = roomName // Auto-generated room name
	_ = joinURL  // https://meet.livekit.io/custom?liveKitUrl=...&token=...
}
