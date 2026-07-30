package generator

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed resources/villages-in-ets2.csv
var villagesCSV string

func runMap(args []string) error {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var typeValues stringSliceFlag
	var focusCity string
	var focusCoordsRaw string
	var focusRadius float64
	var includeHidden bool
	var includeDebug bool
	var skipCoalescing bool
	var inputDir string
	var outputDir string
	var dryRun bool
	var minAttrs bool

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&focusCity, "f", "", "focus city")
	fs.StringVar(&focusCity, "focusCity", "", "focus city")
	fs.StringVar(&focusCoordsRaw, "c", "", "focus game coords")
	fs.StringVar(&focusCoordsRaw, "focusGameCoords", "", "focus game coords")
	fs.Float64Var(&focusRadius, "r", 5000, "focus radius")
	fs.Float64Var(&focusRadius, "focusRadius", 5000, "focus radius")
	fs.BoolVar(&includeHidden, "h", false, "include hidden")
	fs.BoolVar(&includeHidden, "includeHidden", false, "include hidden")
	fs.BoolVar(&includeDebug, "d", false, "include debug")
	fs.BoolVar(&includeDebug, "includeDebug", false, "include debug")
	fs.BoolVar(&skipCoalescing, "skipCoalescing", false, "skip coalescing")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	fs.Var(&typeValues, "t", "output type")
	fs.Var(&typeValues, "type", "output type")
	fs.BoolVar(&dryRun, "dryRun", false, "dry run")
	fs.BoolVar(&minAttrs, "minAttrs", true, "minimal attrs for tiles")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if focusCity != "" && focusCoordsRaw != "" {
		return errors.New("focusCity conflicts with focusGameCoords")
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}
	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, dryRun); err != nil {
		return err
	}

	mapName, err := parseSingleMapOption(mapValues)
	if err != nil {
		return err
	}
	types, err := parseTypeOptions(typeValues, []string{"pmtiles"}, map[string]bool{
		"pmtiles": true,
		"mbtiles": true,
		"geojson": true,
	})
	if err != nil {
		return err
	}

	allCities, err := readJSONArray[City](toJSONPath(inputDir, mapName, "cities"))
	if err != nil {
		return err
	}
	focus, err := makeFocusSpec(mapName, allCities, focusCity, focusCoordsRaw, focusRadius)
	if err != nil {
		return err
	}

	cities := filterCities(allCities, focus)
	cityTokenSet := map[string]bool{}
	countryTokenSet := map[string]bool{}
	for _, city := range cities {
		cityTokenSet[strings.ToLower(city.Token)] = true
		countryTokenSet[strings.ToLower(city.CountryToken)] = true
	}

	countries, err := readJSONArrayFiltered[Country](toJSONPath(inputDir, mapName, "countries"), func(c Country) bool {
		if len(countryTokenSet) == 0 {
			return true
		}
		return countryTokenSet[strings.ToLower(c.Token)]
	})
	if err != nil {
		return err
	}

	nodes, err := readJSONArrayFiltered[Node](toJSONPath(inputDir, mapName, "nodes"), func(n Node) bool {
		return withinFocus(focus, point2{n.X, n.Y}, 1000)
	})
	if err != nil {
		nodesA, errA := readJSONArrayFiltered[Node](toJSONPath(inputDir, mapName, "nodes-1"), func(n Node) bool {
			return withinFocus(focus, point2{n.X, n.Y}, 1000)
		})
		nodesB, errB := readJSONArrayFiltered[Node](toJSONPath(inputDir, mapName, "nodes-2"), func(n Node) bool {
			return withinFocus(focus, point2{n.X, n.Y}, 1000)
		})
		if errA != nil || errB != nil {
			return err
		}
		nodes = append(nodesA, nodesB...)
	}
	nodeByUID := buildNodeLookup(nodes)

	roads, err := readJSONArrayFiltered[Road](toJSONPath(inputDir, mapName, "roads"), func(r Road) bool {
		if !includeHidden && r.Hidden {
			return false
		}
		return withinFocus(focus, point2{r.X, r.Y}, 0)
	})
	if err != nil {
		return err
	}

	ferries, _ := readJSONArrayFiltered[Ferry](toJSONPath(inputDir, mapName, "ferries"), func(f Ferry) bool {
		return withinFocus(focus, point2{f.X, f.Y}, 0)
	})

	prefabs, _ := readJSONArrayFiltered[Prefab](toJSONPath(inputDir, mapName, "prefabs"), func(p Prefab) bool {
		if !includeHidden && p.Hidden {
			return false
		}
		return withinFocus(focus, point2{p.X, p.Y}, 0)
	})

	mapAreas, _ := readJSONArrayFiltered[MapArea](toJSONPath(inputDir, mapName, "mapAreas"), func(a MapArea) bool {
		return withinFocus(focus, point2{a.X, a.Y}, 200)
	})

	pois, _ := readJSONArrayFiltered[POI](toJSONPath(inputDir, mapName, "pois"), func(p POI) bool {
		return withinFocus(focus, point2{p.X, p.Y}, 0)
	})

	roadLooks, _ := readJSONArray[RoadLook](toJSONPath(inputDir, mapName, "roadLooks"))
	roadLookByToken := buildRoadLookLookup(roadLooks)

	features := make([]Feature, 0, len(roads)+len(prefabs)+len(mapAreas)+len(cities)+len(countries)+len(pois))

	for _, area := range mapAreas {
		ring := polygonFromNodeUIDs(area.NodeUIDs, nodeByUID)
		if len(ring) < 4 {
			ring = squareAround(point2{area.X, area.Y}, 80)
		}
		ring = normalizePolygon(mapName, ring, 6)
		features = append(features, Feature{
			Type: "Feature",
			ID:   area.UID.Key(),
			Geometry: Geometry{
				Type:        "Polygon",
				Coordinates: []any{toCoordinateArray(ring)},
			},
			Properties: map[string]any{
				"type":     "mapArea",
				"dlcGuard": area.DlcGuard,
				"zIndex":   0,
				"color":    area.Color,
			},
		})
	}

	for _, prefab := range prefabs {
		ring := polygonFromNodeUIDs(prefab.NodeUIDs, nodeByUID)
		if len(ring) < 4 {
			ring = squareAround(point2{prefab.X, prefab.Y}, 50)
		}
		ring = normalizePolygon(mapName, ring, 6)
		features = append(features, Feature{
			Type: "Feature",
			ID:   prefab.UID.Key(),
			Geometry: Geometry{
				Type:        "Polygon",
				Coordinates: []any{toCoordinateArray(ring)},
			},
			Properties: map[string]any{
				"type":     "prefab",
				"dlcGuard": prefab.DlcGuard,
				"zIndex":   10,
				"color":    "Road",
			},
		})
	}

	for _, road := range roads {
		start, okA := nodeByUID[road.StartNodeUID.Key()]
		end, okB := nodeByUID[road.EndNodeUID.Key()]
		if !okA || !okB {
			continue
		}
		line := normalizeLine(mapName, []point2{{start.X, start.Y}, {end.X, end.Y}}, 6)
		look := roadLookByToken[road.RoadLook]
		leftLanes := len(look.LanesLeft)
		rightLanes := len(look.LanesRight)
		features = append(features, Feature{
			Type: "Feature",
			ID:   road.UID.Key(),
			Geometry: Geometry{
				Type:        "LineString",
				Coordinates: toCoordinateArray(line),
			},
			Properties: map[string]any{
				"type":       "road",
				"roadType":   inferRoadType(leftLanes, rightLanes),
				"leftLanes":  leftLanes,
				"rightLanes": rightLanes,
				"hidden":     road.Hidden,
				"dlcGuard":   road.DlcGuard,
			},
		})
	}

	for _, ferry := range ferries {
		for _, conn := range ferry.Connections {
			line := normalizeLine(mapName, []point2{{ferry.X, ferry.Y}, {conn.X, conn.Y}}, 6)
			featureType := "ferry"
			if ferry.Train {
				featureType = "train"
			}
			name := strings.TrimSpace(ferry.Name)
			if name == "" {
				name = strings.TrimSpace(conn.Name)
			}
			features = append(features, Feature{
				Type: "Feature",
				ID:   normalizeUID(ferry.Token + ":" + conn.Token),
				Geometry: Geometry{
					Type:        "LineString",
					Coordinates: toCoordinateArray(line),
				},
				Properties: map[string]any{
					"type": featureType,
					"name": name,
				},
			})
		}
	}

	for _, city := range cities {
		center := cityCenter(city)
		norm := normalizePoint(mapName, center, 6)
		features = append(features, Feature{
			Type: "Feature",
			ID:   strings.ToLower(city.Token),
			Geometry: Geometry{
				Type:        "Point",
				Coordinates: []float64{norm[0], norm[1]},
			},
			Properties: map[string]any{
				"type":      "city",
				"name":      city.Name,
				"scaleRank": 10,
				"capital":   0,
			},
		})
	}

	for _, country := range countries {
		norm := normalizePoint(mapName, point2{country.X, country.Y}, 6)
		features = append(features, Feature{
			Type: "Feature",
			ID:   strings.ToLower(country.Token),
			Geometry: Geometry{
				Type:        "Point",
				Coordinates: []float64{norm[0], norm[1]},
			},
			Properties: map[string]any{
				"type": "country",
				"name": country.Name,
				"code": country.Code,
			},
		})
	}

	for _, poi := range pois {
		norm := normalizePoint(mapName, point2{poi.X, poi.Y}, 6)
		props := map[string]any{
			"type":    "poi",
			"sprite":  poi.Icon,
			"poiType": poi.Type,
		}
		if poi.Label != "" {
			props["poiName"] = poi.Label
		}
		if poi.DlcGuard != 0 {
			props["dlcGuard"] = poi.DlcGuard
		}
		features = append(features, Feature{
			Type: "Feature",
			Geometry: Geometry{
				Type:        "Point",
				Coordinates: []float64{norm[0], norm[1]},
			},
			Properties: props,
		})
	}

	if includeDebug {
		for _, node := range nodes {
			norm := normalizePoint(mapName, point2{node.X, node.Y}, 6)
			features = append(features, Feature{
				Type: "Feature",
				ID:   node.UID.Key(),
				Geometry: Geometry{
					Type:        "Point",
					Coordinates: []float64{norm[0], norm[1]},
				},
				Properties: map[string]any{
					"type": "debug",
					"name": "node",
				},
			})
		}
	}

	if !skipCoalescing {
		// no-op placeholder to keep parity with source CLI option semantics.
	}

	sortFeaturesByName(features)
	fc := FeatureCollection{Type: "FeatureCollection", Features: features}

	prefix := mapPrefix(mapName)
	wroteGeojson := false
	geojsonPath := filepath.Join(outputDir, prefix+".geojson")
	if !dryRun && contains(types, "geojson") {
		if err := writeFeatureCollection(geojsonPath, fc); err != nil {
			return err
		}
		wroteGeojson = true
	}

	if dryRun {
		return nil
	}

	if contains(types, "pmtiles") || contains(types, "mbtiles") {
		cleanupGeoJSON := false
		if !wroteGeojson {
			tmpGeoJSON, err := os.CreateTemp("", prefix+"-*.geojson")
			if err != nil {
				return err
			}
			geojsonPath = tmpGeoJSON.Name()
			tmpGeoJSON.Close()
			if err := writeFeatureCollection(geojsonPath, fc); err != nil {
				return err
			}
			cleanupGeoJSON = true
		}
		if err := generateTiles(geojsonPath, outputDir, prefix, types, tippecanoeConfig{
			MinZoom: 4,
			MaxZoom: 13,
			MinAttrs: func() []string {
				if !minAttrs {
					return nil
				}
				return []string{"type", "dlcGuard", "zIndex", "height", "hidden", "poiType", "poiName", "sprite", "scaleRank", "capital", "roadType", "color", "name"}
			}(),
			Buffer:    10,
			BaseZoom:  4,
			LayerName: "",
		}); err != nil {
			return err
		}
		if cleanupGeoJSON {
			_ = os.Remove(geojsonPath)
		}
	}

	return nil
}

