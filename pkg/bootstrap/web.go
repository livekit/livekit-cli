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

package bootstrap

import (
	"errors"
)

type WebPackageManager string

const (
	NPM  WebPackageManager = "npm"
	PNPM WebPackageManager = "pnpm"
	Yarn WebPackageManager = "yarn"
)

func AutodetectWebPackageManagers() ([]WebPackageManager, error) {
	var pms []WebPackageManager
	if CommandExists(string(PNPM)) {
		pms = append(pms, PNPM)
	}
	if CommandExists(string(NPM)) {
		pms = append(pms, NPM)
	}
	if CommandExists(string(Yarn)) {
		pms = append(pms, Yarn)
	}
	if len(pms) == 0 {
		return pms, errors.New("must have one of pnpm, npm, or yarn installed")
	}
	return pms, nil
}
