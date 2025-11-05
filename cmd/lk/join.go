// Copyright 2021-2024 LiveKit, Inc.
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
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"

	provider2 "github.com/livekit/livekit-cli/v2/pkg/provider"
)

var (
	JoinCommands = []*cli.Command{
		{
			Hidden: true, // deprecated: use `lk room join`
			Name:   "join-room",
			Usage:  "Joins a room as a participant",
			Action: _deprecatedJoinRoom,
			Flags: []cli.Flag{
				roomFlag,
				identityFlag,
				&cli.BoolFlag{
					Name:  "publish-demo",
					Usage: "publish demo video as a loop",
				},
				&cli.StringSliceFlag{
					Name:      "publish",
					TakesFile: true,
					Usage: "`FILES` to publish as tracks to room (supports .h264, .ivf, .ogg). " +
						"can be used multiple times to publish multiple files. " +
						"can publish from Unix or TCP socket using the format '<codec>://<socket_name>' or '<codec>://<host:address>' respectively. Valid codecs are \"h264\", \"h265\", \"vp8\", \"opus\"",
				},
				&cli.FloatFlag{
					Name:  "fps",
					Usage: "if video files are published, indicates FPS of video",
				},
				&cli.BoolFlag{
					Name:  "exit-after-publish",
					Usage: "when publishing, exit after file or stream is complete",
				},
			},
		},
	}
)

const mimeDelimiter = "://"

func _deprecatedJoinRoom(ctx context.Context, cmd *cli.Command) error {
	pc, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	done := make(chan os.Signal, 1)
	roomCB := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataReceived: func(data []byte, params lksdk.DataReceiveParams) {
				identity := params.SenderIdentity
				logger.Infow("received data", "data", data, "participant", identity)
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track subscribed",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unsubscribed",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackUnpublished: func(pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unpublished",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackMuted: func(pub lksdk.TrackPublication, participant lksdk.Participant) {
				logger.Infow("track muted",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
			OnTrackUnmuted: func(pub lksdk.TrackPublication, participant lksdk.Participant) {
				logger.Infow("track unmuted",
					"kind", pub.Kind(),
					"trackID", pub.SID(),
					"source", pub.Source(),
					"participant", participant.Identity(),
				)
			},
		},
		OnRoomMetadataChanged: func(metadata string) {
			logger.Infow("room metadata changed", "metadata", metadata)
		},
		OnReconnecting: func() {
			logger.Infow("reconnecting to room")
		},
		OnReconnected: func() {
			logger.Infow("reconnected to room")
		},
		OnDisconnected: func() {
			logger.Infow("disconnected from room")
			close(done)
		},
	}
	room, err := lksdk.ConnectToRoom(pc.URL, lksdk.ConnectInfo{
		APIKey:              pc.APIKey,
		APISecret:           pc.APISecret,
		RoomName:            cmd.String("room"),
		ParticipantIdentity: cmd.String("identity"),
	}, roomCB)
	if err != nil {
		return err
	}
	defer room.Disconnect()

	logger.Infow("connected to room", "room", room.Name())

	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if cmd.Bool("publish-demo") {
		if err = publishDemo(room); err != nil {
			return err
		}
	}

	if cmd.StringSlice("publish") != nil {
		fps := cmd.Float("fps")
		h26xStreamingFormat := cmd.String("h26x-streaming-format")
		for _, pub := range cmd.StringSlice("publish") {
			onPublishComplete := func(pub *lksdk.LocalTrackPublication) {
				if cmd.Bool("exit-after-publish") {
					close(done)
					return
				}
				if pub != nil {
					fmt.Printf("finished writing %s\n", pub.Name())
					_ = room.LocalParticipant.UnpublishTrack(pub.SID())
				}
			}
			if err = handlePublish(room, pub, fps, h26xStreamingFormat, onPublishComplete); err != nil {
				return err
			}
		}
	}

	<-done
	return nil
}

func handlePublish(room *lksdk.Room,
	name string,
	fps float64,
	h26xStreamingFormat string,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	if isSocketFormat(name) {
		mimeType, socketType, address, err := parseSocketFromName(name)
		if err != nil {
			return err
		}
		return publishSocket(room, mimeType, socketType, address, fps, h26xStreamingFormat, onPublishComplete)
	}
	return publishFile(room, name, fps, h26xStreamingFormat, onPublishComplete)
}

func publishDemo(room *lksdk.Room) error {
	var tracks []*lksdk.LocalTrack

	loopers, err := provider2.CreateVideoLoopers("high", "", true)
	if err != nil {
		return err
	}
	for i, looper := range loopers {
		layer := looper.ToLayer(livekit.VideoQuality(i))
		track, err := lksdk.NewLocalTrack(looper.Codec(),
			lksdk.WithSimulcast("demo-video", layer),
		)
		if err != nil {
			return err
		}
		if err = track.StartWrite(looper, nil); err != nil {
			return err
		}
		tracks = append(tracks, track)
	}
	_, err = room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name: "demo",
	})
	return err
}