func runPrefabCurves(args []string) error {
	fs := flag.NewFlagSet("prefab-curves", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var focusCity string
	var focusCoordsRaw string
	var focusRadius float64
	var inputDir string
	var outputDir string

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&focusCity, "f", "", "focus city")
	fs.StringVar(&focusCity, "focusCity", "", "focus city")
	fs.StringVar(&focusCoordsRaw, "c", "", "focus game coords")
	fs.StringVar(&focusCoordsRaw, "focusGameCoords", "", "focus game coords")
	fs.Float64Var(&focusRadius, "r", 5000, "focus radius")
	fs.Float64Var(&focusRadius, "focusRadius", 5000, "focus radius")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if focusCity != "" && focusCoordsRaw != "" {
		return errors.New("focusCity conflicts with focusGameCoords")
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	mapName, err := parseSingleMapOption(mapValues)
	if err != nil {
		return err
	}

	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, false); err != nil {
		return err
	}

	allCities, err := readJSONArray[City](toJSONPath(inputDir, mapName, "cities"))
	if err != nil {
		return err
	}
	focus, err := makeFocusSpec(mapName, allCities, focusCity, focusCoordsRaw, focusRadius)
	if err != nil {
		return err
	}

	nodes, err := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes"))
	if err != nil {
		nodesA, errA := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-1"))
		nodesB, errB := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-2"))
		if errA != nil || errB != nil {
			return err
		}
		nodes = append(nodesA, nodesB...)
	}
	nodeByUID := buildNodeLookup(nodes)

	prefabs, err := readJSONArrayFiltered[Prefab](toJSONPath(inputDir, mapName, "prefabs"), func(p Prefab) bool {
		if p.Hidden {
			return false
		}
		return withinFocus(focus, point2{p.X, p.Y}, 0)
	})
	if err != nil {
		return err
	}

	descriptions, err := readJSONArray[PrefabDescription](toJSONPath(inputDir, mapName, "prefabDescriptions"))
	if err != nil {
		return err
	}
	descByToken := make(map[string]PrefabDescription, len(descriptions))
	for _, desc := range descriptions {
		descByToken[desc.Token] = desc
	}

	features := make([]Feature, 0, len(prefabs))
	for _, prefab := range prefabs {
		desc, ok := descByToken[prefab.Token]
		if !ok || len(prefab.NodeUIDs) == 0 || len(desc.Nodes) == 0 {
			continue
		}
		if prefab.OriginNodeIndex < 0 || prefab.OriginNodeIndex >= len(desc.Nodes) {
			continue
		}
		originNode, ok := nodeByUID[prefab.NodeUIDs[0].Key()]
		if !ok {
			continue
		}
		originDesc := desc.Nodes[prefab.OriginNodeIndex]
		originPos := point2{originNode.X, originNode.Y}
		prefabStart := point2{originPos[0] - originDesc.X, originPos[1] - originDesc.Y}
		rotation := originNode.Rotation - originDesc.Rotation
		transform := func(p point2) point2 {
			return rotate(add(p, prefabStart), rotation, originPos)
		}

		curveLines := make([][]point2, 0)
		usedCurveIdx := map[int]bool{}
		for _, navNode := range desc.NavNodes {
			for _, conn := range navNode.Connections {
				for _, idx := range conn.CurveIndices {
					if idx < 0 || idx >= len(desc.NavCurves) {
						continue
					}
					curve := desc.NavCurves[idx]
					pts := toSplinePoints(
						point2{curve.Start.X, curve.Start.Y}, curve.Start.Rotation,
						point2{curve.End.X, curve.End.Y}, curve.End.Rotation,
						0,
					)
					line := make([]point2, 0, len(pts))
					for _, p := range pts {
						line = append(line, normalizePoint(mapName, transform(p), 6))
					}
					curveLines = append(curveLines, line)
					usedCurveIdx[idx] = true
				}
			}
		}
		for idx, curve := range desc.NavCurves {
			if usedCurveIdx[idx] {
				continue
			}
			pts := toSplinePoints(
				point2{curve.Start.X, curve.Start.Y}, curve.Start.Rotation,
				point2{curve.End.X, curve.End.Y}, curve.End.Rotation,
				0,
			)
			line := make([]point2, 0, len(pts))
			for _, p := range pts {
				line = append(line, normalizePoint(mapName, transform(p), 6))
			}
			curveLines = append(curveLines, line)
		}

		if len(curveLines) == 0 {
			continue
		}
		coordinates := make([]any, 0, len(curveLines))
		for _, line := range curveLines {
			coordinates = append(coordinates, toCoordinateArray(line))
		}
		features = append(features, Feature{
			Type: "Feature",
			ID:   prefab.UID.Key(),
			Geometry: Geometry{
				Type:        "MultiLineString",
				Coordinates: coordinates,
			},
			Properties: map[string]any{
				"type":        "debug",
				"debugType":   "lanes",
				"prefabId":    prefab.UID.Key(),
				"prefabToken": prefab.Token,
			},
		})
	}

	outPath := filepath.Join(outputDir, fmt.Sprintf("%s-prefab-curves.geojson", mapName))
	return writeFeatureCollection(outPath, FeatureCollection{Type: "FeatureCollection", Features: features})
}

