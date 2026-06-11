// Copyright 2025 LiveKit, Inc.
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
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/livekit/livekit-cli/v2/pkg/util"
)

const (
	matrixMaxSweeps    = 1
	matrixTickInterval = 50 * time.Millisecond
	matrixMinTrail     = 3
	matrixMaxTrail     = 8
)

var matrixCharset = []rune("ｦｧｨｩｪｫｬｭｮｯｰｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎ0123456789")

// The "digital rain" head + green gradient is a deliberate standalone effect (a bright
// leading glyph fading through three greens), not part of the semantic theme palette, so
// it keeps its fixed shades regardless of theme.
var (
	matrixHeadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	matrixTier1Style = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true)
	matrixTier2Style = lipgloss.NewStyle().Foreground(lipgloss.Color("34"))
	matrixTier3Style = lipgloss.NewStyle().Foreground(lipgloss.Color("22"))
)

// matrixCursorMarkerStyle uses the active theme's brand color so the cursor ties into the
// selected theme.
func matrixCursorMarkerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(util.Brand()).Bold(true)
}

// matrixRow describes the underlying text layer for one row of the rain area.
// The renderer composites rain on top of this neutral description without
// needing to know anything about the upstream domain (jobs, IDs, etc.).
type matrixRow struct {
	text         []rune
	iconCol      int // -1 if no icon cell on this row
	iconCh       rune
	iconStyle    *lipgloss.Style // applied when rain is not on iconCol
	cursorMarker bool            // render a ▸ in col 0 when true
}

// Cell category tags used for coalescing same-styled runs within a row.
const (
	mcSpace = iota
	mcPlain
	mcRainHead
	mcRainT1
	mcRainT2
	mcRainT3
	mcIcon
	mcCursor
)

type matrixTickMsg struct{}

func matrixTickCmd() tea.Cmd {
	return tea.Tick(matrixTickInterval, func(time.Time) tea.Msg {
		return matrixTickMsg{}
	})
}

// matrixAvailHeight is the shared height calculation used by both the job list
// and the matrix overlay so the rain area lines up with the list rows.
func matrixAvailHeight(h int) int {
	availHeight := max(h-14, 5)
	return availHeight
}

type matrixRain struct {
	active    bool
	width     int
	height    int
	heads     []int    // heads[col] = current head row; negative = staggered spawn
	speeds    []int    // ticks per row advance, per column
	tickCount []int    // per-column tick accumulator
	trailLen  []int    // trail length, per column
	sweeps    []int    // times head has crossed the bottom
	grid      [][]rune // height × width; frozen glyphs behind the head
	skipCol   []bool   // columns where rain is suppressed entirely
}

// start seeds the rain state. skipCol (optional, len == width) marks columns
// that should never carry a drop — used to protect the status-icon column and
// to skip columns that contain no text in any row.
func (r *matrixRain) start(width, height int, skipCol []bool) {
	if width < 1 || height < 1 {
		return
	}
	r.active = true
	r.width = width
	r.height = height
	r.heads = make([]int, width)
	r.speeds = make([]int, width)
	r.tickCount = make([]int, width)
	r.trailLen = make([]int, width)
	r.sweeps = make([]int, width)
	r.grid = make([][]rune, height)
	for i := range r.grid {
		r.grid[i] = make([]rune, width)
	}
	r.skipCol = make([]bool, width)
	if skipCol != nil {
		copy(r.skipCol, skipCol)
	}
	for i := range width {
		if r.skipCol[i] {
			// Parked: counts as already swept so it never gates auto-stop.
			r.sweeps[i] = matrixMaxSweeps
			continue
		}
		// Mild initial stagger so columns enter at slightly different times
		// without prolonging the overall animation.
		r.heads[i] = -rand.Intn(height/2 + 1)
		r.speeds[i] = 1 + rand.Intn(3)
		r.trailLen[i] = matrixMinTrail + rand.Intn(matrixMaxTrail-matrixMinTrail+1)
	}
}

func (r *matrixRain) step() {
	if !r.active {
		return
	}
	for col := 0; col < r.width; col++ {
		if r.skipCol[col] {
			continue
		}
		r.tickCount[col]++
		if r.tickCount[col] < r.speeds[col] {
			continue
		}
		r.tickCount[col] = 0
		oldHead := r.heads[col]
		if oldHead >= 0 && oldHead < r.height {
			r.grid[oldHead][col] = matrixCharset[rand.Intn(len(matrixCharset))]
		}
		r.heads[col]++
		// Let the head continue past the bottom so the trail drains out one row
		// at a time (cellKind naturally clips rows where dist > trailLen).
		// Once the whole trail has exited, count the sweep; if the column has
		// hit its sweep budget, park it so the effect fades out as each column
		// finishes independently. Otherwise respawn for another drop.
		if r.heads[col]-r.trailLen[col] >= r.height {
			r.sweeps[col]++
			if r.sweeps[col] >= matrixMaxSweeps {
				r.skipCol[col] = true
				continue
			}
			r.heads[col] = -r.trailLen[col] - rand.Intn(r.height+1)
			r.speeds[col] = 1 + rand.Intn(2)
			r.trailLen[col] = matrixMinTrail + rand.Intn(matrixMaxTrail-matrixMinTrail+1)
		}
	}
	minSweeps := r.sweeps[0]
	for _, s := range r.sweeps[1:] {
		if s < minSweeps {
			minSweeps = s
		}
	}
	if minSweeps >= matrixMaxSweeps {
		r.active = false
	}
}

