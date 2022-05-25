package main

import (
	"context"
	"fmt"
	"io/ioutil"

	"github.com/ggwhite/go-masker"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

const recordingCategory = "Recording"

var (
	RecordCommands = []*cli.Command{
		{
			Name:     "start-recording",
			Usage:    "Starts a recording with a deployed recorder service",
			Before:   createRecordingClient,
			Action:   startRecording,
			Category: recordingCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "request",
					Usage:    "StartRecordingRequest as json file (see https://github.com/livekit/livekit-recorder#request)",
					Required: true,
				},
			},
		},
		{
			Name:     "add-output",
			Usage:    "Adds an rtmp output url to a live recording",
			Before:   createRecordingClient,
			Action:   addOutput,
			Category: recordingCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "id of the recording",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "rtmp-url",
					Usage:    "rtmp url to add",
					Required: true,
				},
			},
		},
		{
			Name:     "remove-output",
			Usage:    "Removes an rtmp output url from a live recording",
			Before:   createRecordingClient,
			Action:   removeOutput,
			Category: recordingCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "id of the recording",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "rtmp-url",
					Usage:    "rtmp url to remove",
					Required: true,
				},
			},
		},
		{
			Name:     "end-recording",
			Usage:    "Ends a recording",
			Before:   createRecordingClient,
			Action:   endRecording,
			Category: recordingCategory,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				verboseFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "id of the recording",
					Required: true,
				},
			},
		},
	}

	recordingClient *lksdk.RecordingServiceClient
)

func createRecordingClient(c *cli.Context) error {
	fmt.Println("Warning: recording service is deprecated (use egress service instead)")

	url := c.String("url")
	apiKey := c.String("api-key")
	apiSecret := c.String("api-secret")

	if c.Bool("verbose") {
		fmt.Printf("creating client to %s, with api-key: %s, secret: %s\n",
			url,
			masker.ID(apiKey),
			masker.ID(apiSecret))
	}

	recordingClient = lksdk.NewRecordingServiceClient(url, apiKey, apiSecret)
	return nil
}

func startRecording(c *cli.Context) error {
	reqFile := c.String("request")
	reqBytes, err := ioutil.ReadFile(reqFile)
	if err != nil {
		return err
	}
	req := &livekit.StartRecordingRequest{}
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	res, err := recordingClient.StartRecording(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Printf("Recording started. Recording ID: %s\n", res.RecordingId)
	return nil
}

func addOutput(c *cli.Context) error {
	_, err := recordingClient.AddOutput(context.Background(), &livekit.AddOutputRequest{
		RecordingId: c.String("id"),
		RtmpUrl:     c.String("rtmp-url"),
	})
	return err
}

func removeOutput(c *cli.Context) error {
	_, err := recordingClient.RemoveOutput(context.Background(), &livekit.RemoveOutputRequest{
		RecordingId: c.String("id"),
		RtmpUrl:     c.String("rtmp-url"),
	})
	return err
}

func endRecording(c *cli.Context) error {
	_, err := recordingClient.EndRecording(context.Background(), &livekit.EndRecordingRequest{
		RecordingId: c.String("id"),
	})
	return err
}
