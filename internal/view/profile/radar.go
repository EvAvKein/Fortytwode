package profile

import (
	"fmt"
	"math"
	"strings"

	"github.com/EvAvKein/Fortytwode/internal/view/model"
)

const (
	radarViewW = 320
	radarViewH = 320
	radarCX    = 160
	radarCY    = 160
	radarGridR = 140
)

// radarPoint is one vertex of the radar chart polygon or axis.
type radarPoint struct{ X, Y float64 }

// radarDot is one data point on the radar, including its 1-based index.
type radarDot struct {
	radarPoint
	Index int
}

// radarData holds everything needed to render the SVG radar chart.
type radarData struct {
	GridPaths   []string
	DataPolygon string
	DataDots    []radarDot
	Summary     string
}

// buildRadarData pre-computes all SVG coordinates for the given skills.
func buildRadarData(skills []model.SkillBar) radarData {
	n := len(skills)
	if n == 0 {
		return radarData{Summary: radarSummary(skills)}
	}
	d := radarData{
		GridPaths: make([]string, 0, 5),
		DataDots:  make([]radarDot, 0, n),
		Summary:   radarSummary(skills),
	}

	// Concentric grid polygons at 20%, 40%, 60%, 80%, 100%.
	for _, pct := range []int{20, 40, 60, 80, 100} {
		pts := make([]radarPoint, n)
		for i := range n {
			pts[i] = radarDataPoint(radarCX, radarCY, radarGridR, i, n, pct)
		}
		d.GridPaths = append(d.GridPaths, polygonPath(pts))
	}

	// Data polygon and numbered dots, scaled to the grid radius.
	dataPts := make([]radarPoint, n)
	for i, sk := range skills {
		dataPts[i] = radarDataPoint(radarCX, radarCY, radarGridR, i, n, sk.Pct)
		d.DataDots = append(d.DataDots, radarDot{radarPoint: dataPts[i], Index: sk.Index})
	}
	d.DataPolygon = polygonPath(dataPts)

	return d
}

// radarDataPoint returns the vertex for one skill, scaled by its percentage.
func radarDataPoint(cx, cy, r float64, i, n, pct int) radarPoint {
	θ := radarAngle(i, n)
	f := float64(pct) / 100.0
	return radarPoint{X: cx + r*f*math.Cos(θ), Y: cy + r*f*math.Sin(θ)}
}

// radarAngle places the first axis at 12 o'clock and proceeds clockwise.
func radarAngle(i, n int) float64 {
	if n == 0 {
		return 0
	}
	return -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
}

// polygonPath builds an SVG path "d" attribute from a list of points.
func polygonPath(points []radarPoint) string {
	if len(points) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "M %.1f %.1f", points[0].X, points[0].Y)
	for _, p := range points[1:] {
		fmt.Fprintf(&b, " L %.1f %.1f", p.X, p.Y)
	}
	b.WriteString(" Z")
	return b.String()
}

// radarSummary builds a screen-reader-friendly text summary of the skills.
func radarSummary(skills []model.SkillBar) string {
	var b strings.Builder
	b.WriteString("Skills radar: ")
	for i, sk := range skills {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %s", sk.Name, sk.Level)
	}
	return b.String()
}