// cellKind classifies a given (row, col) against the current drop state.
//
//	-1 = no rain here
//	 0 = head
//	 1 = tier 1 (1-2 above head)
//	 2 = tier 2 (3-5 above head)
//	 3 = tier 3 (6..trailLen above head)
func (r *matrixRain) cellKind(row, col int) int {
	if r.skipCol[col] {
		return -1
	}
	h := r.heads[col]
	if h < 0 || row > h {
		return -1
	}
	dist := h - row
	if dist == 0 {
		return 0
	}
	if dist > r.trailLen[col] {
		return -1
	}
	switch {
	case dist <= 2:
		return 1
	case dist <= 5:
		return 2
	default:
		return 3
	}
}

// matrixHeadChar returns a fresh random glyph for a head cell.
func matrixHeadChar() rune {
	return matrixCharset[rand.Intn(len(matrixCharset))]
}

// matrixTrailChar returns the frozen glyph at (row, col) if set, otherwise a
// fresh random glyph. Callers must already know the cell is part of the trail.
func matrixTrailChar(r *matrixRain, row, col int) rune {
	if row >= 0 && row < r.height && col >= 0 && col < r.width && r.grid[row][col] != 0 {
		return r.grid[row][col]
	}
	return matrixCharset[rand.Intn(len(matrixCharset))]
}

// render composites the current rain frame over the supplied text rows. Each
// cell is classified into a rain tier or a text category (icon, dim, plain,
// space, cursor marker) and runs of the same category coalesce into a single
// styled render call to keep ANSI output compact.
func (r *matrixRain) render(rows []matrixRow) string {
	if r.width < 1 {
		return ""
	}
	var b strings.Builder
	b.Grow(r.height * r.width * 8)

	cats := make([]int, r.width)
	runes := make([]rune, r.width)

	for row := 0; row < r.height; row++ {
		var rd matrixRow
		rd.iconCol = -1
		if row < len(rows) {
			rd = rows[row]
		}

		for col := 0; col < r.width; col++ {
			switch r.cellKind(row, col) {
			case 0:
				cats[col] = mcRainHead
				runes[col] = matrixHeadChar()
				continue
			case 1:
				cats[col] = mcRainT1
				runes[col] = matrixTrailChar(r, row, col)
				continue
			case 2:
				cats[col] = mcRainT2
				runes[col] = matrixTrailChar(r, row, col)
				continue
			case 3:
				cats[col] = mcRainT3
				runes[col] = matrixTrailChar(r, row, col)
				continue
			}
			// Not rain — pick the text-layer category for this cell.
			switch {
			case col == 0 && rd.cursorMarker:
				cats[col] = mcCursor
				runes[col] = '▸'
			case col == rd.iconCol && rd.iconStyle != nil:
				cats[col] = mcIcon
				runes[col] = rd.iconCh
			case col < len(rd.text):
				cats[col] = mcPlain
				runes[col] = rd.text[col]
			default:
				cats[col] = mcSpace
				runes[col] = ' '
			}
		}

		runStart := 0
		for col := 1; col <= r.width; col++ {
			if col == r.width || cats[col] != cats[runStart] {
				writeMatrixRun(&b, cats[runStart], runes[runStart:col], rd.iconStyle)
				runStart = col
			}
		}
		if len(rd.text) > r.width {
			b.WriteString(string(rd.text[r.width:]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func writeMatrixRun(b *strings.Builder, cat int, rs []rune, iconStyle *lipgloss.Style) {
	if len(rs) == 0 {
		return
	}
	s := string(rs)
	switch cat {
	case mcRainHead:
		b.WriteString(matrixHeadStyle.Render(s))
	case mcRainT1:
		b.WriteString(matrixTier1Style.Render(s))
	case mcRainT2:
		b.WriteString(matrixTier2Style.Render(s))
	case mcRainT3:
		b.WriteString(matrixTier3Style.Render(s))
	case mcIcon:
		if iconStyle != nil {
			b.WriteString(iconStyle.Render(s))
		} else {
			b.WriteString(s)
		}
	case mcCursor:
		b.WriteString(matrixCursorMarkerStyle().Render(s))
	default:
		b.WriteString(s)
	}
}
