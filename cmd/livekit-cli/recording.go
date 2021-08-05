package main

import (
	"context"
	"fmt"
	"io/ioutil"

	"github.com/ggwhite/go-masker"
	lksdk "github.com/livekit/server-sdk-go"
	livekit "github.com/livekit/server-sdk-go/proto"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	RecordCommands = []*cli.Command{
		{
			Name:   "start-recording",
			Usage:  "starts a recording with a deployed recorder service",
			Before: createRecordingClient,
			Action: startRecording,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
				&cli.StringFlag{
					Name:     "config",
					Usage:    "recording config file path (see https://github.com/livekit/livekit-recorder/tree/main/recorder)",
					Required: true,
				},
			},
		},
		{
			Name:   "end-recording",
			Before: createRecordingClient,
			Action: endRecording,
			Flags: []cli.Flag{
				urlFlag,
				apiKeyFlag,
				secretFlag,
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
	configFile := c.String("config")
	config, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}
	req := &livekit.StartRecordingRequest{}
	err = protojson.Unmarshal(config, req)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		PrintJSON(req)
	}

	resp, err := recordingClient.StartRecording(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Printf("Recording started. Recording ID: %s\n", resp.RecordingId)
	return nil
}

func endRecording(c *cli.Context) error {
	_, err := recordingClient.EndRecording(context.Background(), &livekit.EndRecordingRequest{
		RecordingId: c.String("id"),
	})
	return err
}
