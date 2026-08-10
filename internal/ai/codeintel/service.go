package codeintel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Thadopz/telltale/internal/authz"
)

var (
	ErrSourceUnavailable = errors.New("code source unavailable")
	ErrInvalidQuery      = errors.New("invalid code search query")
	ErrSensitiveQuery    = errors.New("code search query contains sensitive data")
	ErrBroadQuery        = errors.New("code search query is too broad")
	ErrHandleNotFound    = errors.New("code read handle not found")
	ErrHandleExpired     = errors.New("code read handle expired")
	ErrHandleDenied      = errors.New("code read handle is not valid for this context")
	ErrStaleHandle       = errors.New("code read handle refers to a stale source snapshot")
	ErrHandleLimit       = errors.New("code read handle limit reached")
)

type Service struct {
	snapshot *Snapshot
	config   Config

	handlesMu sync.Mutex
	handles   map[string]*ReadGrant
}

type ReadGrant struct {
	ID               string
	TenantID         string
	UserID           string
	ConversationID   string
	AgentScope       authz.AgentScope
	Repository       string
	Revision         string
	SnapshotDigest   string
	AllowedModes     []string
	RelativePath     string
	FileDigest       string
	AllowedStartLine int
	AllowedEndLine   int
	SymbolStartLine  int
	SymbolEndLine    int
	Symbol           string
	MatchKind        string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RemainingUses    int
	IndexGeneration  string
}

type searchCandidate struct {
	file        *IndexedFile
	startLine   int
	endLine     int
	symbol      string
	matchKind   string
	matchReason string
	score       int
}

func NewService(config Config) (*Service, error) {
	config = normalizeConfig(config)
	if !config.Enabled {
		return nil, ErrSourceUnavailable
	}
	root, err := resolveSourceRoot(config.Root)
	if err != nil {
		return nil, err
	}
	snapshot, err := buildSnapshot(config, root)
	if err != nil {
		return nil, err
	}
	return &Service{
		snapshot: snapshot,
		config:   config,
		handles:  make(map[string]*ReadGrant),
	}, nil
}

func (service *Service) Snapshot() Snapshot {
	if service == nil || service.snapshot == nil {
		return Snapshot{}
	}
	copySnapshot := *service.snapshot
	copySnapshot.Files = nil
	return copySnapshot
}

func (service *Service) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if service == nil || service.snapshot == nil {
		return SearchResult{}, ErrSourceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	query := strings.TrimSpace(request.Query)
	if utf8.RuneCountInString(query) == 0 || utf8.RuneCountInString(query) > 256 {
		return SearchResult{}, ErrInvalidQuery
	}
	if containsSensitiveQuery(query) {
		return SearchResult{}, ErrSensitiveQuery
	}
	if isBroadQuery(query) {
		return SearchResult{}, ErrBroadQuery
	}
	mode, err := normalizeSearchMode(request.Mode)
	if err != nil {
		return SearchResult{}, err
	}
	pathScope, err := normalizePathScope(request.PathScope)
	if err != nil {
		return SearchResult{}, err
	}
	limit := request.Limit
	if limit <= 0 || limit > service.config.MaxSearchResults {
		limit = service.config.MaxSearchResults
	}

	var candidates []searchCandidate
	paths := make([]string, 0, len(service.snapshot.Files))
	for path := range service.snapshot.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file := service.snapshot.Files[path]
		if !fileMatchesScope(file, pathScope) {
			continue
		}
		fileCandidates := findFileCandidates(file, query, mode)
		candidates = append(candidates, fileCandidates...)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].file.RelativePath != candidates[j].file.RelativePath {
			return candidates[i].file.RelativePath < candidates[j].file.RelativePath
		}
		if candidates[i].startLine != candidates[j].startLine {
			return candidates[i].startLine < candidates[j].startLine
		}
		return candidates[i].matchKind < candidates[j].matchKind
	})

	result := SearchResult{
		Repository:      service.snapshot.Repository,
		ServiceName:     service.snapshot.ServiceName,
		Revision:        service.snapshot.Revision,
		SnapshotDigest:  service.snapshot.SnapshotDigest,
		IndexGeneration: service.snapshot.IndexGeneration,
		VersionAlign:    service.snapshot.VersionAlign,
		AnchorID:        strings.TrimSpace(request.AnchorID),
		Hits:            make([]CodeHit, 0, minInt(limit, len(candidates))),
	}
	for _, candidate := range candidates {
		if len(result.Hits) >= limit {
			break
		}
		hit, err := service.makeHit(ctx, candidate)
		if err != nil {
			return SearchResult{}, err
		}
		result.Hits = append(result.Hits, hit)
	}
	return result, nil
}

