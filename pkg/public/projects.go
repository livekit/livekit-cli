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
	"errors"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
)

// ProjectSettings are the project properties that CreateProject and
// UpdateProject both set. A nil field is left out of the request: a new
// project gets the server's default for it and an existing project keeps its
// current value.
type ProjectSettings struct {
	Name         *string
	Subdomain    *string
	CustomDomain *string
	IsPrivate    *bool

	DiscoverableByDomain *bool
	JoinableByDomain     *bool

	EnableAnalytics                  *bool
	EnableAutoCreate                 *bool
	EnableRemoteUnmute               *bool
	EnableEgressAutoRetry            *bool
	EnableEgressBackupStorage        *bool
	EnableInferenceRegionRestriction *bool
	EnableUserDataRecording          *bool
	EnableUserDataTraining           *bool
	RequireExplicitDispatch          *bool
	SfuAllowPause                    *bool
	DisableTokenEndpoint             *bool

	EnablePiiRedaction     *bool
	PiiRedactionCategories *[]oapi.CloudProtocolPIIRedactionCategory

	EnabledCodecs *[]string
	// PinnedRegions are the SFU region pins. On update an empty list clears
	// them; on create it leaves the project unpinned.
	PinnedRegions *[]string

	// UserDataRegion is where session data is stored. It can only be chosen
	// at creation, so UpdateProject refuses settings that set it.
	UserDataRegion *string
}

// IsZero reports whether s sets nothing.
func (s ProjectSettings) IsZero() bool {
	return s == ProjectSettings{}
}

func (s ProjectSettings) createRequest() oapi.LivekitPublicapiProjectsV1CreateProjectRequest {
	return oapi.LivekitPublicapiProjectsV1CreateProjectRequest{
		Name:                             s.Name,
		Subdomain:                        s.Subdomain,
		CustomDomain:                     s.CustomDomain,
		IsPrivate:                        s.IsPrivate,
		DiscoverableByDomain:             s.DiscoverableByDomain,
		JoinableByDomain:                 s.JoinableByDomain,
		EnableAnalytics:                  s.EnableAnalytics,
		EnableAutoCreate:                 s.EnableAutoCreate,
		EnableRemoteUnmute:               s.EnableRemoteUnmute,
		EnableEgressAutoRetry:            s.EnableEgressAutoRetry,
		EnableEgressBackupStorage:        s.EnableEgressBackupStorage,
		EnableInferenceRegionRestriction: s.EnableInferenceRegionRestriction,
		EnableUserDataRecording:          s.EnableUserDataRecording,
		EnableUserDataTraining:           s.EnableUserDataTraining,
		RequireExplicitDispatch:          s.RequireExplicitDispatch,
		SfuAllowPause:                    s.SfuAllowPause,
		DisableTokenEndpoint:             s.DisableTokenEndpoint,
		EnablePiiRedaction:               s.EnablePiiRedaction,
		PiiRedactionCategories:           s.PiiRedactionCategories,
		EnabledCodecs:                    s.EnabledCodecs,
		PinnedRegions:                    s.PinnedRegions,
		UserDataRegion:                   s.UserDataRegion,
	}
}

func (s ProjectSettings) updateRequest() (oapi.ProjectServiceUpdateProjectJSONRequestBody, error) {
	if s.UserDataRegion != nil {
		return oapi.ProjectServiceUpdateProjectJSONRequestBody{}, errors.New("a project's user data region is chosen at creation and cannot be changed")
	}
	var pinned *oapi.LivekitPublicapiProjectsV1PinnedRegions
	if s.PinnedRegions != nil {
		pinned = &oapi.LivekitPublicapiProjectsV1PinnedRegions{Regions: s.PinnedRegions}
	}
	return oapi.ProjectServiceUpdateProjectJSONRequestBody{
		Name:                             s.Name,
		Subdomain:                        s.Subdomain,
		CustomDomain:                     s.CustomDomain,
		IsPrivate:                        s.IsPrivate,
		DiscoverableByDomain:             s.DiscoverableByDomain,
		JoinableByDomain:                 s.JoinableByDomain,
		EnableAnalytics:                  s.EnableAnalytics,
		EnableAutoCreate:                 s.EnableAutoCreate,
		EnableRemoteUnmute:               s.EnableRemoteUnmute,
		EnableEgressAutoRetry:            s.EnableEgressAutoRetry,
		EnableEgressBackupStorage:        s.EnableEgressBackupStorage,
		EnableInferenceRegionRestriction: s.EnableInferenceRegionRestriction,
		EnableUserDataRecording:          s.EnableUserDataRecording,
		EnableUserDataTraining:           s.EnableUserDataTraining,
		RequireExplicitDispatch:          s.RequireExplicitDispatch,
		SfuAllowPause:                    s.SfuAllowPause,
		DisableTokenEndpoint:             s.DisableTokenEndpoint,
		EnablePiiRedaction:               s.EnablePiiRedaction,
		PiiRedactionCategories:           s.PiiRedactionCategories,
		EnabledCodecs:                    s.EnabledCodecs,
		PinnedRegions:                    pinned,
	}, nil
}