func publishFile(room *lksdk.Room,
	filename string,
	fps float64,
	h26xStreamingFormat string,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	// Configure provider
	opts := []lksdk.ReaderSampleProviderOption{
		lksdk.ReaderTrackWithRTCPHandler(func(packet rtcp.Packet) {
			switch packet.(type) {
			case *rtcp.PictureLossIndication:
				logger.Infow("received PLI", "filename", filename)
			}
		}),
	}
	var pub *lksdk.LocalTrackPublication
	if onPublishComplete != nil {
		opts = append(opts, lksdk.ReaderTrackWithOnWriteComplete(func() {
			onPublishComplete(pub)
		}))
	}

	// Set frame rate if it's a video stream and FPS is set
	ext := filepath.Ext(filename)
	if ext == ".h264" || ext == ".ivf" {
		if fps != 0 {
			frameDuration := time.Second / time.Duration(fps)
			opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
		}

		switch h26xStreamingFormat {
		case "annex-b":
			opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatAnnexB))
		case "length-prefixed":
			opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatLengthPrefixed))
		default:
			return fmt.Errorf("unsupported h26x streaming format: %s", h26xStreamingFormat)
		}
	}

	// Create track and publish
	track, err := lksdk.NewLocalFileTrack(filename, opts...)
	if err != nil {
		return err
	}
	pub, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: filename,
	})
	return err
}

func parseSocketFromName(name string) (string, string, string, error) {
	// Extract mime type, socket type, and address
	// e.g. h264://192.168.0.1:1234 (tcp)
	// e.g. opus:///tmp/my.socket (unix domain socket)

	offset := strings.Index(name, mimeDelimiter)
	if offset == -1 {
		return "", "", "", fmt.Errorf("did not find delimiter %s in %s", mimeDelimiter, name)
	}

	mimeType := name[:offset]

	if mimeType != "h264" && mimeType != "h265" && mimeType != "vp8" && mimeType != "opus" {
		return "", "", "", fmt.Errorf("unsupported mime type: %s", mimeType)
	}

	address := name[offset+len(mimeDelimiter):]

	if len(address) == 0 {
		return "", "", "", fmt.Errorf("address cannot be empty. input was: %s", name)
	}

	// If the address doesn't contain a ':' we assume it's a unix socket
	if !strings.Contains(address, ":") {
		return mimeType, "unix", address, nil
	}

	return mimeType, "tcp", address, nil
}

func isSocketFormat(name string) bool {
	return strings.Contains(name, mimeDelimiter)
}

func publishSocket(room *lksdk.Room,
	mimeType string,
	socketType string,
	address string,
	fps float64,
	h26xStreamingFormat string,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	var mime string
	switch {
	case strings.Contains(mimeType, "h264"):
		mime = webrtc.MimeTypeH264
	case strings.Contains(mimeType, "h265"):
		mime = webrtc.MimeTypeH265
	case strings.Contains(mimeType, "vp8"):
		mime = webrtc.MimeTypeVP8
	case strings.Contains(mimeType, "opus"):
		mime = webrtc.MimeTypeOpus
	default:
		return lksdk.ErrUnsupportedFileType
	}

	// Dial socket
	sock, err := net.Dial(socketType, address)
	if err != nil {
		return err
	}

	// Publish to room
	err = publishReader(room, sock, mime, fps, h26xStreamingFormat, onPublishComplete)
	return err
}