func (service *Service) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	if service == nil || service.snapshot == nil {
		return ReadResult{}, ErrSourceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	handle := strings.TrimSpace(request.Handle)
	if handle == "" {
		return ReadResult{}, ErrHandleNotFound
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "match_context"
	}
	if mode != "match_context" && mode != "enclosing_symbol" {
		return ReadResult{}, fmt.Errorf("unsupported code read mode %q", mode)
	}
	now := time.Now()
	service.handlesMu.Lock()
	grant, ok := service.handles[handle]
	if !ok {
		service.handlesMu.Unlock()
		return ReadResult{}, ErrHandleNotFound
	}
	if now.After(grant.ExpiresAt) {
		delete(service.handles, handle)
		service.handlesMu.Unlock()
		return ReadResult{}, ErrHandleExpired
	}
	if grant.IndexGeneration != service.snapshot.IndexGeneration {
		delete(service.handles, handle)
		service.handlesMu.Unlock()
		return ReadResult{}, ErrStaleHandle
	}
	if grant.Repository != service.snapshot.Repository ||
		grant.Revision != service.snapshot.Revision ||
		grant.SnapshotDigest != service.snapshot.SnapshotDigest {
		delete(service.handles, handle)
		service.handlesMu.Unlock()
		return ReadResult{}, ErrStaleHandle
	}
	if !grantMatchesContext(ctx, grant) {
		service.handlesMu.Unlock()
		return ReadResult{}, ErrHandleDenied
	}
	if grant.RemainingUses <= 0 {
		delete(service.handles, handle)
		service.handlesMu.Unlock()
		return ReadResult{}, ErrHandleExpired
	}
	if !modeAllowed(mode, grant.AllowedModes) {
		service.handlesMu.Unlock()
		return ReadResult{}, ErrHandleDenied
	}
	grant.RemainingUses--
	service.handlesMu.Unlock()

	file, ok := service.snapshot.Files[grant.RelativePath]
	if !ok || file.OriginalDigest != grant.FileDigest {
		return ReadResult{}, ErrStaleHandle
	}
	startLine, endLine := grant.AllowedStartLine, grant.AllowedEndLine
	if mode == "enclosing_symbol" && grant.SymbolStartLine > 0 && grant.SymbolEndLine >= grant.SymbolStartLine {
		startLine, endLine = grant.SymbolStartLine, grant.SymbolEndLine
	}
	if endLine-startLine+1 > service.config.MaxReadLines {
		endLine = startLine + service.config.MaxReadLines - 1
	}
	if startLine < grant.AllowedStartLine {
		startLine = grant.AllowedStartLine
	}
	if endLine > grant.AllowedEndLine {
		endLine = grant.AllowedEndLine
	}
	content := formatLines(file, startLine, endLine)
	content = limitContent(content, service.config.MaxReadChars)
	return ReadResult{
		Repository:      service.snapshot.Repository,
		ServiceName:     service.snapshot.ServiceName,
		Revision:        service.snapshot.Revision,
		SnapshotDigest:  service.snapshot.SnapshotDigest,
		IndexGeneration: service.snapshot.IndexGeneration,
		VersionAlign:    service.snapshot.VersionAlign,
		Path:            grant.RelativePath,
		StartLine:       startLine,
		EndLine:         endLine,
		Symbol:          grant.Symbol,
		MatchKind:       grant.MatchKind,
		Content:         content,
		RuntimeVerified: false,
		ContentTrust:    "untrusted_repository_content",
	}, nil
}

