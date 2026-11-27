package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// The spectrum analyzer lives on one shared hardware tuner, so these views are
// deliberately single-capture: one 6 MHz trace per refresh, never two sweeps
// in flight. Everything is drawn from the points the firmware returned; the
// requested window is only a hint.

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func clipLines(lines []string, width, height int) []string {
	if height > 4 && len(lines) > height {
		lines = append(lines[:height-1], lines[len(lines)-1])
	}
	if width > 0 {
		for index, line := range lines {
			lines[index] = truncate(line, width)
		}
	}
	return lines
}

// SpectrumFrame draws the live trace plus the analyzer's channel table.
func SpectrumFrame(header string, centerMHz float64, sweep *hitron.SpectrumSweep, channels []hitron.SpectrumChannel, frames []hitron.RealTimeFrame, width, height int, errText string) []string {
	lines := []string{header}
	if errText != "" {
		lines = append(lines, "  "+Red("error: ")+errText)
		if sweep == nil || len(sweep.Points) == 0 {
			return clipLines(append(lines, "", "  "+Dim("r retry   1 stats   2 spectrum   3 constellation   q quit")), width, height)
		}
		lines = append(lines, "  "+Dim("showing the last good capture"))
	}

	if sweep == nil {
		lines = append(lines, "", "  "+Dim("waiting for the first capture..."),
			"", "  "+Dim("r retry   1 stats   2 spectrum   3 constellation   q quit"))
		return clipLines(lines, width, height)
	}
	if len(sweep.Points) == 0 {
		lines = append(lines,
			fmt.Sprintf("  %s %.1f MHz   %s", Bold("spectrum"), centerMHz, Yellow("no samples in this window")),
			"",
			"  "+Dim("The tuner returns an empty trace outside its capture range; try , or . to tune."),
			"", "  "+Dim("r retry   /  tune   q quit"))
		return clipLines(lines, width, height)
	}

	minPower, maxPower, avgPower := spectrumStats(sweep.Points)
	lines = append(lines, fmt.Sprintf(
		"  %s %.1f MHz   %s   %s   %s   %s   %s",
		Bold("spectrum"),
		centerMHz,
		Dim(fmt.Sprintf("%.1f–%.1f MHz", sweep.FromMHz, sweep.ToMHz)),
		Dim(fmt.Sprintf("%d pts", len(sweep.Points))),
		Dim("min ")+fmt.Sprintf("%+.1f", minPower),
		Dim("max ")+fmt.Sprintf("%+.1f", maxPower),
		Dim("avg ")+fmt.Sprintf("%+.1f dBmV", avgPower),
	))
	if actual := (sweep.FromMHz + sweep.ToMHz) / 2; math.Abs(actual-centerMHz) > 4 {
		lines = append(lines, "  "+Yellow(fmt.Sprintf(
			"capture is centerd at %.1f MHz, not %.1f; the shared tuner may still be settling — press r",
			actual, centerMHz)))
	}

	plotWidth := width - 9
	if plotWidth < 20 {
		plotWidth = width - 2
	}
	rows := clampInt(height-14, 5, 18)
	lines = append(lines, "")
	lines = append(lines, spectrumChart(sweep.Points, plotWidth, rows, width >= 40)...)

	if frame, ok := lastFrame(frames); ok {
		lines = append(lines, "", "  "+Dim("RTData "+frame.Time))
		for _, row := range packTokens(realTimeTokens(frame), width-4) {
			lines = append(lines, "    "+Dim(row))
		}
	}

	if len(channels) > 0 {
		lines = append(lines, "  "+Dim("channels ")+channelStrip(channels, centerMHz))
	}

	lines = append(lines, "",
		"  "+Dim("q quit  r refresh  a auto  1 stats  2 spectrum  3 constellation  [/] channel  , . tune  z FEC reset  h help"))
	return clipLines(lines, width, height)
}

