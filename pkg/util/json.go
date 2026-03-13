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

package util

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func PrintJSON(obj any) {
	_ = PrintJSONTo(os.Stdout, obj)
}

func PrintJSONTo(w io.Writer, obj any) error {
	const indent = "  "
	var txt []byte
	var err error
	if m, ok := obj.(proto.Message); ok {
		txt, err = protojson.MarshalOptions{Indent: indent}.Marshal(m)
	} else {
		txt, err = json.MarshalIndent(obj, "", indent)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(txt))
	return err
}