func (service *Service) makeHit(ctx context.Context, candidate searchCandidate) (CodeHit, error) {
	handle, err := service.createGrant(ctx, candidate)
	if err != nil {
		return CodeHit{}, err
	}
	return CodeHit{
		Handle:          handle,
		Path:            candidate.file.RelativePath,
		StartLine:       candidate.startLine,
		EndLine:         candidate.endLine,
		Symbol:          candidate.symbol,
		MatchKind:       candidate.matchKind,
		MatchReason:     candidate.matchReason,
		Preview:         lineText(candidate.file, candidate.startLine),
		Generated:       candidate.file.Generated,
		Repository:      service.snapshot.Repository,
		Revision:        service.snapshot.Revision,
		FileDigest:      candidate.file.OriginalDigest,
		IndexGeneration: service.snapshot.IndexGeneration,
		VersionAlign:    service.snapshot.VersionAlign,
	}, nil
}

func (service *Service) createGrant(ctx context.Context, candidate searchCandidate) (string, error) {
	service.handlesMu.Lock()
	defer service.handlesMu.Unlock()
	service.removeExpiredHandles(time.Now())
	if len(service.handles) >= service.config.MaxHandles {
		return "", ErrHandleLimit
	}
	id, err := randomHandle()
	if err != nil {
		return "", err
	}
	startLine := candidate.startLine - 20
	if startLine < 1 {
		startLine = 1
	}
	endLine := candidate.endLine + 20
	lineCount := len(candidate.file.LineOffsets)
	if endLine > lineCount {
		endLine = lineCount
	}
	symbolStart, symbolEnd := symbolBounds(candidate.file, candidate.startLine, candidate.endLine)
	principal := authz.PrincipalFromContext(ctx)
	grant := &ReadGrant{
		ID:               id,
		TenantID:         principal.TenantID,
		UserID:           principal.UserID,
		ConversationID:   contextString(ctx, "client_id"),
		AgentScope:       authz.AgentScopeFromContext(ctx),
		Repository:       service.snapshot.Repository,
		Revision:         service.snapshot.Revision,
		SnapshotDigest:   service.snapshot.SnapshotDigest,
		AllowedModes:     []string{"match_context", "enclosing_symbol"},
		RelativePath:     candidate.file.RelativePath,
		FileDigest:       candidate.file.OriginalDigest,
		AllowedStartLine: startLine,
		AllowedEndLine:   endLine,
		SymbolStartLine:  symbolStart,
		SymbolEndLine:    symbolEnd,
		Symbol:           candidate.symbol,
		MatchKind:        candidate.matchKind,
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(service.config.HandleTTL),
		RemainingUses:    service.config.HandleUses,
		IndexGeneration:  service.snapshot.IndexGeneration,
	}
	service.handles[id] = grant
	return id, nil
}

func (service *Service) removeExpiredHandles(now time.Time) {
	for id, grant := range service.handles {
		if now.After(grant.ExpiresAt) || grant.RemainingUses <= 0 {
			delete(service.handles, id)
		}
	}
}

func buildSnapshot(config Config, root string) (*Snapshot, error) {
	files := make(map[string]*IndexedFile)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && !snapshotPathWithinRoot(root, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if !includedPath(relativePath, config.Include) || excludedPath(relativePath, config.Exclude) {
			return nil
		}
		if !supportedFile(relativePath) || isSensitiveFile(relativePath) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > int64(config.MaxFileBytes) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sanitized, ok := sanitizeSource(relativePath, raw)
		if !ok || len(sanitized) == 0 {
			return nil
		}
		indexed := &IndexedFile{
			RelativePath:   relativePath,
			Content:        append([]byte(nil), sanitized...),
			LineOffsets:    buildLineOffsets(sanitized),
			OriginalDigest: digestBytes(raw),
			ContentDigest:  digestBytes(sanitized),
			Generated:      generatedFile(relativePath, sanitized),
			ConfigFile:     configFile(relativePath),
		}
		if strings.EqualFold(filepath.Ext(relativePath), ".go") {
			indexed.AST, indexed.FileSet, indexed.Symbols, indexed.References = parseGoSymbols(relativePath, sanitized)
		}
		files[relativePath] = indexed
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build code snapshot: %w", err)
	}

	deployedRevision := ""
	if config.DeployedRevisionTrusted {
		deployedRevision = config.DeployedRevision
	}
	return &Snapshot{
		Repository:      config.Repository,
		ServiceName:     config.ServiceName,
		Revision:        config.Revision,
		SnapshotDigest:  snapshotDigest(files),
		IndexGeneration: randomGeneration(),
		VersionAlign:    versionAlignment(config.Revision, deployedRevision),
		Files:           files,
	}, nil
}

