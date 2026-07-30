package parser

type IndexedMod struct {
	ArchivePath   string   `json:"archivePath"`
	FileStem      string   `json:"fileStem"`
	CanonicalName string   `json:"canonicalName"`
	DisplayName   string   `json:"displayName"`
	Dependencies  []string `json:"dependencies"`
	Incompatible  []string `json:"incompatible"`
	ManifestPath  string   `json:"manifestPath,omitempty"`
}

type ModConflictPolicy string

const (
	ModConflictWarn   ModConflictPolicy = "warn"
	ModConflictError  ModConflictPolicy = "error"
	ModConflictIgnore ModConflictPolicy = "ignore"
)

type ResolveModsOptions struct {
	ExplicitOrder      []string
	GameLogOrder       []string
	EnabledMods        []string
	DisabledMods       []string
	StrictDependencies bool
	ConflictPolicy     ModConflictPolicy
}

type ResolveModsResult struct {
	OrderedMods []IndexedMod
	Warnings    []string
}

type parseResult struct {
	MapName  string
	OnlyDefs bool
	DefData  map[string][]any
	MapData  map[string][]any
	Icons    map[string][]byte
}

var defDataKeys = []string{
	"countries",
	"companyDefs",
	"roadLooks",
	"prefabDescriptions",
	"modelDescriptions",
	"signDescriptions",
	"achievements",
	"routes",
	"mileageTargets",
}

var mapDataKeys = []string{
	"achievements",
	"cities",
	"companies",
	"companyDefs",
	"countries",
	"dividers",
	"ferries",
	"mapAreas",
	"mileageTargets",
	"modelDescriptions",
	"models",
	"nodes",
	"elevation",
	"pois",
	"prefabDescriptions",
	"prefabs",
	"roadLooks",
	"roads",
	"trajectories",
	"triggers",
	"cutscenes",
	"routes",
}
