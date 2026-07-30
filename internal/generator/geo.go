package generator

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
)

type point2 [2]float64

type focusSpec struct {
	kind   string
	city   string
	coords point2
	radius float64
}

func distance(a, b point2) float64 {
	dx := b[0] - a[0]
	dy := b[1] - a[1]
	return math.Sqrt(dx*dx + dy*dy)
}

func add(a, b point2) point2 {
	return point2{a[0] + b[0], a[1] + b[1]}
}

func rotate(p point2, theta float64, origin point2) point2 {
	sinT := math.Sin(theta)
	cosT := math.Cos(theta)
	return point2{
		cosT*(p[0]-origin[0]) - sinT*(p[1]-origin[1]) + origin[0],
		sinT*(p[0]-origin[0]) + cosT*(p[1]-origin[1]) + origin[1],
	}
}

func nonUniformScale(p point2, scale point2, origin point2) point2 {
	return point2{
		(p[0]-origin[0])*scale[0] + origin[0],
		(p[1]-origin[1])*scale[1] + origin[1],
	}
}

func toSplinePoints(startPos point2, startRotation float64, endPos point2, endRotation float64, steps int) []point2 {
	if steps <= 0 {
		steps = defaultSplineSteps(startRotation, endRotation)
	}
	dist := distance(startPos, endPos)
	m0 := point2{math.Cos(startRotation) * dist, math.Sin(startRotation) * dist}
	m1 := point2{math.Cos(endRotation) * dist, math.Sin(endRotation) * dist}

	points := make([]point2, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		t2 := t * t
		t3 := t2 * t
		p := add(
			scale(startPos, 2*t3-3*t2+1),
			add(
				scale(m0, t3-2*t2+t),
				add(scale(endPos, -2*t3+3*t2), scale(m1, t3-t2)),
			),
		)
		points = append(points, p)
	}
	return points
}

func defaultSplineSteps(startRotation, endRotation float64) int {
	steps := int(math.Abs(math.Tan(startRotation-endRotation))*20) + 1
	if steps < 1 {
		steps = 1
	}
	if steps > 8 {
		steps = 8
	}
	return steps
}

func scale(p point2, s float64) point2 {
	return point2{p[0] * s, p[1] * s}
}

func normalizePoint(mapName string, p point2, decimalPoints int) point2 {
	lon, lat := gameToLonLat(mapName, p[0], p[1])
	if decimalPoints >= 0 {
		factor := math.Pow10(decimalPoints)
		lon = math.Round(lon*factor) / factor
		lat = math.Round(lat*factor) / factor
	}
	return point2{lon, lat}
}

func normalizeLine(mapName string, line []point2, decimalPoints int) []point2 {
	out := make([]point2, 0, len(line))
	for _, p := range line {
		out = append(out, normalizePoint(mapName, p, decimalPoints))
	}
	return out
}

func normalizePolygon(mapName string, ring []point2, decimalPoints int) []point2 {
	out := normalizeLine(mapName, ring, decimalPoints)
	if len(out) > 0 && out[0] != out[len(out)-1] {
		out = append(out, out[0])
	}
	return out
}

func gameToLonLat(mapName string, x, y float64) (lon float64, lat float64) {
	switch mapName {
	case "usa":
		const originLat = 39.0
		const originLon = -96.0
		const factorX = -0.00017706234
		const factorY = 0.000176689948
		lon = originLon + x*factorX
		lat = originLat - y*factorY
		return
	case "europe":
		const originLat = 50.0
		const originLon = 15.0
		const offsetX = 16660.0
		const offsetY = 4150.0
		const factorX = -0.000171570875
		const factorY = 0.0001729241463

		xAdj := x - offsetX
		yAdj := y - offsetY

		const calaisX = -31100.0
		const calaisY = -5500.0
		if x < calaisX && y < calaisY {
			xAdj -= 16650
			yAdj -= 2700
		}

		lon = originLon + xAdj*factorX
		lat = originLat - yAdj*factorY
		return
	default:
		return x, y
	}
}

func ensureOutputDir(path string, dryRun bool) error {
	if dryRun {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

func writeFeatureCollection(path string, fc FeatureCollection) error {
	if fc.Type == "" {
		fc.Type = "FeatureCollection"
	}
	if fc.Features == nil {
		fc.Features = []Feature{}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func sortFeaturesByName(features []Feature) {
	sort.Slice(features, func(i, j int) bool {
		a := featureName(features[i])
		b := featureName(features[j])
		if a == b {
			return features[i].ID < features[j].ID
		}
		return a < b
	})
}

func featureName(f Feature) string {
	if f.Properties == nil {
		return ""
	}
	if n, ok := f.Properties["name"].(string); ok {
		return n
	}
	if n, ok := f.Properties["type"].(string); ok {
		return n
	}
	return ""
}