func runCities(args []string) error {
	fs := flag.NewFlagSet("cities", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var inputDir string
	var outputDir string

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	maps, err := parseMultiMapOption(mapValues)
	if err != nil {
		return err
	}
	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, false); err != nil {
		return err
	}

	features := make([]Feature, 0)
	for _, mapName := range maps {
		countries, err := readJSONArray[Country](toJSONPath(inputDir, mapName, "countries"))
		if err != nil {
			return err
		}
		cities, err := readJSONArray[City](toJSONPath(inputDir, mapName, "cities"))
		if err != nil {
			return err
		}

		countriesByToken := map[string]Country{}
		for _, country := range countries {
			countriesByToken[strings.ToLower(country.Token)] = country
		}

		for _, city := range cities {
			country, ok := countriesByToken[strings.ToLower(city.CountryToken)]
			if !ok {
				continue
			}
			code := country.Code
			if mapName == "europe" {
				if mapped, ok := ets2IsoA2[code]; ok {
					code = mapped
				}
			}
			center := cityCenter(city)
			norm := normalizePoint(mapName, center, 4)
			features = append(features, Feature{
				Type: "Feature",
				Geometry: Geometry{
					Type:        "Point",
					Coordinates: []float64{norm[0], norm[1]},
				},
				Properties: map[string]any{
					"type":        "city",
					"map":         mapName,
					"countryCode": code,
					"name":        city.Name,
				},
			})
		}

		for _, country := range countries {
			code := country.Code
			if mapName == "europe" {
				if mapped, ok := ets2IsoA2[code]; ok {
					code = mapped
				}
			}
			norm := normalizePoint(mapName, point2{country.X, country.Y}, 4)
			features = append(features, Feature{
				Type: "Feature",
				Geometry: Geometry{
					Type:        "Point",
					Coordinates: []float64{norm[0], norm[1]},
				},
				Properties: map[string]any{
					"type": "country",
					"map":  mapName,
					"code": code,
					"name": country.Name,
				},
			})
		}
	}

	sortFeaturesByName(features)
	return writeFeatureCollection(filepath.Join(outputDir, "cities.geojson"), FeatureCollection{Type: "FeatureCollection", Features: features})
}

