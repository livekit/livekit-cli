// Copyright 2024 LiveKit, Inc.
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
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const flagRequest = "request"

var unmarshaller = protojson.UnmarshalOptions{
	AllowPartial: true,
}

type protoType[T any] interface {
	*T
	proto.Message
}

type protoTypeValidator[T any] interface {
	protoType[T]
	Validate() error
}

func ReadRequest[T any, P protoType[T]](cmd *cli.Command) (*T, error) {
	return ReadRequestFileOrLiteral[T, P](cmd.String(flagRequest))
}

func ReadRequestArg[T any, P protoType[T]](cmd *cli.Command) (*T, error) {
	reqFile, err := extractArg(cmd)
	if err != nil {
		return nil, err
	}
	return ReadRequestFileOrLiteral[T, P](reqFile)
}

func ReadRequestArgOrFlag[T any, P protoType[T]](cmd *cli.Command) (*T, error) {
	reqFile, err := extractArg(cmd)
	if err != nil {
		return ReadRequest[T, P](cmd)
	}
	return ReadRequestFileOrLiteral[T, P](reqFile)
}

func ReadRequestFileOrLiteral[T any, P protoType[T]](pathOrLiteral string) (P, error) {
	var reqBytes []byte
	var err error

	// This allows us to read JSON from either CLI arg or FS
	if _, err = os.Stat(pathOrLiteral); err == nil {
		reqBytes, err = os.ReadFile(pathOrLiteral)
	} else {
		reqBytes = []byte(pathOrLiteral)
	}
	if err != nil {
		return nil, err
	}

	var req P = new(T)
	err = unmarshaller.Unmarshal(reqBytes, req)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func RequestFlag[T any, P protoType[T]]() *cli.StringFlag {
	return &cli.StringFlag{
		Name:     flagRequest,
		Usage:    RequestDesc[T, P](),
		Required: true,
	}
}

func RequestDesc[T any, _ protoType[T]]() string {
	typ := reflect.TypeFor[T]().Name()
	return typ + " as JSON file"
}

func createAndPrintFile[T any, P protoTypeValidator[T], R any](
	ctx context.Context,
	cmd *cli.Command, file string,
	fill func(p P) error,
	create func(ctx context.Context, p P) (R, error),
	print func(r R),
) error {
	req, err := ReadRequestFileOrLiteral[T, P](file)
	if err != nil {
		return fmt.Errorf("could not read request: %w", err)
	}
	return createAndPrintReq(ctx, cmd, req, fill, create, print)
}

func createAndPrintReq[T any, P protoTypeValidator[T], R any](
	ctx context.Context,
	cmd *cli.Command, req P,
	fill func(p P) error,
	create func(ctx context.Context, p P) (R, error),
	print func(r R),
) error {
	if fill != nil {
		if err := fill(req); err != nil {
			return err
		}
	}
	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	info, err := create(ctx, req)
	if err != nil {
		return err
	}
	print(info)
	return nil
}

func createAndPrintLegacy[T any, P protoType[T], R any](
	ctx context.Context,
	cmd *cli.Command,
	create func(ctx context.Context, p P) (R, error),
	print func(r R),
) error {
	req, err := ReadRequest[T, P](cmd)
	if err != nil {
		return err
	}
	if cmd.Bool("verbose") {
		util.PrintJSON(req)
	}
	info, err := create(ctx, req)
	if err != nil {
		return err
	}
	print(info)
	return nil
}

func createAndPrintReqs[T any, P protoTypeValidator[T], R any](
	ctx context.Context,
	cmd *cli.Command,
	fill func(p P) error,
	create func(ctx context.Context, p P) (R, error),
	print func(r R),
) error {
	args := cmd.Args()
	if !args.Present() {
		if fill == nil {
			return errors.New("at least one JSON request file is required")
		}
		var req P = new(T)
		return createAndPrintReq(ctx, cmd, req, fill, create, print)
	}
	for _, file := range args.Slice() {
		if err := createAndPrintFile(ctx, cmd, file, fill, create, print); err != nil {
			return err
		}
	}
	return nil
}

func forEachID(ctx context.Context, cmd *cli.Command, fnc func(ctx context.Context, id string) error) error {
	args := cmd.Args()
	if !args.Present() {
		return errors.New("at least one ID is required")
	}
	for _, id := range args.Slice() {
		if err := fnc(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func listAndPrint[
	ReqT any, Req protoType[ReqT],
	T any, _ protoType[T],
	Resp interface {
		proto.Message
		GetItems() []*T
	},
](
	ctx context.Context,
	cmd *cli.Command,
	getList func(ctx context.Context, req Req) (Resp, error), req Req,
	header []string, tableRow func(item *T) []string,
) error {
	res, err := getList(ctx, req)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		util.PrintJSON(res)
	} else {
		table := util.CreateTable().
			Headers(header...)
		for _, item := range res.GetItems() {
			if item == nil {
				continue
			}
			row := tableRow(item)
			if len(row) == 0 {
				continue
			}
			table.Row(row...)
		}
		fmt.Println(table)
	}

	return nil
}
