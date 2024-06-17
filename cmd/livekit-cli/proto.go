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
	"os"
	"reflect"

	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const flagRequest = "request"

func ReadRequest[T any, P interface {
	*T
	proto.Message
}](c *cli.Context) (*T, error) {
	reqBytes, err := os.ReadFile(c.String(flagRequest))
	if err != nil {
		return nil, err
	}

	var req P = new(T)
	err = protojson.Unmarshal(reqBytes, req)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func RequestFlag[T any, _ interface {
	*T
	proto.Message
}]() *cli.StringFlag {
	typ := reflect.TypeFor[T]().Name()
	return &cli.StringFlag{
		Name:     flagRequest,
		Usage:    typ + " as JSON file (see livekit-cli/examples)",
		Required: true,
	}
}
