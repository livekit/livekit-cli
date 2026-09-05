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
	"context"
	"fmt"
	"io"
	"os"

	"charm.land/huh/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-isatty"
)

// Printer is a single sink for human-facing CLI output. One instance per process
// is initialized from the root command and reused everywhere, so all status,
// warning, and result lines share consistent streams and gating.
type Printer struct {
	Out   io.Writer // primary output: data the user might pipe or redirect
	Err   io.Writer // status, warnings, diagnostics
	Quiet bool      // suppresses Status (warnings and errors still print)

	// interactive reports whether Err is a real terminal. It gates decoration
	// (spinners) — never content — so redirected or piped runs stay clean.
	interactive bool

	// rawErr is Err before the color-profile wrapper. Bubble Tea needs the
	// underlying file to drive the terminal, so the spinner gets this one.
	rawErr io.Writer
}

// colorWriter adapts w to the color support of its destination. Lip Gloss v2
// renders styles unconditionally — unlike v1, whose renderer consulted the
// terminal's capabilities — so the downsampling has to happen on the way out:
// truecolor degrades to ANSI256/ANSI where that's all the terminal has, and
// color is dropped entirely for non-terminals and under NO_COLOR.
func colorWriter(w io.Writer) io.Writer {
	return colorprofile.NewWriter(w, os.Environ())
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
	return &Printer{
		Out:         colorWriter(out),
		Err:         colorWriter(err),
		Quiet:       quiet,
		interactive: isTerminal(err),
		rawErr:      err,
	}
}

// Interactive reports whether status output is going to a real terminal, so
// callers can gate terminal-only escapes (e.g. OSC 8 hyperlinks) and avoid
// leaking them into piped or redirected output.
func (p *Printer) Interactive() bool {
	return p != nil && p.interactive
}

// isTerminal reports whether w is a terminal-backed *os.File. Non-file writers
// (bytes.Buffer in tests, pipes) are never terminals.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
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

// The writer accessors below are for code that streams output through an
// io.Writer; each carries the same gating as its print-method counterpart.

// StatusWriter is the streaming counterpart of Status: io.Discard under --quiet.
func (p *Printer) StatusWriter() io.Writer {
	if p == nil || p.Quiet {
		return io.Discard
	}
	return p.Err
}

// WarnWriter is the streaming counterpart of Warnf: never silenced.
func (p *Printer) WarnWriter() io.Writer {
	if p == nil {
		return io.Discard
	}
	return p.Err
}

// ResultWriter is the streaming counterpart of Result: always printed.
func (p *Printer) ResultWriter() io.Writer {
	if p == nil {
		return io.Discard
	}
	return p.Out
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

// Await runs action while showing a spinner, then returns the action's error.
//
// The spinner is decoration: it only animates when the Printer targets an interactive
// terminal and is not in quiet mode. Otherwise Await emits the title once as a plain
// status line (itself suppressed by --quiet) and runs the action without animation, so
// redirected/piped/CI output stays free of escape sequences and --quiet stays silent.
func (p *Printer) Await(title string, ctx context.Context, action func(ctx context.Context) error) error {
	if p == nil {
		return action(ctx)
	}
	if p.Quiet || !p.interactive {
		p.Status(title)
		return action(ctx)
	}
	// v2 replaced the individual Style/Output setters with a theme and WithOutput.
	// Only the spinner glyph is themed; the title stays unstyled, as before.
	spinnerTheme := spinner.ThemeFunc(func(bool) *spinner.Styles {
		return &spinner.Styles{Spinner: Theme.Focused.Title, Title: lipgloss.NewStyle()}
	})
	return spinner.New().
		Title(" " + title).
		ActionWithErr(action).
		Type(spinner.Pulse).
		WithTheme(spinnerTheme).
		WithOutput(p.rawErr).
		Context(ctx).
		Run()
}