// ConstellationFrame draws one downstream port's I/Q cloud.
func ConstellationFrame(header string, cons *hitron.Constellation, channels []hitron.SpectrumChannel, width, height int, errText string) []string {
	lines := []string{header}
	if errText != "" {
		lines = append(lines, "  "+Red("error: ")+errText)
		if cons == nil || len(cons.Points) == 0 {
			return clipLines(append(lines, "", "  "+Dim("r retry   1 stats   2 spectrum   3 constellation   q quit")), width, height)
		}
		lines = append(lines, "  "+Dim("showing the last good capture"))
	}
	if cons == nil {
		lines = append(lines, "", "  "+Dim("waiting for the first capture..."),
			"", "  "+Dim("r retry   1 stats   2 spectrum   3 constellation   q quit"))
		return clipLines(lines, width, height)
	}

	name := channelName(channels, cons.PortID)
	meta := fmt.Sprintf("port %d", cons.PortID)
	if name != "" {
		meta += " · " + name
	}
	if cons.Modulation != "" {
		meta += " · " + cons.Modulation
	}
	if cons.BandwidthMHz > 0 {
		meta += fmt.Sprintf(" · %.1f MHz", cons.BandwidthMHz)
	}
	if cons.MERdB > 0 {
		meta += fmt.Sprintf(" · MER %.1f dB", cons.MERdB)
	}
	if cons.PowerDBmV != 0 {
		meta += fmt.Sprintf(" · %+.1f dBmV", cons.PowerDBmV)
	}
	meta += fmt.Sprintf(" · %d pts", len(cons.Points))
	lines = append(lines, "  "+Bold("constellation")+"  "+Dim(meta))

	if len(cons.Points) == 0 {
		lines = append(lines,
			"",
			"  "+Yellow("no constellation points on this port"),
			"  "+Dim("Inactive ports answer 200 with an empty cloud; use [/] to pick a locked channel."),
			"", "  "+Dim("r retry   1 stats   2 spectrum   3 constellation   q quit"))
		return clipLines(lines, width, height)
	}

	rows := clampInt(height-10, 8, 22)
	cols := width - 10
	if cols > rows*2 {
		cols = rows * 2
	}
	cols = clampInt(cols, 20, 120)

	lines = append(lines, "")
	lines = append(lines, constellationChart(cons.Points, cols, rows)...)

	lines = append(lines, "",
		"  "+Dim("q quit  r refresh  a auto  1 stats  2 spectrum  3 constellation  [/] channel  z FEC reset  h help"))
	return clipLines(lines, width, height)
}

func spectrumStats(points []hitron.SpectrumPoint) (min, max, avg float64) {
	min, max = points[0].PowerDBmV, points[0].PowerDBmV
	sum := 0.0
	for _, point := range points {
		if point.PowerDBmV < min {
			min = point.PowerDBmV
		}
		if point.PowerDBmV > max {
			max = point.PowerDBmV
		}
		sum += point.PowerDBmV
	}
	return min, max, sum / float64(len(points))
}

// spectrumChart renders the trace as a braille line. Braille gives two dots
// per column and four per row. The analyzer returns 300 samples for a 6 MHz
// window, which is far more than a terminal has sub-columns, so samples are
// averaged into each sub-column first: connecting every raw sample would fill
// the noise band with static and bury the shape of the channel.
func spectrumChart(points []hitron.SpectrumPoint, plotWidth, rows int, labeled bool) []string {
	if plotWidth < 1 || rows < 1 || len(points) == 0 {
		return nil
	}
	from, to := points[0].FreqMHz, points[len(points)-1].FreqMHz
	if to <= from {
		to = from + 1
	}

	subWidth, subHeight := plotWidth*2, rows*4
	sums := make([]float64, subWidth)
	counts := make([]int, subWidth)
	for _, point := range points {
		x := clampInt(int(math.Round((point.FreqMHz-from)/(to-from)*float64(subWidth-1))), 0, subWidth-1)
		sums[x] += point.PowerDBmV
		counts[x]++
	}
	values := make([]float64, 0, subWidth)
	for index := range sums {
		if counts[index] > 0 {
			values = append(values, sums[index]/float64(counts[index]))
		}
	}

	ymin, ymax := spectrumRange(values)
	if ymax-ymin < 8 {
		mid := (ymin + ymax) / 2
		ymin, ymax = math.Floor(mid-4), math.Ceil(mid+4)
	}

	grid := make([][]uint8, rows)
	for row := range grid {
		grid[row] = make([]uint8, plotWidth)
	}
	set := func(x, y int) {
		if x < 0 || x >= subWidth || y < 0 || y >= subHeight {
			return
		}
		grid[y/4][x/2] |= brailleBit[x%2][y%4]
	}

	previous := [2]int{}
	havePrevious := false
	for x, count := range counts {
		if count == 0 {
			havePrevious = false
			continue
		}
		power := sums[x] / float64(count)
		y := clampInt(int(math.Round((ymax-power)/(ymax-ymin)*float64(subHeight-1))), 0, subHeight-1)
		if havePrevious {
			brailleLine(previous[0], previous[1], x, y, set)
		} else {
			set(x, y)
		}
		previous = [2]int{x, y}
		havePrevious = true
	}

	lines := make([]string, 0, rows+3)
	for row := 0; row < rows; row++ {
		var trace strings.Builder
		for column := 0; column < plotWidth; column++ {
			bits := grid[row][column]
			if bits == 0 {
				trace.WriteByte(' ')
				continue
			}
			trace.WriteRune(rune(0x2800) + rune(bits))
		}
		value := ymax - (ymax-ymin)*float64(row)/float64(rows-1)
		label := ""
		if labeled && (row == 0 || row == rows-1 || row == rows/2) {
			label = fmt.Sprintf("%+5.0f │", value)
		} else if labeled {
			label = "      │"
		}
		lines = append(lines, "  "+Dim(label)+Cyan(trace.String()))
	}

	if labeled {
		lines = append(lines, "  "+Dim("      └"+strings.Repeat("─", plotWidth)))
		lines = append(lines, axisLabels(from, to, plotWidth))
	}
	return lines
}