func snapshotPathWithinRoot(root string, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(relative)
}

func parseGoSymbols(path string, content []byte) (*ast.File, *token.FileSet, []SymbolRange, []ReferenceRange) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, content, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, nil
	}
	var symbols []SymbolRange
	var references []ReferenceRange
	ast.Inspect(file, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.FuncDecl:
			symbols = append(symbols, SymbolRange{
				Name:      current.Name.Name,
				Receiver:  receiverName(current.Recv),
				Kind:      "function",
				StartLine: fileSet.Position(current.Pos()).Line,
				EndLine:   fileSet.Position(current.End()).Line,
			})
		case *ast.TypeSpec:
			symbols = append(symbols, SymbolRange{
				Name:      current.Name.Name,
				Kind:      "type",
				StartLine: fileSet.Position(current.Pos()).Line,
				EndLine:   fileSet.Position(current.End()).Line,
			})
		case *ast.CallExpr:
			if name := expressionName(current.Fun); name != "" {
				references = append(references, ReferenceRange{
					Name:      name,
					StartLine: fileSet.Position(current.Pos()).Line,
					EndLine:   fileSet.Position(current.End()).Line,
				})
			}
		}
		return true
	})
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].StartLine != symbols[j].StartLine {
			return symbols[i].StartLine < symbols[j].StartLine
		}
		return symbols[i].EndLine < symbols[j].EndLine
	})
	sort.Slice(references, func(i, j int) bool {
		if references[i].StartLine != references[j].StartLine {
			return references[i].StartLine < references[j].StartLine
		}
		return references[i].Name < references[j].Name
	})
	return file, fileSet, symbols, references
}

func receiverName(fieldList *ast.FieldList) string {
	if fieldList == nil || len(fieldList.List) == 0 {
		return ""
	}
	return expressionName(fieldList.List[0].Type)
}

func expressionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return expressionName(value.X)
	case *ast.SelectorExpr:
		left := expressionName(value.X)
		if left == "" {
			return value.Sel.Name
		}
		return left + "." + value.Sel.Name
	default:
		return ""
	}
}

func findFileCandidates(file *IndexedFile, query string, mode SearchMode) []searchCandidate {
	if mode == SearchConfig && !file.ConfigFile {
		return nil
	}
	if mode == SearchSymbol {
		if !strings.EqualFold(filepath.Ext(file.RelativePath), ".go") {
			return nil
		}
		candidates := symbolCandidates(file, query)
		if len(candidates) > 0 || file.AST != nil {
			return candidates
		}
		return lexicalSymbolFallback(file, query)
	}
	if mode == SearchReference {
		if !strings.EqualFold(filepath.Ext(file.RelativePath), ".go") {
			return nil
		}
		return referenceCandidates(file, query)
	}
	lineMatches := findStringMatches(string(file.Content), query, mode == SearchExact)
	if len(lineMatches) == 0 && mode == SearchExact {
		lineMatches = findStringMatches(string(file.Content), query, false)
	}
	candidates := make([]searchCandidate, 0, len(lineMatches))
	for _, line := range lineMatches {
		kind := "exact_text"
		reason := "exact source text match"
		score := 70
		if mode == SearchExact && strings.HasPrefix(query, "/") && len(query) > 1 {
			kind = "static_route"
			reason = "static route text match"
			score = 85
		}
		if mode == SearchConfig {
			kind = "static_config"
			reason = "configuration text match"
			score = 85
		}
		if lineMatchesCaseSensitive(file.Content, query, line) {
			score += 20
		}
		if symbol := enclosingSymbol(file, line); symbol != nil {
			candidates = append(candidates, searchCandidate{
				file: file, startLine: line, endLine: line, symbol: symbol.Name,
				matchKind: kind, matchReason: reason, score: score,
			})
			continue
		}
		candidates = append(candidates, searchCandidate{
			file: file, startLine: line, endLine: line,
			matchKind: kind, matchReason: reason, score: score,
		})
	}
	return candidates
}