func runEts2Villages(args []string) error {
	fs := flag.NewFlagSet("ets2-villages", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var outputDir string
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}
	outputDir = untildify(outputDir)
	if err := ensureOutputDir(outputDir, false); err != nil {
		return err
	}

	lines := strings.Split(strings.ReplaceAll(villagesCSV, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == 0 {
		return errors.New("villages resource is empty")
	}

	headers := strings.Split(filtered[0], ";")
	if len(headers) < 6 || headers[0] != "name" || headers[1] != "countryCode" || headers[2] != "xCoord" || headers[4] != "zCoord" || headers[5] != "notes" {
		return errors.New("unexpected headers in villages CSV file")
	}

	features := make([]Feature, 0, len(filtered)-1)
	for _, line := range filtered[1:] {
		parts := strings.Split(line, ";")
		if len(parts) < 6 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		countryCode := strings.TrimSpace(parts[1])
		xRaw := strings.TrimSpace(parts[2])
		yRaw := strings.TrimSpace(parts[4])
		note := strings.TrimSpace(parts[5])

		switch note {
		case "HoR":
			continue
		case "inaccessible", "":
			// keep
		default:
			// unknown notes are non-fatal
		}

		x, errX := strconvParseFloat(xRaw)
		y, errY := strconvParseFloat(yRaw)
		if errX != nil || errY != nil {
			continue
		}
		norm := normalizePoint("europe", point2{x, y}, 4)
		features = append(features, Feature{
			Type: "Feature",
			Geometry: Geometry{
				Type:        "Point",
				Coordinates: []float64{norm[0], norm[1]},
			},
			Properties: map[string]any{
				"state": countryCode,
				"name":  name,
			},
		})
	}
	return writeFeatureCollection(filepath.Join(outputDir, "ets2-villages.geojson"), FeatureCollection{Type: "FeatureCollection", Features: features})
}

func runFootprints(args []string) error {
	fs := flag.NewFlagSet("footprints", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var typeValues stringSliceFlag
	var inputDir string
	var outputDir string
	var dryRun bool

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	fs.Var(&typeValues, "t", "output type")
	fs.Var(&typeValues, "type", "output type")
	fs.BoolVar(&dryRun, "dryRun", false, "dry run")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	maps, err := parseMultiMapOption(mapValues)
	if err != nil {
		return err
	}
	types, err := parseTypeOptions(typeValues, []string{"pmtiles"}, map[string]bool{"pmtiles": true, "geojson": true})
	if err != nil {
		return err
	}

	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, dryRun); err != nil {
		return err
	}

	for _, mapName := range maps {
		nodes, err := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes"))
		if err != nil {
			nodesA, errA := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-1"))
			nodesB, errB := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-2"))
			if errA != nil || errB != nil {
				return err
			}
			nodes = append(nodesA, nodesB...)
		}
		nodeByUID := buildNodeLookup(nodes)
		models, err := readJSONArray[Model](toJSONPath(inputDir, mapName, "models"))
		if err != nil {
			return err
		}
		modelDescriptions, err := readJSONArray[ModelDescription](toJSONPath(inputDir, mapName, "modelDescriptions"))
		if err != nil {
			return err
		}
		descByToken := make(map[string]ModelDescription, len(modelDescriptions))
		for _, desc := range modelDescriptions {
			descByToken[desc.Token] = desc
		}

		features := make([]Feature, 0, len(models))
		for _, model := range models {
			node, ok := nodeByUID[model.NodeUID.Key()]
			if !ok {
				continue
			}
			desc, ok := descByToken[model.Token]
			if !ok {
				continue
			}
			scaleX := model.Scale.X
			scaleY := model.Scale.Y
			scaleZ := model.Scale.Z
			if scaleX == 0 {
				scaleX = 1
			}
			if scaleY == 0 {
				scaleY = 1
			}
			if scaleZ == 0 {
				scaleZ = 1
			}

			origin := point2{node.X + desc.Center.X, node.Y + desc.Center.Y}
			tl := add(point2{desc.Start.X, desc.Start.Y}, origin)
			tr := add(point2{desc.End.X, desc.Start.Y}, origin)
			br := add(point2{desc.End.X, desc.End.Y}, origin)
			bl := add(point2{desc.Start.X, desc.End.Y}, origin)
			rotation := node.Rotation - math.Pi/2
			corners := []point2{tl, tr, br, bl}
			for i, corner := range corners {
				rot := rotate(corner, rotation, origin)
				corners[i] = nonUniformScale(rot, point2{scaleX, scaleY}, origin)
			}
			ring := append(corners, corners[0])
			ring = normalizePolygon(mapName, ring, 6)

			features = append(features, Feature{
				Type: "Feature",
				Geometry: Geometry{
					Type:        "Polygon",
					Coordinates: []any{toCoordinateArray(ring)},
				},
				Properties: map[string]any{
					"type":   "footprint",
					"height": int(math.Round(desc.Height * scaleZ)),
				},
			})
		}

		fc := FeatureCollection{Type: "FeatureCollection", Features: features}
		prefix := mapPrefix(mapName)
		geojsonPath := filepath.Join(outputDir, fmt.Sprintf("%s-footprints.geojson", prefix))
		wroteGeoJSON := false
		if !dryRun && contains(types, "geojson") {
			if err := writeFeatureCollection(geojsonPath, fc); err != nil {
				return err
			}
			wroteGeoJSON = true
		}
		if dryRun {
			continue
		}
		if contains(types, "pmtiles") {
			cleanupGeoJSON := false
			if !wroteGeoJSON {
				tmpGeoJSON, err := os.CreateTemp("", prefix+"-footprints-*.geojson")
				if err != nil {
					return err
				}
				geojsonPath = tmpGeoJSON.Name()
				tmpGeoJSON.Close()
				if err := writeFeatureCollection(geojsonPath, fc); err != nil {
					return err
				}
				cleanupGeoJSON = true
			}
			if err := generateTiles(geojsonPath, outputDir, fmt.Sprintf("%s-footprints", prefix), []string{"pmtiles"}, tippecanoeConfig{
				MinZoom:   4,
				MaxZoom:   12,
				MinAttrs:  []string{"type", "height"},
				LayerName: "footprints",
				Buffer:    10,
				BaseZoom:  4,
			}); err != nil {
				return err
			}
			if cleanupGeoJSON {
				_ = os.Remove(geojsonPath)
			}
		}
	}
	return nil
}

func runContours(args []string) error {
	fs := flag.NewFlagSet("contours", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var typeValues stringSliceFlag
	var inputDir string
	var outputDir string
	var dryRun bool

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	fs.Var(&typeValues, "t", "output type")
	fs.Var(&typeValues, "type", "output type")
	fs.BoolVar(&dryRun, "dryRun", false, "dry run")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	maps, err := parseMultiMapOption(mapValues)
	if err != nil {
		return err
	}
	types, err := parseTypeOptions(typeValues, []string{"pmtiles"}, map[string]bool{"pmtiles": true, "geojson": true})
	if err != nil {
		return err
	}

	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, dryRun); err != nil {
		return err
	}

	for _, mapName := range maps {
		points, err := readJSONArray[[]float64](toJSONPath(inputDir, mapName, "elevation"))
		if err != nil {
			return err
		}
		byElevation := map[int][]point2{}
		for _, p := range points {
			if len(p) < 3 {
				continue
			}
			elevation := int(math.Round(p[2]))
			byElevation[elevation] = append(byElevation[elevation], normalizePoint(mapName, point2{p[0], p[1]}, 4))
		}
		elevations := make([]int, 0, len(byElevation))
		for elevation := range byElevation {
			elevations = append(elevations, elevation)
		}
		sort.Ints(elevations)
		features := make([]Feature, 0, len(elevations))
		for _, elevation := range elevations {
			coords := make([]any, 0, len(byElevation[elevation]))
			for _, point := range byElevation[elevation] {
				coords = append(coords, []float64{point[0], point[1]})
			}
			features = append(features, Feature{
				Type: "Feature",
				Geometry: Geometry{
					Type:        "MultiPoint",
					Coordinates: coords,
				},
				Properties: map[string]any{
					"elevation": elevation,
				},
			})
		}
		fc := FeatureCollection{Type: "FeatureCollection", Features: features}

		prefix := mapPrefix(mapName)
		geojsonPath := filepath.Join(outputDir, fmt.Sprintf("%s-contours.geojson", prefix))
		wroteGeoJSON := false
		if !dryRun && contains(types, "geojson") {
			if err := writeFeatureCollection(geojsonPath, fc); err != nil {
				return err
			}
			wroteGeoJSON = true
		}
		if dryRun {
			continue
		}
		if contains(types, "pmtiles") {
			cleanupGeoJSON := false
			if !wroteGeoJSON {
				tmpGeoJSON, err := os.CreateTemp("", prefix+"-contours-*.geojson")
				if err != nil {
					return err
				}
				geojsonPath = tmpGeoJSON.Name()
				tmpGeoJSON.Close()
				if err := writeFeatureCollection(geojsonPath, fc); err != nil {
					return err
				}
				cleanupGeoJSON = true
			}
			if err := generateTiles(geojsonPath, outputDir, fmt.Sprintf("%s-contours", prefix), []string{"pmtiles"}, tippecanoeConfig{
				MinZoom:   4,
				MaxZoom:   9,
				MinAttrs:  []string{"elevation"},
				LayerName: "contours",
				Buffer:    10,
				BaseZoom:  4,
			}); err != nil {
				return err
			}
			if cleanupGeoJSON {
				_ = os.Remove(geojsonPath)
			}
		}
	}
	return nil
}

func runAchievements(args []string) error {
	fs := flag.NewFlagSet("achievements", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var inputDir string
	var outputDir string
	var dryRun bool

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	fs.BoolVar(&dryRun, "dryRun", false, "dry run")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	maps, err := parseMultiMapOption(mapValues)
	if err != nil {
		return err
	}
	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, dryRun); err != nil {
		return err
	}

	for _, mapName := range maps {
		achievements, err := readRawArray(toJSONPath(inputDir, mapName, "achievements"))
		if err != nil {
			return err
		}
		cities, err := readJSONArray[City](toJSONPath(inputDir, mapName, "cities"))
		if err != nil {
			return err
		}
		countries, _ := readJSONArray[Country](toJSONPath(inputDir, mapName, "countries"))
		companies, _ := readJSONArray[CompanyItem](toJSONPath(inputDir, mapName, "companies"))

		cityByToken := map[string]City{}
		for _, city := range cities {
			cityByToken[strings.ToLower(city.Token)] = city
		}
		countryByToken := map[string]Country{}
		for _, country := range countries {
			countryByToken[strings.ToLower(country.Token)] = country
		}
		companyByToken := map[string]CompanyItem{}
		for _, company := range companies {
			companyByToken[strings.ToLower(company.Token)] = company
		}

		features := make([]Feature, 0)
		for _, achievement := range achievements {
			name := mustGetString(achievement, "token")
			if name == "" {
				name = mustGetString(achievement, "achievementName")
			}
			if name == "" {
				name = "achievement"
			}
			typ := mustGetString(achievement, "type")

			points := make([]point2, 0)
			switch typ {
			case "visitCityData":
				for _, token := range toStringSlice(mustGetArray(achievement, "cities")) {
					if city, ok := cityByToken[strings.ToLower(token)]; ok {
						points = append(points, cityCenter(city))
					}
				}
			case "delivery":
				delivery := mustGetMap(achievement, "delivery")
				switch mustGetString(delivery, "type") {
				case "city":
					for _, item := range mustGetArray(delivery, "cities") {
						if itemMap, ok := item.(map[string]any); ok {
							token := strings.ToLower(mustGetString(itemMap, "cityToken"))
							if city, ok := cityByToken[token]; ok {
								points = append(points, cityCenter(city))
							}
						}
					}
				case "company":
					for _, item := range mustGetArray(delivery, "companies") {
						itemMap, ok := item.(map[string]any)
						if !ok {
							continue
						}
						locationType := mustGetString(itemMap, "locationType")
						locationToken := strings.ToLower(mustGetString(itemMap, "locationToken"))
						switch locationType {
						case "city":
							if city, ok := cityByToken[locationToken]; ok {
								points = append(points, cityCenter(city))
							}
						case "country":
							if country, ok := countryByToken[locationToken]; ok {
								points = append(points, point2{country.X, country.Y})
							}
						}
					}
				}
			case "eachCompanyData", "deliverCargoData":
				for _, item := range mustGetArray(achievement, "companies") {
					if itemMap, ok := item.(map[string]any); ok {
						cityToken := strings.ToLower(mustGetString(itemMap, "city"))
						if city, ok := cityByToken[cityToken]; ok {
							points = append(points, cityCenter(city))
						}
					}
				}
			case "deliveryLogData":
				for _, item := range mustGetArray(achievement, "locations") {
					itemMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					locationType := mustGetString(itemMap, "type")
					switch locationType {
					case "city":
						if city, ok := cityByToken[strings.ToLower(mustGetString(itemMap, "city"))]; ok {
							points = append(points, cityCenter(city))
						}
					case "company":
						if company, ok := companyByToken[strings.ToLower(mustGetString(itemMap, "company"))]; ok {
							if city, ok := cityByToken[strings.ToLower(company.CityToken)]; ok {
								points = append(points, cityCenter(city))
							}
						}
					}
				}
			}

			for idx, point := range points {
				norm := normalizePoint(mapName, point, 6)
				features = append(features, Feature{
					Type: "Feature",
					ID:   fmt.Sprintf("%s-%d", normalizeUID(name), idx),
					Geometry: Geometry{
						Type:        "Point",
						Coordinates: []float64{norm[0], norm[1]},
					},
					Properties: map[string]any{
						"name":     name,
						"dlcGuard": 0,
					},
				})
			}
		}

		if dryRun {
			continue
		}
		prefix := mapPrefix(mapName)
		outPath := filepath.Join(outputDir, fmt.Sprintf("%s-achievements.geojson", prefix))
		if err := writeFeatureCollection(outPath, FeatureCollection{Type: "FeatureCollection", Features: features}); err != nil {
			return err
		}
	}
	return nil
}

func runGraph(args []string) error {
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var mapValues stringSliceFlag
	var inputDir string
	var outputDir string
	var check bool
	var demo bool
	var dryRun bool

	fs.Var(&mapValues, "m", "source map")
	fs.Var(&mapValues, "map", "source map")
	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")
	fs.BoolVar(&check, "c", false, "check graph")
	fs.BoolVar(&check, "check", false, "check graph")
	fs.BoolVar(&demo, "d", false, "demo graph")
	fs.BoolVar(&demo, "demo", false, "demo graph")
	fs.BoolVar(&dryRun, "dryRun", false, "dry run")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	mapName, err := parseSingleMapOption(mapValues)
	if err != nil {
		return err
	}

	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, dryRun); err != nil {
		return err
	}

	nodes, err := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes"))
	if err != nil {
		nodesA, errA := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-1"))
		nodesB, errB := readJSONArray[Node](toJSONPath(inputDir, mapName, "nodes-2"))
		if errA != nil || errB != nil {
			return err
		}
		nodes = append(nodesA, nodesB...)
	}
	nodeByUID := buildNodeLookup(nodes)
	roads, err := readJSONArray[Road](toJSONPath(inputDir, mapName, "roads"))
	if err != nil {
		return err
	}
	roadLooks, _ := readJSONArray[RoadLook](toJSONPath(inputDir, mapName, "roadLooks"))
	roadLookByToken := buildRoadLookLookup(roadLooks)
	companies, _ := readJSONArray[CompanyItem](toJSONPath(inputDir, mapName, "companies"))
	companyDefs, _ := readJSONArray[CompanyDef](toJSONPath(inputDir, mapName, "companyDefs"))

	type graphNeighbor struct {
		NodeID        string  `json:"nodeId"`
		Distance      float64 `json:"distance"`
		IsOneLaneRoad bool    `json:"isOneLaneRoad,omitempty"`
		Direction     string  `json:"direction"`
		DlcGuard      int     `json:"dlcGuard"`
	}
	type graphNeighbors struct {
		Forward  []graphNeighbor `json:"forward,omitempty"`
		Backward []graphNeighbor `json:"backward,omitempty"`
	}

	graph := map[string]*graphNeighbors{}
	getNode := func(nodeID string) *graphNeighbors {
		if v, ok := graph[nodeID]; ok {
			return v
		}
		v := &graphNeighbors{}
		graph[nodeID] = v
		return v
	}
	addNeighbor := func(from, to string, dist float64, oneWay bool, direction string, dlcGuard int, forward bool) {
		neighbor := graphNeighbor{
			NodeID:        to,
			Distance:      dist,
			Direction:     direction,
			DlcGuard:      dlcGuard,
			IsOneLaneRoad: oneWay,
		}
		n := getNode(from)
		if forward {
			n.Forward = append(n.Forward, neighbor)
		} else {
			n.Backward = append(n.Backward, neighbor)
		}
	}

	for _, road := range roads {
		start, okA := nodeByUID[road.StartNodeUID.Key()]
		end, okB := nodeByUID[road.EndNodeUID.Key()]
		if !okA || !okB {
			continue
		}
		look := roadLookByToken[road.RoadLook]
		oneWay := len(look.LanesLeft) == 0 || len(look.LanesRight) == 0
		dist := road.Length
		if dist <= 0 {
			dist = distance(point2{start.X, start.Y}, point2{end.X, end.Y})
		}
		startID := road.StartNodeUID.Key()
		endID := road.EndNodeUID.Key()

		addNeighbor(startID, endID, dist, oneWay, "forward", road.DlcGuard, true)
		addNeighbor(endID, startID, dist, oneWay, "backward", road.DlcGuard, false)
		if !oneWay {
			addNeighbor(endID, startID, dist, oneWay, "forward", road.DlcGuard, true)
			addNeighbor(startID, endID, dist, oneWay, "backward", road.DlcGuard, false)
		}
	}

	if check {
		for nodeID, neighbors := range graph {
			if _, ok := nodeByUID[nodeID]; !ok {
				return fmt.Errorf("graph node %s has no corresponding map node", nodeID)
			}
			for _, group := range [][]graphNeighbor{neighbors.Forward, neighbors.Backward} {
				for _, neighbor := range group {
					if _, ok := nodeByUID[neighbor.NodeID]; !ok {
						return fmt.Errorf("graph edge %s -> %s references missing node", nodeID, neighbor.NodeID)
					}
				}
			}
		}
	}

	if dryRun {
		return nil
	}

	prefix := mapName
	if demo {
		type demoNeighbor struct {
			N string  `json:"n"`
			L float64 `json:"l"`
			O bool    `json:"o,omitempty"`
			D string  `json:"d"`
			G int     `json:"g"`
		}
		type demoNeighbors struct {
			F []demoNeighbor `json:"f,omitempty"`
			B []demoNeighbor `json:"b,omitempty"`
		}
		demoGraph := make([][2]any, 0, len(graph))
		nodeIDs := sortedKeys(graph)
		for _, nodeID := range nodeIDs {
			neighbors := graph[nodeID]
			dn := demoNeighbors{}
			for _, n := range neighbors.Forward {
				dn.F = append(dn.F, demoNeighbor{N: n.NodeID, L: n.Distance, O: n.IsOneLaneRoad, D: "f", G: n.DlcGuard})
			}
			for _, n := range neighbors.Backward {
				dn.B = append(dn.B, demoNeighbor{N: n.NodeID, L: n.Distance, O: n.IsOneLaneRoad, D: "b", G: n.DlcGuard})
			}
			demoGraph = append(demoGraph, [2]any{nodeID, dn})
		}

		usedNodeIDs := map[string]bool{}
		for _, entry := range demoGraph {
			nodeID := entry[0].(string)
			usedNodeIDs[nodeID] = true
			neighbors := entry[1].(demoNeighbors)
			for _, n := range neighbors.F {
				usedNodeIDs[n.N] = true
			}
			for _, n := range neighbors.B {
				usedNodeIDs[n.N] = true
			}
		}

		demoNodes := make([][2]any, 0, len(usedNodeIDs))
		for nodeID := range usedNodeIDs {
			if node, ok := nodeByUID[nodeID]; ok {
				demoNodes = append(demoNodes, [2]any{nodeID, []float64{node.X, node.Y}})
			}
		}
		sort.Slice(demoNodes, func(i, j int) bool {
			return demoNodes[i][0].(string) < demoNodes[j][0].(string)
		})

		type demoCompany struct {
			N string `json:"n"`
			T string `json:"t"`
			C string `json:"c"`
		}
		type demoCompanyDef struct {
			T string   `json:"t"`
			D []string `json:"d"`
		}
		demoCompanies := make([]demoCompany, 0, len(companies))
		for _, company := range companies {
			demoCompanies = append(demoCompanies, demoCompany{
				N: company.NodeUID.Key(),
				T: company.Token,
				C: company.CityToken,
			})
		}
		demoCompanyDefs := make([]demoCompanyDef, 0, len(companyDefs))
		for _, def := range companyDefs {
			demoCompanyDefs = append(demoCompanyDefs, demoCompanyDef{
				T: def.Token,
				D: def.CargoOutTokens,
			})
		}

		payload := map[string]any{
			"demoGraph":       demoGraph,
			"demoNodes":       demoNodes,
			"demoCompanies":   demoCompanies,
			"demoCompanyDefs": demoCompanyDefs,
		}
		return writeJSONFile(filepath.Join(outputDir, fmt.Sprintf("%s-graph-demo.json", prefix)), payload)
	}

	entries := make([][2]any, 0, len(graph))
	for _, nodeID := range sortedKeys(graph) {
		neighbors := graph[nodeID]
		entries = append(entries, [2]any{nodeID, neighbors})
	}
	return writeJSONFile(filepath.Join(outputDir, fmt.Sprintf("%s-graph.json", prefix)), entries)
}

func runSpritesheet(args []string) error {
	fs := flag.NewFlagSet("spritesheet", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var inputDir string
	var outputDir string

	fs.StringVar(&inputDir, "i", "", "input directory")
	fs.StringVar(&inputDir, "inputDir", "", "input directory")
	fs.StringVar(&outputDir, "o", "", "output directory")
	fs.StringVar(&outputDir, "outputDir", "", "output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	inputDir = untildify(inputDir)
	outputDir = untildify(outputDir)
	if err := requireDir(inputDir); err != nil {
		return err
	}
	if err := ensureOutputDir(outputDir, false); err != nil {
		return err
	}

	poiFiles, err := listPOIFiles(inputDir)
	if err != nil {
		return err
	}
	if len(poiFiles) == 0 {
		return errors.New("no -pois.json files found")
	}

	iconSet := map[string]bool{}
	for _, poiFile := range poiFiles {
		pois, err := readJSONArray[POI](poiFile)
		if err != nil {
			return err
		}
		for _, poi := range pois {
			if strings.TrimSpace(poi.Icon) != "" {
				iconSet[poi.Icon] = true
			}
		}
	}

	spriteImages := map[string]image.Image{}
	for _, builtin := range []string{"dot", "dotdot", "roadwork", "railcrossing"} {
		spriteImages[builtin] = generateBuiltinIcon(builtin)
	}

	missing := make([]string, 0)
	for icon := range iconSet {
		iconPath := filepath.Join(inputDir, "icons", icon+".png")
		f, err := os.Open(iconPath)
		if err != nil {
			missing = append(missing, iconPath)
			continue
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			missing = append(missing, iconPath)
			continue
		}
		spriteImages[icon] = img
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing png files: %s", strings.Join(missing, ", "))
	}

	names := make([]string, 0, len(spriteImages))
	for name := range spriteImages {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 0 {
		return errors.New("no sprites to pack")
	}
	maxW, maxH := 1, 1
	for _, name := range names {
		bounds := spriteImages[name].Bounds()
		if bounds.Dx() > maxW {
			maxW = bounds.Dx()
		}
		if bounds.Dy() > maxH {
			maxH = bounds.Dy()
		}
	}

	cols := int(math.Ceil(math.Sqrt(float64(len(names)))))
	if cols < 1 {
		cols = 1
	}
	rows := (len(names) + cols - 1) / cols
	canvas := image.NewRGBA(image.Rect(0, 0, cols*maxW, rows*maxH))

	coordinates := map[string]spriteLocation{}
	for idx, name := range names {
		row := idx / cols
		col := idx % cols
		x := col * maxW
		y := row * maxH
		img := spriteImages[name]
		b := img.Bounds()
		draw.Draw(canvas, image.Rect(x, y, x+b.Dx(), y+b.Dy()), img, b.Min, draw.Over)
		coordinates[name] = spriteLocation{
			X:          x,
			Y:          y,
			Width:      b.Dx(),
			Height:     b.Dy(),
			PixelRatio: 2,
		}
	}

	var spriteBuf bytes.Buffer
	if err := png.Encode(&spriteBuf, canvas); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "sprites.png"), spriteBuf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "sprites@2x.png"), spriteBuf.Bytes(), 0o644); err != nil {
		return err
	}

	coordsJSON, err := json.MarshalIndent(coordinates, "", "  ")
	if err != nil {
		return err
	}
	coordsJSON = append(coordsJSON, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, "sprites.json"), coordsJSON, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "sprites@2x.json"), coordsJSON, 0o644); err != nil {
		return err
	}

	return nil
}