// spectrumRange picks the vertical axis. The tuner's capture window has a
// 30–40 dB anti-alias roll-off at both ends, and taking the raw min/max lets
// those two edges squash the channel into a few rows. Trimming the outer 5% of
// the samples keeps the trace readable; points outside the range are pinned to
// the edge, and the header still reports the true minimum and maximum.
func spectrumRange(values []float64) (float64, float64) {
	if len(values) == 0 {
		return -30, 10
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	trim := len(sorted) / 20
	low := sorted[trim]
	high := sorted[len(sorted)-1-trim]
	if high-low < 4 {
		mid := (low + high) / 2
		low, high = mid-2, mid+2
	}
	return math.Floor(low) - 1, math.Ceil(high) + 1
}

// brailleBit maps a dot position inside a braille cell to its Unicode bit.
var brailleBit = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40}, // left column, rows top to bottom
	{0x08, 0x10, 0x20, 0x80}, // right column
}

// brailleLine is Bresenham over the sub-cell dot grid, so consecutive samples
// are connected even when the power jumps several rows.
func brailleLine(x0, y0, x1, y1 int, set func(int, int)) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y0
	if dy > 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		set(x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		step := 2 * err
		if step >= dy {
			err += dy
			x0 += sx
		}
		if step <= dx {
			err += dx
			y0 += sy
		}
	}
}

func axisLabels(from, to float64, plotWidth int) string {
	const base = 9 // two indent + five y-label + " │" puts the trace at column 9
	left := fmt.Sprintf("%.1f", from)
	right := fmt.Sprintf("%.1f", to)
	mid := fmt.Sprintf("%.1f MHz", (from+to)/2)
	row := make([]rune, base+plotWidth)
	for index := range row {
		row[index] = ' '
	}
	copy(row[base:], []rune(left))
	copy(row[base+plotWidth-len([]rune(right)):], []rune(right))
	start := base + (plotWidth-len([]rune(mid)))/2
	if start < base+len([]rune(left))+1 {
		start = base + len([]rune(left)) + 1
	}
	if start+len([]rune(mid)) <= base+plotWidth-len([]rune(right)) {
		copy(row[start:], []rune(mid))
	}
	return string(row)
}