func referenceCandidates(file *IndexedFile, query string) []searchCandidate {
	query = strings.ToLower(strings.TrimSpace(query))
	var candidates []searchCandidate
	for _, reference := range file.References {
		if !referenceNameMatches(reference.Name, query) {
			continue
		}
		candidate := searchCandidate{
			file:        file,
			startLine:   reference.StartLine,
			endLine:     reference.EndLine,
			matchKind:   "static_code_call_site",
			matchReason: "Go AST call site; dynamic dispatch is unresolved",
			score:       95,
		}
		if symbol := enclosingSymbol(file, reference.StartLine); symbol != nil {
			candidate.symbol = symbol.Name
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) > 0 || file.AST != nil {
		return candidates
	}
	return lexicalReferenceFallback(file, query)
}

func referenceNameMatches(name string, query string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	query = strings.ToLower(strings.TrimSpace(query))
	return name == query || lastName(name) == query
}

func lastName(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index+1:]
	}
	return name
}

func lexicalReferenceFallback(file *IndexedFile, query string) []searchCandidate {
	lines := findStringMatches(string(file.Content), query, true)
	candidates := make([]searchCandidate, 0, len(lines))
	for _, line := range lines {
		candidate := searchCandidate{
			file:        file,
			startLine:   line,
			endLine:     line,
			matchKind:   "static_code_call_site_lexical_fallback",
			matchReason: "Go AST reference unavailable; lexical fallback; dynamic dispatch is unresolved",
			score:       65,
		}
		if symbol := enclosingSymbol(file, line); symbol != nil {
			candidate.symbol = symbol.Name
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func symbolCandidates(file *IndexedFile, query string) []searchCandidate {
	query = strings.ToLower(strings.TrimSpace(query))
	var candidates []searchCandidate
	for _, symbol := range file.Symbols {
		fullName := strings.ToLower(symbol.Name)
		if symbol.Receiver != "" {
			fullName = strings.ToLower(symbol.Receiver + "." + symbol.Name)
		}
		if fullName != query && strings.ToLower(symbol.Name) != query {
			continue
		}
		kind := "symbol_declaration"
		score := 100
		if file.Generated {
			score -= 25
		}
		candidates = append(candidates, searchCandidate{
			file: file, startLine: symbol.StartLine, endLine: symbol.EndLine,
			symbol: symbol.Name, matchKind: kind,
			matchReason: "exact Go AST symbol declaration", score: score,
		})
	}
	return candidates
}

func lexicalSymbolFallback(file *IndexedFile, query string) []searchCandidate {
	lines := findStringMatches(string(file.Content), query, true)
	candidates := make([]searchCandidate, 0, len(lines))
	for _, line := range lines {
		candidates = append(candidates, searchCandidate{
			file:        file,
			startLine:   line,
			endLine:     line,
			matchKind:   "symbol_text_fallback",
			matchReason: "Go AST symbol unavailable; lexical fallback",
			score:       65,
		})
	}
	return candidates
}

func enclosingSymbol(file *IndexedFile, startLine int) *SymbolRange {
	var selected *SymbolRange
	for index := range file.Symbols {
		symbol := &file.Symbols[index]
		if startLine < symbol.StartLine || startLine > symbol.EndLine {
			continue
		}
		if selected == nil || symbol.EndLine-symbol.StartLine < selected.EndLine-selected.StartLine {
			selected = symbol
		}
	}
	return selected
}

func symbolBounds(file *IndexedFile, startLine int, endLine int) (int, int) {
	symbol := enclosingSymbol(file, startLine)
	if symbol == nil {
		return 0, 0
	}
	return symbol.StartLine, symbol.EndLine
}

func findStringMatches(content string, query string, caseSensitive bool) []int {
	if !caseSensitive {
		content = strings.ToLower(content)
		query = strings.ToLower(query)
	}
	var lines []int
	start := 0
	for {
		index := strings.Index(content[start:], query)
		if index < 0 {
			break
		}
		absolute := start + index
		lines = append(lines, lineNumberAt(content, absolute))
		start = absolute + len(query)
		if start >= len(content) {
			break
		}
	}
	return uniqueInts(lines)
}

func lineMatchesCaseSensitive(content []byte, query string, line int) bool {
	text := lineTextFromBytes(content, line)
	return strings.Contains(text, query)
}

func formatLines(file *IndexedFile, startLine int, endLine int) string {
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(file.LineOffsets) {
		endLine = len(file.LineOffsets)
	}
	var builder strings.Builder
	for line := startLine; line <= endLine; line++ {
		builder.WriteString(fmt.Sprintf("%d: %s\n", line, lineText(file, line)))
	}
	return strings.TrimRight(builder.String(), "\n")
}

func limitContent(content string, maxChars int) string {
	if maxChars <= 0 || len([]rune(content)) <= maxChars {
		return content
	}
	runes := []rune(content)
	return string(runes[:maxChars]) + "\n[code_read output truncated]"
}

func lineText(file *IndexedFile, line int) string {
	return lineTextFromBytes(file.Content, line)
}

func lineTextFromBytes(content []byte, line int) string {
	if line < 1 {
		return ""
	}
	lines := strings.Split(string(content), "\n")
	if line > len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], "\r")
}

func buildLineOffsets(content []byte) []int {
	offsets := []int{0}
	for index, value := range content {
		if value == '\n' {
			offsets = append(offsets, index+1)
		}
	}
	if len(content) > 0 && content[len(content)-1] == '\n' {
		offsets = offsets[:len(offsets)-1]
	}
	return offsets
}

func lineNumberAt(content string, offset int) int {
	if offset <= 0 {
		return 1
	}
	return 1 + strings.Count(content[:offset], "\n")
}

func resolveSourceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", ErrSourceUnavailable
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve code source root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve code source root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", ErrSourceUnavailable
	}
	return resolved, nil
}

