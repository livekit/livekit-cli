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

// Conventional CLI output: results go to stdout, everything else (status,
// diagnostics, warnings) goes to stderr. The stream split alone keeps redirected
// or piped result data clean; --quiet silences informational status without
// touching warnings or errors. TTY-gated decoration (color, spinners) is handled
// by lipgloss/termenv and the huh spinner respectively.

package util

import (
	"fmt"
	"io"
	"os"
)

// Printer is a single sink for human-facing CLI output. One instance per process
// is initialized from the root command and reused everywhere, so all status,
// warning, and result lines share consistent streams and gating.
type Printer struct {
	Out   io.Writer // primary output: data the user might pipe or redirect
	Err   io.Writer // status, warnings, diagnostics
	Quiet bool      // suppresses Status (warnings and errors still print)
}

// NewPrinter builds a Printer targeting the given writers. Pass nil to default
// to os.Stdout / os.Stderr; this is the path tests use with bytes.Buffer.
func NewPrinter(out, err io.Writer, quiet bool) *Printer {
	if out == nil {
		out = os.Stdout
	}
	if err == nil {
		err = os.Stderr
	}
	return &Printer{Out: out, Err: err, Quiet: quiet}
}

// Status writes an informational breadcrumb to stderr ("Using project [X]",
// "Cloning template…"). Suppressed by --quiet. A trailing newline is appended.
func (p *Printer) Status(a ...any) {
	if p == nil || p.Quiet {
		return
	}
	fmt.Fprintln(p.Err, a...)
}

// Statusf is Printf-style Status.
func (p *Printer) Statusf(format string, a ...any) {
	if p == nil || p.Quiet {
		return
	}
	fmt.Fprintf(p.Err, ensureNewline(format), a...)
}

// Warnf writes a warning to stderr. NOT suppressed by --quiet — warnings are
// always worth surfacing.
func (p *Printer) Warnf(format string, a ...any) {
	if p == nil {
		return
	}
	fmt.Fprintf(p.Err, ensureNewline(format), a...)
}

// Result writes the command's primary output to stdout. Always printed.
func (p *Printer) Result(a ...any) {
	if p == nil {
		return
	}
	fmt.Fprintln(p.Out, a...)
}

// Resultf is Printf-style Result.
func (p *Printer) Resultf(format string, a ...any) {
	if p == nil {
		return
	}
	fmt.Fprintf(p.Out, format, a...)
}

func ensureNewline(s string) string {
	if len(s) == 0 || s[len(s)-1] != '\n' {
		return s + "\n"
	}
	return s
}
