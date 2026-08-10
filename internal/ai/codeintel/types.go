package codeintel

import (
	"go/ast"
	"go/token"
	"time"
)

const RepositoryName = "card_service"

type SearchMode string

const (
	SearchExact     SearchMode = "exact"
	SearchSymbol    SearchMode = "symbol"
	SearchReference SearchMode = "reference"
	SearchConfig    SearchMode = "config"
)

type PathScope string

const (
	ScopeBusiness  PathScope = "business"
	ScopeAPI       PathScope = "api"
	ScopeConfig    PathScope = "config"
	ScopeMigration PathScope = "migration"
)

type Config struct {
	Enabled                 bool
	Repository              string
	ServiceName             string
	Root                    string
	Revision                string
	DeployedRevision        string
	DeployedRevisionTrusted bool
	DeploymentManifestPath  string
	DeploymentImageDigest   string
	Include                 []string
	Exclude                 []string

	MaxFileBytes     int
	MaxSearchResults int
	MaxReadLines     int
	MaxReadChars     int
	MaxHandles       int
	HandleTTL        time.Duration
	HandleUses       int
}

type SearchRequest struct {
	Query     string
	Mode      SearchMode
	PathScope PathScope
	Limit     int
	AnchorID  string
}

type CodeHit struct {
	Handle          string `json:"handle"`
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	Symbol          string `json:"symbol,omitempty"`
	MatchKind       string `json:"match_kind"`
	MatchReason     string `json:"match_reason"`
	Preview         string `json:"preview,omitempty"`
	Generated       bool   `json:"generated"`
	Repository      string `json:"repository"`
	Revision        string `json:"revision,omitempty"`
	FileDigest      string `json:"file_digest"`
	IndexGeneration string `json:"index_generation"`
	VersionAlign    string `json:"version_alignment"`
}

type SearchResult struct {
	Repository      string    `json:"repository"`
	ServiceName     string    `json:"service_name"`
	Revision        string    `json:"revision,omitempty"`
	SnapshotDigest  string    `json:"snapshot_digest"`
	IndexGeneration string    `json:"index_generation"`
	VersionAlign    string    `json:"version_alignment"`
	AnchorID        string    `json:"anchor_id,omitempty"`
	Hits            []CodeHit `json:"hits"`
}

type ReadRequest struct {
	Handle string
	Mode   string
}

type ReadResult struct {
	Repository      string `json:"repository"`
	ServiceName     string `json:"service_name"`
	Revision        string `json:"revision,omitempty"`
	SnapshotDigest  string `json:"snapshot_digest"`
	IndexGeneration string `json:"index_generation"`
	VersionAlign    string `json:"version_alignment"`
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	Symbol          string `json:"symbol,omitempty"`
	MatchKind       string `json:"match_kind"`
	Content         string `json:"content"`
	RuntimeVerified bool   `json:"runtime_verified"`
	ContentTrust    string `json:"content_trust"`
}

type SymbolRange struct {
	Name      string
	Receiver  string
	Kind      string
	StartLine int
	EndLine   int
}

type ReferenceRange struct {
	Name      string
	StartLine int
	EndLine   int
}

type IndexedFile struct {
	RelativePath   string
	Content        []byte
	LineOffsets    []int
	AST            *ast.File
	FileSet        *token.FileSet
	Symbols        []SymbolRange
	References     []ReferenceRange
	OriginalDigest string
	ContentDigest  string
	Generated      bool
	ConfigFile     bool
}

type Snapshot struct {
	Repository      string
	ServiceName     string
	Revision        string
	SnapshotDigest  string
	IndexGeneration string
	VersionAlign    string
	Files           map[string]*IndexedFile
}