func normalizeConfig(config Config) Config {
	if strings.TrimSpace(config.Repository) == "" {
		config.Repository = RepositoryName
	}
	if strings.TrimSpace(config.ServiceName) == "" {
		config.ServiceName = "card-service"
	}
	if len(config.Include) == 0 {
		config.Include = defaultInclude()
	}
	if len(config.Exclude) == 0 {
		config.Exclude = defaultExclude()
	}
	if config.MaxFileBytes <= 0 {
		config.MaxFileBytes = 1024 * 1024
	}
	if config.MaxSearchResults <= 0 || config.MaxSearchResults > 8 {
		config.MaxSearchResults = 8
	}
	if config.MaxReadLines <= 0 || config.MaxReadLines > 160 {
		config.MaxReadLines = 160
	}
	if config.MaxReadChars <= 0 || config.MaxReadChars > 64000 {
		config.MaxReadChars = 24000
	}
	if config.MaxHandles <= 0 {
		config.MaxHandles = 64
	}
	if config.HandleTTL <= 0 {
		config.HandleTTL = 10 * time.Minute
	}
	if config.HandleUses <= 0 {
		config.HandleUses = 3
	}
	return config
}

func versionAlignment(revision string, deployedRevision string) string {
	revision = strings.TrimSpace(revision)
	deployedRevision = strings.TrimSpace(deployedRevision)
	if revision == "" || deployedRevision == "" {
		return "unknown"
	}
	if !validRevision(revision) || !validRevision(deployedRevision) {
		return "unverifiable"
	}
	if revision != deployedRevision {
		return "mismatch"
	}
	return "aligned"
}

