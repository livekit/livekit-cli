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

package util

import (
	"strconv"
	"time"
)

// RenderList prints a list of items either as JSON (asJSON) or as a table whose
// rows are produced by row(). When the list is empty it prints the empty message
// as a status line. It is generic so callers render the source types directly
// without an intermediate view model.
func RenderList[T any](p *Printer, asJSON bool, list []T, empty string, headers []string, row func(T) []string) error {
	if asJSON {
		return PrintJSONTo(p.ResultWriter(), list)
	}
	if len(list) == 0 {
		p.Status(empty)
		return nil
	}
	t := CreateTable().Headers(headers...)
	for _, it := range list {
		t.Row(row(it)...)
	}
	p.Result(t)
	return nil
}

// RenderOne prints a single item as JSON (asJSON) or a one-row table.
func RenderOne[T any](p *Printer, asJSON bool, item T, headers []string, row func(T) []string) error {
	if asJSON {
		return PrintJSONTo(p.ResultWriter(), item)
	}
	t := CreateTable().Headers(headers...)
	t.Row(row(item)...)
	p.Result(t)
	return nil
}

// Deref returns the pointed-to value, or the zero value when nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DashString renders an optional string for a table cell, using a dash when it
// is nil or empty.
func DashString(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// DashInt32 renders an optional int32 for a table cell (0 when nil).
func DashInt32(v *int32) string {
	return strconv.Itoa(int(Deref(v)))
}

// DerefEnum renders an optional generated string-enum for a table cell, using a
// dash when nil or empty.
func DerefEnum[T ~string](p *T) string {
	if p == nil || *p == "" {
		return "-"
	}
	return string(*p)
}

// FormatTime renders an optional timestamp in local time, using a dash for a
// missing or zero value.
func FormatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}