func makeFocusSpec(mapName string, cities []City, focusCity, focusCoordsRaw string, radius float64) (*focusSpec, error) {
	if focusCity == "" && focusCoordsRaw == "" {
		return nil, nil
	}
	if radius <= 0 {
		return nil, errors.New("focusRadius must be greater than 0")
	}
	if focusCity != "" {
		for _, city := range cities {
			if strings.EqualFold(city.Name, focusCity) {
				return &focusSpec{kind: "city", city: focusCity, coords: cityCenter(city), radius: radius}, nil
			}
		}
		return nil, fmt.Errorf("unknown focus city %s", focusCity)
	}
	coords, err := parseCoords(focusCoordsRaw)
	if err != nil {
		return nil, err
	}
	_ = mapName
	return &focusSpec{kind: "coords", coords: coords, radius: radius}, nil
}

func withinFocus(focus *focusSpec, point point2, padding float64) bool {
	if focus == nil {
		return true
	}
	return distance(point, focus.coords) <= focus.radius+padding
}

func filterCities(cities []City, focus *focusSpec) []City {
	if focus == nil {
		return cities
	}
	out := make([]City, 0, len(cities))
	for _, city := range cities {
		if withinFocus(focus, point2{city.X, city.Y}, 0) {
			out = append(out, city)
		}
	}
	return out
}

