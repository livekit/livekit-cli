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
	// Normalize to a non-nil slice so the JSON output is always [], never null,
	// for consumers like jq.
	if list == nil {
		list = []T{}
	}
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

// Page is the --json shape of every cursor-paginated listing: the items, each
// as the API sent it, and the pageInfo the Public API pages with. A listing
// that read several pages has every page's items and the last page's
// pageInfo.
type Page[T any] struct {
	Items    []T      `json:"items"`
	PageInfo PageInfo `json:"pageInfo"`
}

// PageInfo is the Public API's pageInfo: the cursor to fetch the next page,
// and whether there is one. Both are omitted on the last page, as the API
// omits them.
type PageInfo struct {
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore,omitempty"`
}

// NewPage is the --json page of items with the cursor to the next page, empty
// on the last page. Nil items print as [], never null, for consumers like jq.
func NewPage[T any](items []T, nextCursor string) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, PageInfo: PageInfo{NextCursor: nextCursor, HasMore: nextCursor != ""}}
}

// RenderAction reports an action whose API response has no body, such as a
// delete, a removal or a cancel. As JSON it prints {key: value, done: true},
// such as {"id": "p_1", "deleted": true}, naming what was acted on; otherwise
// message, on stderr.
func RenderAction(p *Printer, asJSON bool, key, value, done, message string) error {
	if asJSON {
		return PrintJSONTo(p.ResultWriter(), map[string]any{key: value, done: true})
	}
	p.Status(message)
	return nil
}

// RenderPage prints one page of a cursor-paginated listing. As JSON it emits
// a Page so a caller can iterate; as a table it prints the rows and, when
// nextCursor is non-empty, the command that reads the next page.
func RenderPage[T any](p *Printer, asJSON bool, list []T, nextCursor, empty string, headers []string, row func(T) []string) error {
	if asJSON {
		return PrintJSONTo(p.ResultWriter(), NewPage(list, nextCursor))
	}
	if err := RenderList(p, false, list, empty, headers, row); err != nil {
		return err
	}
	if nextCursor != "" {
		p.Statusf("More results available — next page: %s", NextPageHint(nextCursor))
	}
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