func publishReader(room *lksdk.Room,
	in io.ReadCloser,
	mime string,
	fps float64,
	h26xStreamingFormat string,
	onPublishComplete func(pub *lksdk.LocalTrackPublication),
) error {
	// Configure provider
	var opts []lksdk.ReaderSampleProviderOption
	var pub *lksdk.LocalTrackPublication
	if onPublishComplete != nil {
		opts = append(opts, lksdk.ReaderTrackWithOnWriteComplete(func() {
			onPublishComplete(pub)
		}))
	}

	// Set frame rate if it's a video stream and FPS is set
	if strings.HasPrefix(strings.ToLower(mime), "video") {
		if fps != 0 {
			frameDuration := time.Second / time.Duration(fps)
			opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
		}

		switch h26xStreamingFormat {
		case "annex-b":
			opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatAnnexB))
		case "length-prefixed":
			opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatLengthPrefixed))
		default:
			return fmt.Errorf("unsupported h26x streaming format: %s", h26xStreamingFormat)
		}
	}

	// Create track and publish
	track, err := lksdk.NewLocalReaderTrack(in, mime, opts...)
	if err != nil {
		return err
	}
	pub, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{})
	if err != nil {
		return err
	}
	return nil
}

// simulcastURLParts represents the parsed components of a simulcast URL
type simulcastURLParts struct {
	codec   string // "h264" or "h265"
	network string // "tcp" or "unix"
	address string
	width   uint32
	height  uint32
}

// parseSimulcastURL validates and parses a simulcast URL in the format <codec>://<host:port>/<width>x<height> or <codec>://<socket_path>/<width>x<height>
func parseSimulcastURL(url string) (*simulcastURLParts, error) {
	matches := simulcastURLRegex.FindStringSubmatch(url)
	if matches == nil {
		return nil, fmt.Errorf("simulcast URL must be in format <codec>://<host:port>/<width>x<height> or <codec>://<socket_path>/<width>x<height> where codec is h264 or h265, got: %s", url)
	}

	codec := matches[1]
	address, widthStr, heightStr := matches[2], matches[3], matches[4]

	// Parse dimensions
	width, err := strconv.ParseUint(widthStr, 10, 32)
	if err != nil || width == 0 {
		return nil, fmt.Errorf("invalid width in URL %s: must be > 0", url)
	}

	height, err := strconv.ParseUint(heightStr, 10, 32)
	if err != nil || height == 0 {
		return nil, fmt.Errorf("invalid height in URL %s: must be > 0", url)
	}

	network := "unix"
	if strings.Contains(address, ":") {
		network = "tcp"
	}

	return &simulcastURLParts{
		codec:   codec,
		network: network,
		address: address,
		width:   uint32(width),
		height:  uint32(height),
	}, nil
}

// createSimulcastVideoTrack creates a simulcast video track from a TCP or Unix socket H.264/H.265 streams
func createSimulcastVideoTrack(urlParts *simulcastURLParts, quality livekit.VideoQuality, fps float64, h26xStreamingFormat string, onComplete func()) (*lksdk.LocalTrack, error) {
	conn, err := net.Dial(urlParts.network, urlParts.address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s://%s: %w", urlParts.network, urlParts.address, err)
	}

	var opts []lksdk.ReaderSampleProviderOption

	// Add completion handler if provided
	if onComplete != nil {
		opts = append(opts, lksdk.ReaderTrackWithOnWriteComplete(onComplete))
	}

	// Set frame rate if FPS is set
	if fps != 0 {
		frameDuration := time.Second / time.Duration(fps)
		opts = append(opts, lksdk.ReaderTrackWithFrameDuration(frameDuration))
	}

	switch h26xStreamingFormat {
	case "annex-b":
		opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatAnnexB))
	case "length-prefixed":
		opts = append(opts, lksdk.ReaderTrackWithH26xStreamingFormat(lksdk.H26xStreamingFormatLengthPrefixed))
	default:
		return nil, fmt.Errorf("unsupported h26x streaming format: %s", h26xStreamingFormat)
	}

	// Configure simulcast layer
	opts = append(opts, lksdk.ReaderTrackWithSampleOptions(lksdk.WithSimulcast("simulcast", &livekit.VideoLayer{
		Quality: quality,
		Width:   urlParts.width,
		Height:  urlParts.height,
	})))

	mime := webrtc.MimeTypeH264
	if urlParts.codec == "h265" {
		mime = webrtc.MimeTypeH265
	}
	return lksdk.NewLocalReaderTrack(conn, mime, opts...)
}

