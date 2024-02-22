package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v2"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const obfuscatedCategory = "Obfuscated"

var (
	ObfuscatedCommands = []*cli.Command{
		{
			Name:     "a",
			Usage:    "A",
			Before:   createObfuscatedClient,
			Action:   a,
			Category: obfuscatedCategory,
			Hidden:   true,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "a",
					Usage:    "A",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "b",
					Usage:    "B",
					Required: true,
				},
				&cli.Int64Flag{
					Name:     "c",
					Usage:    "C",
					Required: false,
				},
			),
		},
		{
			Name:     "b",
			Usage:    "B",
			Before:   createObfuscatedClient,
			Action:   b,
			Category: obfuscatedCategory,
			Hidden:   true,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "a",
					Usage:    "A",
					Required: true,
				},
				&cli.Int64Flag{
					Name:     "b",
					Usage:    "B",
					Required: true,
				},
			),
		},
		{
			Name:     "c",
			Usage:    "C",
			Before:   createObfuscatedClient,
			Action:   c,
			Category: obfuscatedCategory,
			Hidden:   true,
			Flags: withDefaultFlags(
				&cli.StringFlag{
					Name:     "a",
					Usage:    "A",
					Required: true,
				},
			),
		},
	}

	obfuscatedClient *lksdk.ObfuscatedClient
)

func createObfuscatedClient(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	obfuscatedClient = lksdk.NewObfuscatedClient(pc.URL, pc.APIKey, pc.APISecret)
	return nil
}

func a(c *cli.Context) error {
	req := &livekit.ARequest{
		A: c.String("a"),
		B: c.String("b"),
		C: c.Int64("c"),
	}

	res, err := obfuscatedClient.A(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Println("Response: ", res)
	return nil
}

func b(c *cli.Context) error {
	req := &livekit.BRequest{
		A: c.String("a"),
		B: c.Int64("b"),
	}

	_, err := obfuscatedClient.B(context.Background(), req)
	return err
}

func c(c *cli.Context) error {
	req := &livekit.CRequest{
		A: c.String("a"),
	}

	_, err := obfuscatedClient.C(context.Background(), req)
	return err
}
