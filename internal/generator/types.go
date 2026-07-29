package generator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type UID string

func (u UID) Key() string {
	return normalizeUID(string(u))
}

func (u UID) String() string {
	return string(u)
}

func (u *UID) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == "" {
		*u = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*u = UID(normalizeUID(s))
		return nil
	}

	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		*u = UID(normalizeUID(num.String()))
		return nil
	}

	return fmt.Errorf("unsupported uid value: %s", string(data))
}

func normalizeUID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimPrefix(v, "0x")
	if v == "" {
		return ""
	}
	if onlyDigits(v) {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return strconv.FormatInt(i, 10)
		}
	}
	return v
}

func onlyDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type Node struct {
	UID      UID     `json:"uid"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
}

type CityArea struct {
	Hidden bool    `json:"hidden"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type City struct {
	Token        string     `json:"token"`
	Name         string     `json:"name"`
	CountryToken string     `json:"countryToken"`
	X            float64    `json:"x"`
	Y            float64    `json:"y"`
	Areas        []CityArea `json:"areas"`
}

type Country struct {
	Token string  `json:"token"`
	Name  string  `json:"name"`
	Code  string  `json:"code"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type Road struct {
	UID          UID     `json:"uid"`
	StartNodeUID UID     `json:"startNodeUid"`
	EndNodeUID   UID     `json:"endNodeUid"`
	RoadLook     string  `json:"roadLookToken"`
	DlcGuard     int     `json:"dlcGuard"`
	Hidden       bool    `json:"hidden"`
	Length       float64 `json:"length"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}

type FerryConnection struct {
	Token string  `json:"token"`
	Name  string  `json:"name"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type Ferry struct {
	Token       string            `json:"token"`
	Train       bool              `json:"train"`
	Name        string            `json:"name"`
	X           float64           `json:"x"`
	Y           float64           `json:"y"`
	Connections []FerryConnection `json:"connections"`
}

type Prefab struct {
	UID             UID     `json:"uid"`
	Token           string  `json:"token"`
	NodeUIDs        []UID   `json:"nodeUids"`
	OriginNodeIndex int     `json:"originNodeIndex"`
	X               float64 `json:"x"`
	Y               float64 `json:"y"`
	DlcGuard        int     `json:"dlcGuard"`
	Hidden          bool    `json:"hidden"`
}

type MapArea struct {
	UID      UID     `json:"uid"`
	NodeUIDs []UID   `json:"nodeUids"`
	Color    any     `json:"color"`
	DlcGuard int     `json:"dlcGuard"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

type POI struct {
	Type     string  `json:"type"`
	Icon     string  `json:"icon"`
	Label    string  `json:"label"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	DlcGuard int     `json:"dlcGuard"`
}

type RoadLook struct {
	Token      string `json:"token"`
	LanesLeft  []any  `json:"lanesLeft"`
	LanesRight []any  `json:"lanesRight"`
}

type PrefabDescriptionNode struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
}

type PrefabCurveEndpoint struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
}

type PrefabNavCurve struct {
	Start PrefabCurveEndpoint `json:"start"`
	End   PrefabCurveEndpoint `json:"end"`
}

type PrefabNavConnection struct {
	CurveIndices []int `json:"curveIndices"`
}

type PrefabNavNode struct {
	Connections []PrefabNavConnection `json:"connections"`
}

type PrefabDescription struct {
	Token     string                  `json:"token"`
	Nodes     []PrefabDescriptionNode `json:"nodes"`
	NavCurves []PrefabNavCurve        `json:"navCurves"`
	NavNodes  []PrefabNavNode         `json:"navNodes"`
}

type ModelScale struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type Model struct {
	UID     UID        `json:"uid"`
	Token   string     `json:"token"`
	NodeUID UID        `json:"nodeUid"`
	Scale   ModelScale `json:"scale"`
}

type ModelDescriptionPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type ModelDescription struct {
	Token  string                `json:"token"`
	Center ModelDescriptionPoint `json:"center"`
	Start  ModelDescriptionPoint `json:"start"`
	End    ModelDescriptionPoint `json:"end"`
	Height float64               `json:"height"`
}

type CompanyItem struct {
	UID       UID     `json:"uid"`
	Token     string  `json:"token"`
	CityToken string  `json:"cityToken"`
	NodeUID   UID     `json:"nodeUid"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

type CompanyDef struct {
	Token          string   `json:"token"`
	Name           string   `json:"name"`
	CityTokens     []string `json:"cityTokens"`
	CargoOutTokens []string `json:"cargoOutTokens"`
}

type RawAchievement map[string]any

type FeatureCollection struct {
	Type     string    `json:"type"`
	Features []Feature `json:"features"`
}

type Feature struct {
	Type       string         `json:"type"`
	ID         string         `json:"id,omitempty"`
	Geometry   Geometry       `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type Geometry struct {
	Type        string `json:"type"`
	Coordinates any    `json:"coordinates"`
}

type spriteLocation struct {
	X          int `json:"x"`
	Y          int `json:"y"`
	Width      int `json:"width"`
	Height     int `json:"height"`
	PixelRatio int `json:"pixelRatio"`
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*s = append(*s, value)
	return nil
}

func countTruthy(b bool) int {
	if b {
		return 1
	}
	return 0
}