// simulcastLayer represents a parsed H.264/H.265 stream with quality info
type simulcastLayer struct {
	url     string
	parts   *simulcastURLParts
	quality livekit.VideoQuality
	name    string
}

// handleSimulcastPublish handles publishing multiple H.264 streams as a simulcast track
func handleSimulcastPublish(room *lksdk.Room, urls []string, fps float64, h26xStreamingFormat string, onPublishComplete func(*lksdk.LocalTrackPublication)) error {
	// Parse all URLs
	var layers []simulcastLayer
	for _, url := range urls {
		parts, err := parseSimulcastURL(url)
		if err != nil {
			return fmt.Errorf("invalid simulcast URL %s: %w", url, err)
		}
		if parts != nil {
			layers = append(layers, simulcastLayer{
				url:   url,
				parts: parts,
			})
		}
	}

	if len(layers) == 0 {
		return fmt.Errorf("no valid simulcast URLs provided")
	}

	// Ensure all layers use the same codec
	codec := layers[0].parts.codec
	for _, l := range layers[1:] {
		if l.parts.codec != codec {
			return fmt.Errorf("all simulcast layers must use the same codec; expected %s, found %s", codec, l.parts.codec)
		}
	}

	// Sort streams by width to determine quality levels
	sort.Slice(layers, func(i, j int) bool {
		return layers[i].parts.width < layers[j].parts.width
	})

	// Assign quality levels based on stream count and order
	if len(layers) == 2 {
		// 2 streams: low and high quality
		layers[0].quality = livekit.VideoQuality_LOW
		layers[0].name = "low"
		layers[1].quality = livekit.VideoQuality_HIGH
		layers[1].name = "high"
	} else if len(layers) == 3 {
		// 3 streams: low, medium, high quality
		layers[0].quality = livekit.VideoQuality_LOW
		layers[0].name = "low"
		layers[1].quality = livekit.VideoQuality_MEDIUM
		layers[1].name = "medium"
		layers[2].quality = livekit.VideoQuality_HIGH
		layers[2].name = "high"
	} else {
		return fmt.Errorf("simulcast requires 2 or 3 streams, got %d", len(layers))
	}

	// Create tracks for each stream
	var tracks []*lksdk.LocalTrack
	var trackNames []string

	// Track completion - if any stream ends, signal completion
	var pub *lksdk.LocalTrackPublication
	completionSignaled := false
	var completionMutex sync.Mutex

	signalCompletion := func() {
		completionMutex.Lock()
		defer completionMutex.Unlock()
		if !completionSignaled && onPublishComplete != nil {
			completionSignaled = true
			onPublishComplete(pub)
		}
	}

	for _, layer := range layers {
		track, err := createSimulcastVideoTrack(layer.parts, layer.quality, fps, h26xStreamingFormat, signalCompletion)
		if err != nil {
			// Clean up any tracks we've already created
			for _, t := range tracks {
				t.Close()
			}
			return fmt.Errorf("failed to create %s quality track (%dx%d): %w",
				layer.name, layer.parts.width, layer.parts.height, err)
		}
		tracks = append(tracks, track)
		trackNames = append(trackNames, fmt.Sprintf("%s(%dx%d)", layer.name, layer.parts.width, layer.parts.height))
	}

	// Publish simulcast track
	var err error
	pub, err = room.LocalParticipant.PublishSimulcastTrack(tracks, &lksdk.TrackPublicationOptions{
		Name: "simulcast",
	})
	if err != nil {
		// Clean up tracks on publish failure
		for _, track := range tracks {
			track.Close()
		}
		return fmt.Errorf("failed to publish simulcast track: %w", err)
	}

	fmt.Printf("Successfully published %s simulcast track with qualities: %v\n", strings.ToUpper(codec), trackNames)
	return nil
}