func cityCenter(city City) point2 {
	for _, area := range city.Areas {
		if area.Hidden {
			continue
		}
		return point2{city.X + area.Width/2, city.Y + area.Height/2}
	}
	return point2{city.X, city.Y}
}

func polygonFromNodeUIDs(nodeUIDs []UID, nodes map[string]Node) []point2 {
	ring := make([]point2, 0, len(nodeUIDs)+1)
	for _, uid := range nodeUIDs {
		node, ok := nodes[uid.Key()]
		if !ok {
			continue
		}
		ring = append(ring, point2{node.X, node.Y})
	}
	if len(ring) >= 3 {
		ring = append(ring, ring[0])
	}
	return ring
}

func squareAround(center point2, size float64) []point2 {
	half := size / 2
	return []point2{
		{center[0] - half, center[1] - half},
		{center[0] + half, center[1] - half},
		{center[0] + half, center[1] + half},
		{center[0] - half, center[1] + half},
		{center[0] - half, center[1] - half},
	}
}

func toCoordinateArray(points []point2) []any {
	coords := make([]any, 0, len(points))
	for _, p := range points {
		coords = append(coords, []float64{p[0], p[1]})
	}
	return coords
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func inferRoadType(leftLanes, rightLanes int) string {
	total := leftLanes + rightLanes
	switch {
	case total == 0:
		return "unknown"
	case leftLanes == 0 || rightLanes == 0:
		if total >= 2 {
			return "divided"
		}
		return "local"
	case total >= 4:
		return "freeway"
	default:
		return "local"
	}
}

type tippecanoeConfig struct {
	MinZoom   int
	MaxZoom   int
	MinAttrs  []string
	LayerName string
	Buffer    int
	BaseZoom  int
}

func generateTiles(geojsonPath, outputDir, outputPrefix string, types []string, cfg tippecanoeConfig) error {
	for _, t := range types {
		if t != "pmtiles" && t != "mbtiles" {
			continue
		}
		tmpOutput := filepath.Join(os.TempDir(), fmt.Sprintf("%s.%s", outputPrefix, t))
		args := []string{
			fmt.Sprintf("-Z%d", cfg.MinZoom),
			fmt.Sprintf("-z%d", cfg.MaxZoom),
		}
		for _, attr := range cfg.MinAttrs {
			args = append(args, "-y", attr)
		}
		if cfg.LayerName != "" {
			args = append(args, "-l", cfg.LayerName)
		}
		if cfg.BaseZoom > 0 {
			args = append(args, "-B", fmt.Sprintf("%d", cfg.BaseZoom))
		}
		if cfg.Buffer > 0 {
			args = append(args, "-b", fmt.Sprintf("%d", cfg.Buffer))
		}
		args = append(args, "--force", "-o", tmpOutput, geojsonPath)

		cmd := exec.Command("tippecanoe", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return errors.New("tippecanoe is required for pmtiles/mbtiles output")
			}
			return fmt.Errorf("tippecanoe failed for %s: %v\n%s", t, err, string(out))
		}
		finalOutput := filepath.Join(outputDir, fmt.Sprintf("%s.%s", outputPrefix, t))
		if err := os.Rename(tmpOutput, finalOutput); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func strconvParseFloat(v string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(v), 64)
}

func generateBuiltinIcon(name string) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, draw.Src)

	switch name {
	case "dot":
		drawCircle(img, 16, 16, 11, color.RGBA{R: 0xf4, G: 0xef, B: 0xdf, A: 0xff})
		drawCircle(img, 16, 16, 12, color.RGBA{R: 0x22, G: 0x22, B: 0x22, A: 0x80})
	case "dotdot":
		drawCircle(img, 16, 16, 11, color.RGBA{R: 0xf4, G: 0xef, B: 0xdf, A: 0xff})
		drawCircle(img, 16, 16, 12, color.RGBA{R: 0x22, G: 0x22, B: 0x22, A: 0x80})
		drawCircle(img, 16, 16, 4, color.RGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff})
	case "roadwork":
		drawRect(img, 4, 4, 28, 28, color.RGBA{R: 0xf2, G: 0x8c, B: 0x28, A: 0xff})
		for i := 0; i < 24; i++ {
			img.Set(4+i, 27-i, color.Black)
		}
	case "railcrossing":
		drawRect(img, 13, 2, 19, 30, color.RGBA{R: 0xd1, G: 0x34, B: 0x38, A: 0xff})
		drawRect(img, 2, 13, 30, 19, color.RGBA{R: 0xd1, G: 0x34, B: 0x38, A: 0xff})
	default:
		drawRect(img, 6, 6, 26, 26, color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff})
	}
	return img
}

func drawRect(img *image.RGBA, minX, minY, maxX, maxY int, c color.Color) {
	draw.Draw(img, image.Rect(minX, minY, maxX, maxY), &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawCircle(img *image.RGBA, centerX, centerY, radius int, c color.Color) {
	r2 := radius * radius
	for y := centerY - radius; y <= centerY+radius; y++ {
		for x := centerX - radius; x <= centerX+radius; x++ {
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy <= r2 {
				if image.Pt(x, y).In(img.Bounds()) {
					img.Set(x, y, c)
				}
			}
		}
	}
}
