module github.com/livekit/livekit-cli

go 1.16

require (
	github.com/ggwhite/go-masker v1.0.4
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/livekit/livekit-sdk-go v0.5.2
	github.com/livekit/protocol v0.5.0
	github.com/pion/webrtc/v3 v3.0.29
	github.com/twitchtv/twirp v8.0.0+incompatible // indirect
	github.com/urfave/cli/v2 v2.3.0
	golang.org/x/crypto v0.0.0-20210513164829-c07d793c2f9a // indirect
	golang.org/x/net v0.0.0-20210510120150-4163338589ed // indirect
	golang.org/x/sys v0.0.0-20210511113859-b0526f3d8744 // indirect
)

//replace github.com/livekit/livekit-sdk-go => ../livekit-sdk-go
