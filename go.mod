module github.com/livekit/livekit-cli

go 1.16

require (
	github.com/ggwhite/go-masker v1.0.4
	github.com/lithammer/shortuuid/v3 v3.0.7 // indirect
	github.com/livekit/protocol v0.6.4
	github.com/livekit/server-sdk-go v0.5.15
	github.com/pion/webrtc/v3 v3.0.30
	github.com/urfave/cli/v2 v2.3.0
	golang.org/x/crypto v0.0.0-20210513164829-c07d793c2f9a // indirect
	golang.org/x/sys v0.0.0-20210601080250-7ecdf8ef093b // indirect
	google.golang.org/protobuf v1.26.0
)

//replace github.com/livekit/server-sdk-go => ../server-sdk-go