// constellationChart plots I/Q on a canvas twice as wide as it is tall, which
// approximates square terminal cells. Zero axes are drawn only through empty
// cells so they never hide a sample.
func constellationChart(points []hitron.IQPoint, cols, rows int) []string {
	scale := 1.0
	for _, point := range points {
		if value := math.Abs(point.I); value > scale {
			scale = value
		}
		if value := math.Abs(point.Q); value > scale {
			scale = value
		}
	}
	scale = math.Ceil(scale*1.1*2) / 2

	counts := make([][]int, rows)
	for row := range counts {
		counts[row] = make([]int, cols)
	}
	for _, point := range points {
		column := int(math.Round((point.I + scale) / (2 * scale) * float64(cols-1)))
		row := int(math.Round((scale - point.Q) / (2 * scale) * float64(rows-1)))
		counts[clampInt(row, 0, rows-1)][clampInt(column, 0, cols-1)]++
	}

	zeroCol := int(math.Round(scale / (2 * scale) * float64(cols-1)))
	zeroRow := int(math.Round(scale / (2 * scale) * float64(rows-1)))

	canvas := make([][]rune, rows)
	for row := range canvas {
		canvas[row] = make([]rune, cols)
		for column := range canvas[row] {
			switch {
			case counts[row][column] >= 4:
				canvas[row][column] = '●'
			case counts[row][column] >= 2:
				canvas[row][column] = '•'
			case counts[row][column] == 1:
				canvas[row][column] = '·'
			case row == zeroRow && column == zeroCol:
				canvas[row][column] = '┼'
			case row == zeroRow:
				canvas[row][column] = '─'
			case column == zeroCol:
				canvas[row][column] = '│'
			default:
				canvas[row][column] = ' '
			}
		}
	}

	lines := make([]string, 0, rows+3)
	for row := 0; row < rows; row++ {
		value := scale - 2*scale*float64(row)/float64(rows-1)
		label := "     "
		switch row {
		case 0:
			label = fmt.Sprintf("%+5.0f", value)
		case rows / 2:
			label = "    0"
		case rows - 1:
			label = fmt.Sprintf("%+5.0f", value)
		}
		lines = append(lines, "  "+Dim(label+" │")+Cyan(string(canvas[row])))
	}
	lines = append(lines, "  "+Dim("      └"+strings.Repeat("─", cols)))
	lines = append(lines, constellationAxisLabels(scale, cols))

	return lines
}

func constellationAxisLabels(scale float64, cols int) string {
	const base = 9
	left := fmt.Sprintf("%+.0f", -scale)
	right := fmt.Sprintf("%+.0f", scale)
	mid := "0  I"
	row := make([]rune, base+cols)
	for index := range row {
		row[index] = ' '
	}
	copy(row[base:], []rune(left))
	copy(row[base+cols-len([]rune(right)):], []rune(right))
	start := base + (cols-len([]rune(mid)))/2
	if start > base+len([]rune(left))+1 && start+len([]rune(mid)) < base+cols-len([]rune(right)) {
		copy(row[start:], []rune(mid))
	}
	return string(row)
}

func lastFrame(frames []hitron.RealTimeFrame) (hitron.RealTimeFrame, bool) {
	if len(frames) == 0 {
		return hitron.RealTimeFrame{}, false
	}
	return frames[len(frames)-1], true
}

func realTimeTokens(frame hitron.RealTimeFrame) []string {
	tokens := make([]string, 0, len(frame.Channels))
	for _, channel := range frame.Channels {
		tokens = append(tokens, fmt.Sprintf("%.0f %+.1f/%.0f", channel.FreqMHz, channel.PowerDBmV, channel.MERdB))
	}
	return tokens
}

func packTokens(tokens []string, width int) []string {
	if len(tokens) == 0 {
		return nil
	}
	if width < 20 {
		width = 20
	}
	lines := make([]string, 0, 2)
	current := ""
	for _, token := range tokens {
		next := token
		if current != "" {
			next = current + " · " + token
		}
		if len(next) > width && current != "" {
			lines = append(lines, current)
			current = token
			continue
		}
		current = next
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func channelStrip(channels []hitron.SpectrumChannel, centerMHz float64) string {
	parts := make([]string, 0, len(channels))
	for _, channel := range channels {
		label := fmt.Sprintf("%.0f", channel.FreqMHz)
		if math.Abs(channel.FreqMHz-centerMHz) < 0.5 {
			parts = append(parts, Cyan("["+label+"]"))
		} else {
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, " ")
}

func channelName(channels []hitron.SpectrumChannel, portID int) string {
	for _, channel := range channels {
		if channel.PortID == portID {
			return fmt.Sprintf("%.1f MHz", channel.FreqMHz)
		}
	}
	return ""
}