func validRevision(revision string) bool {
	if revision == "" || len(revision) > 256 {
		return false
	}
	for _, character := range revision {
		if character == '\r' || character == '\n' || character == '\t' || character == ' ' || character < 0x20 {
			return false
		}
	}
	return true
}

func snapshotDigest(files map[string]*IndexedFile) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		hash.Write([]byte(path))
		hash.Write([]byte("\x00"))
		hash.Write([]byte(files[path].OriginalDigest))
		hash.Write([]byte("\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func randomGeneration() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return digestBytes([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))[:24]
}

func randomHandle() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "code_" + hex.EncodeToString(bytes), nil
}

func grantMatchesContext(ctx context.Context, grant *ReadGrant) bool {
	principal := authz.PrincipalFromContext(ctx)
	if grant.UserID != "" && grant.UserID != principal.UserID {
		return false
	}
	if grant.TenantID != "" && grant.TenantID != principal.TenantID {
		return false
	}
	if grant.ConversationID != "" && grant.ConversationID != contextString(ctx, "client_id") {
		return false
	}
	if grant.AgentScope != "" && grant.AgentScope != authz.AgentScopeFromContext(ctx) {
		return false
	}
	return true
}

func modeAllowed(mode string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), mode) {
			return true
		}
	}
	return len(allowed) == 0
}

func contextString(ctx context.Context, key string) string {
	value, _ := ctx.Value(key).(string)
	return strings.TrimSpace(value)
}

func normalizeSearchMode(mode SearchMode) (SearchMode, error) {
	mode = SearchMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		return SearchExact, nil
	}
	switch mode {
	case SearchExact, SearchSymbol, SearchReference, SearchConfig:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported code search mode %q", mode)
	}
}

func normalizePathScope(scope PathScope) (PathScope, error) {
	scope = PathScope(strings.ToLower(strings.TrimSpace(string(scope))))
	if scope == "" {
		return ScopeBusiness, nil
	}
	switch scope {
	case ScopeBusiness, ScopeAPI, ScopeConfig, ScopeMigration:
		return scope, nil
	default:
		return "", fmt.Errorf("unsupported code path scope %q", scope)
	}
}

func fileMatchesScope(file *IndexedFile, scope PathScope) bool {
	path := file.RelativePath
	switch scope {
	case ScopeAPI:
		return strings.HasPrefix(path, "api/")
	case ScopeConfig:
		return strings.HasPrefix(path, "manifest/config/") || file.ConfigFile
	case ScopeMigration:
		return strings.HasPrefix(path, "migrations/") || strings.HasSuffix(path, ".sql")
	default:
		return strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/")
	}
}

func includedPath(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if pathMatchesPattern(path, pattern) {
			return true
		}
	}
	return false
}

func excludedPath(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if pathMatchesPattern(path, pattern) {
			return true
		}
	}
	return false
}

func pathMatchesPattern(path string, pattern string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "**"))
	}
	if strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
		if matched, _ := pathpkg.Match(pattern, filepath.Base(path)); matched {
			return true
		}
		return strings.HasSuffix(path, "/"+pattern)
	}
	matched, _ := pathpkg.Match(pattern, path)
	return matched || path == pattern
}

func supportedFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".sql", ".yaml", ".yml", ".json", ".toml", ".proto", ".md":
		return true
	default:
		return false
	}
}

func configFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json", ".toml":
		return true
	default:
		return false
	}
}

func generatedFile(path string, content []byte) bool {
	name := strings.ToLower(filepath.Base(path))
	if strings.Contains(name, ".gen.") || strings.HasSuffix(name, "_generated.go") || strings.HasSuffix(name, ".pb.go") {
		return true
	}
	return strings.Contains(strings.ToLower(string(content)), "code generated")
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func uniqueInts(values []int) []int {
	seen := make(map[int]bool, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
