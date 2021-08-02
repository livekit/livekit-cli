package main

import (
	"context"
	"fmt"
	"io/ioutil"

	livekit "github.com/livekit/server-sdk-go/proto"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	RecordCommands = []*cli.Command{
		{
			Name:   "start-recording",
			Usage:  "starts a recording with a deployed recorder service",
			Before: createRoomClient,
			Action: startRecording,
			Flags: []cli.Flag{
				urlFlag,
				&cli.StringFlag{
					Name:     "config",
					Usage:    "recording config file path (see https://github.com/livekit/livekit-recorder/tree/main/recorder)",
					Required: true,
				},
			},
		},
		{
			Name:   "end-recording",
			Before: createRoomClient,
			Action: endRecording,
			Flags: []cli.Flag{
				urlFlag,
				&cli.StringFlag{
					Name:     "id",
					Usage:    "id of the recording",
					Required: true,
				},
			},
		},
	}
)

func startRecording(c *cli.Context) error {
	configFile := c.String("config")
	config, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}
	req := &livekit.RecordRoomRequest{}
	err = protojson.Unmarshal(config, req)
	if err != nil {
		return err
	}

	fmt.Printf("Recording config: %+v\n", req)

	resp, err := roomClient.StartRecording(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Printf("Recording started. Recording ID: %s\n", resp.RecordingId)
	return nil
}

func endRecording(c *cli.Context) error {
	_, err := roomClient.EndRecording(context.Background(), &livekit.EndRecordingRequest{
		RecordingId: c.String("id"),
	})
	return err
}
