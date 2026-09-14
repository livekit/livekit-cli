// Copyright 2026 LiveKit, Inc.
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

package public

import (
	"fmt"
	"strings"
)

// Role is a member/invite access level. It mirrors the server's
// cloud_protocol.ProjectMemberRole enum, which the Public API represents on the
// wire as a bare int32 (INVITED=0, READ=1, WRITE=2, ADMIN=3) with no name — so
// this int <-> name mapping has to live here rather than in the generated code.
type Role int32

const (
	RoleInvited Role = 0
	RoleRead    Role = 1
	RoleWrite   Role = 2
	RoleAdmin   Role = 3
)

// String returns the lowercase name of the role, or the numeric value for an
// unknown role.
func (r Role) String() string {
	switch r {
	case RoleInvited:
		return "invited"
	case RoleRead:
		return "read"
	case RoleWrite:
		return "write"
	case RoleAdmin:
		return "admin"
	default:
		return fmt.Sprintf("role(%d)", int32(r))
	}
}

// ParseRole resolves a case-insensitive role name (or its numeric value) to a
// Role. "member" is accepted as an alias for "write", "viewer" for "read".
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "invited", "0":
		return RoleInvited, nil
	case "read", "viewer", "1":
		return RoleRead, nil
	case "write", "member", "2":
		return RoleWrite, nil
	case "admin", "3":
		return RoleAdmin, nil
	default:
		return 0, fmt.Errorf("invalid role %q (expected one of: invited, read, write, admin)", s)
	}
}

// RoleName renders a wire role value (a *int32) for display. Empty for nil.
func RoleName(v *int32) string {
	if v == nil {
		return ""
	}
	return Role(*v).String()
}
