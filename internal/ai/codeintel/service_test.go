package codeintel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/authz"
)

func TestServiceBuildsSanitizedImmutableSnapshotAndBoundedHandles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/pool.go", `package pool

func OpenPool() {
	SetMaxOpenConns(20)
	dsn := "pgsql:card:card@tcp(db.internal:5432)/cards"
	_ = dsn
}
`)
	writeTestFile(t, root, "internal/bad.go", "package pool\n\nfunc Broken(\n")
	writeTestFile(t, root, "manifest/config/database.yaml", `database:
  user: card
  password: super-secret
  dsn: pgsql:card:card@tcp(db.internal:5432)/cards
  maxOpen: 20
`)
	writeTestFile(t, root, ".env", "PASSWORD=super-secret\n")

	service, err := NewService(testCodeConfig(root))
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	if service.Snapshot().SnapshotDigest == "" || service.Snapshot().IndexGeneration == "" {
		t.Fatalf("expected snapshot metadata, got %#v", service.Snapshot())
	}

	ctx := codeReaderContext()
	search, err := service.Search(ctx, SearchRequest{Query: "OpenPool", Mode: SearchSymbol, PathScope: ScopeBusiness, Limit: 4})
	if err != nil {
		t.Fatalf("search symbol: %v", err)
	}
	if len(search.Hits) != 1 || search.Hits[0].MatchKind != "symbol_declaration" {
		t.Fatalf("expected one AST symbol hit, got %#v", search.Hits)
	}
	handle := search.Hits[0].Handle
	if strings.Contains(marshalSearch(search), "super-secret") {
		t.Fatal("search response leaked a source secret")
	}

	read, err := service.Read(ctx, ReadRequest{Handle: handle, Mode: "enclosing_symbol"})
	if err != nil {
		t.Fatalf("read bounded symbol: %v", err)
	}
	if read.RuntimeVerified || read.ContentTrust != "untrusted_repository_content" {
		t.Fatalf("expected unverified untrusted static content, got %#v", read)
	}
	if !strings.Contains(read.Content, "SetMaxOpenConns(20)") {
		t.Fatalf("expected bounded function content, got %s", read.Content)
	}
	if strings.Contains(read.Content, "card:card") || strings.Contains(read.Content, "super-secret") {
		t.Fatalf("read response leaked credentials: %s", read.Content)
	}

	writeTestFile(t, root, "internal/pool.go", "package pool\n\nfunc OpenPool() { SetMaxOpenConns(999) }\n")
	unchanged, err := service.Read(ctx, ReadRequest{Handle: handle, Mode: "match_context"})
	if err != nil {
		t.Fatalf("read immutable snapshot after disk change: %v", err)
	}
	if !strings.Contains(unchanged.Content, "SetMaxOpenConns(20)") || strings.Contains(unchanged.Content, "999") {
		t.Fatalf("expected original snapshot content, got %s", unchanged.Content)
	}
}

func TestServiceRejectsSensitiveAndBroadQueriesWithoutHitSideChannel(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/pool.go", "package pool\nconst poolSize = 20\n")
	service, err := NewService(testCodeConfig(root))
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	ctx := codeReaderContext()
	if _, err = service.Search(ctx, SearchRequest{Query: "password=super-secret"}); !errors.Is(err, ErrSensitiveQuery) {
		t.Fatalf("expected sensitive query denial, got %v", err)
	}
	if _, err = service.Search(ctx, SearchRequest{Query: "payment"}); !errors.Is(err, ErrBroadQuery) {
		t.Fatalf("expected broad query denial, got %v", err)
	}
	result, err := service.Search(ctx, SearchRequest{Query: "super-secret", Mode: SearchExact})
	if err != nil {
		t.Fatalf("search redacted secret token: %v", err)
	}
	if len(result.Hits) != 0 {
		t.Fatalf("expected no hit for excluded/redacted secret, got %#v", result.Hits)
	}
}

func TestServiceUsesLexicalFallbackWhenGoASTCannotParse(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/bad.go", "package broken\n\nfunc Broken(\n")
	service, err := NewService(testCodeConfig(root))
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	result, err := service.Search(codeReaderContext(), SearchRequest{Query: "Broken", Mode: SearchSymbol, PathScope: ScopeBusiness})
	if err != nil {
		t.Fatalf("search malformed Go source: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].MatchKind != "symbol_text_fallback" {
		t.Fatalf("expected explicit lexical fallback, got %#v", result.Hits)
	}
	if !strings.Contains(result.Hits[0].MatchReason, "AST symbol unavailable") {
		t.Fatalf("expected AST limitation in match reason, got %q", result.Hits[0].MatchReason)
	}
	referenceResult, err := service.Search(codeReaderContext(), SearchRequest{Query: "Broken", Mode: SearchReference, PathScope: ScopeBusiness})
	if err != nil {
		t.Fatalf("search malformed Go reference: %v", err)
	}
	if len(referenceResult.Hits) != 1 || referenceResult.Hits[0].MatchKind != "static_code_call_site_lexical_fallback" {
		t.Fatalf("expected explicit reference fallback, got %#v", referenceResult.Hits)
	}
}

func TestServiceReferenceSearchUsesGoASTCallSites(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/pool.go", `package pool

func OpenPool() {
	SetMaxOpenConns(20)
}
`)
	service, err := NewService(testCodeConfig(root))
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	result, err := service.Search(codeReaderContext(), SearchRequest{Query: "SetMaxOpenConns", Mode: SearchReference, PathScope: ScopeBusiness})
	if err != nil {
		t.Fatalf("search AST call site: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].MatchKind != "static_code_call_site" {
		t.Fatalf("expected AST call-site hit, got %#v", result.Hits)
	}
	if !strings.Contains(result.Hits[0].MatchReason, "Go AST call site") {
		t.Fatalf("expected static analysis limitation in reason, got %q", result.Hits[0].MatchReason)
	}
}

func TestVersionAlignmentRejectsMalformedRevision(t *testing.T) {
	if got := versionAlignment("rev-a", "rev-a"); got != "aligned" {
		t.Fatalf("expected aligned revision, got %q", got)
	}
	if got := versionAlignment("rev-a", "rev-b"); got != "mismatch" {
		t.Fatalf("expected mismatched revision, got %q", got)
	}
	if got := versionAlignment("rev a", "rev a"); got != "unverifiable" {
		t.Fatalf("expected malformed revision to be unverifiable, got %q", got)
	}
	if got := versionAlignment("", "rev-a"); got != "unknown" {
		t.Fatalf("expected missing revision to be unknown, got %q", got)
	}
}

func TestServiceRequiresTrustedDeploymentManifestForAlignment(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/pool.go", "package pool\n\nfunc OpenPool() {}\n")

	withoutManifest := testCodeConfig(root)
	service, err := NewService(withoutManifest)
	if err != nil {
		t.Fatalf("build source snapshot without manifest: %v", err)
	}
	if service.Snapshot().VersionAlign != "unknown" {
		t.Fatalf("untrusted configured deployment revision must remain unknown, got %q", service.Snapshot().VersionAlign)
	}

	manifestPath := filepath.Join(root, "deployment-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"revision":"rev-a","image_digest":"sha256:test"}`), 0o600); err != nil {
		t.Fatalf("write deployment manifest: %v", err)
	}
	withManifest := withoutManifest
	withManifest.DeploymentManifestPath = manifestPath
	withManifest.DeployedRevisionTrusted = true
	withManifest.DeployedRevision = "rev-a"
	service, err = NewService(withManifest)
	if err != nil {
		t.Fatalf("build source snapshot with manifest: %v", err)
	}
	if service.Snapshot().VersionAlign != "aligned" {
		t.Fatalf("trusted matching deployment revision must align, got %q", service.Snapshot().VersionAlign)
	}

	withManifest.DeployedRevision = "rev-b"
	service, err = NewService(withManifest)
	if err != nil {
		t.Fatalf("build mismatched source snapshot: %v", err)
	}
	if service.Snapshot().VersionAlign != "mismatch" {
		t.Fatalf("trusted mismatched deployment revision must be mismatch, got %q", service.Snapshot().VersionAlign)
	}
}

func TestServiceBindsHandleToPrincipalSessionAndAgentScope(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/pool.go", "package pool\n\nfunc OpenPool() {}\n")
	config := testCodeConfig(root)
	config.HandleUses = 1
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	ctx := codeReaderContext()
	result, err := service.Search(ctx, SearchRequest{Query: "OpenPool", Mode: SearchSymbol})
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("search handle: result=%#v err=%v", result, err)
	}
	handle := result.Hits[0].Handle

	wrongAgent := authz.WithAgentScope(ctx, authz.AgentScopeDatabase)
	if _, err = service.Read(wrongAgent, ReadRequest{Handle: handle}); !errors.Is(err, ErrHandleDenied) {
		t.Fatalf("expected wrong agent to be denied, got %v", err)
	}
	wrongSession := context.WithValue(ctx, "client_id", "other-conversation")
	if _, err = service.Read(wrongSession, ReadRequest{Handle: handle}); !errors.Is(err, ErrHandleDenied) {
		t.Fatalf("expected wrong session to be denied, got %v", err)
	}
	if _, err = service.Read(ctx, ReadRequest{Handle: handle}); err != nil {
		t.Fatalf("expected bound context to read handle, got %v", err)
	}
	if _, err = service.Read(ctx, ReadRequest{Handle: handle}); !errors.Is(err, ErrHandleExpired) {
		t.Fatalf("expected one-use handle to expire, got %v", err)
	}
}

func TestServiceExcludesSensitiveFilesAndSymlinkedFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, outside, "outside.go", "package outside\nconst Secret = \"outside\"\n")
	writeTestFile(t, root, ".env.production", "TOKEN=super-secret\n")
	writeTestFile(t, root, "internal/credentials.go", "package credentials\nconst Secret = \"secret\"\n")
	link := filepath.Join(root, "internal", "linked.go")
	if err := os.Symlink(filepath.Join(outside, "outside.go"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	service, err := NewService(testCodeConfig(root))
	if err != nil {
		t.Fatalf("build code snapshot: %v", err)
	}
	for path := range service.snapshot.Files {
		if strings.Contains(path, "credential") || strings.Contains(path, "linked") || strings.Contains(path, ".env") {
			t.Fatalf("sensitive or symlinked path entered snapshot: %s", path)
		}
	}
}

func testCodeConfig(root string) Config {
	return Config{
		Enabled:          true,
		Repository:       RepositoryName,
		ServiceName:      "card-service",
		Root:             root,
		Revision:         "rev-a",
		DeployedRevision: "rev-a",
		Include:          []string{"internal/**", "manifest/config/**"},
		Exclude:          []string{".env*", "**/*.key", "**/*.pem", "**/*.crt"},
		MaxFileBytes:     1024 * 1024,
		MaxSearchResults: 8,
		MaxReadLines:     80,
		MaxHandles:       32,
		HandleTTL:        time.Minute,
		HandleUses:       3,
	}
}

func codeReaderContext() context.Context {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{
		UserID:   "reader",
		TenantID: "tenant-a",
		Roles:    []string{"code_reader"},
	})
	ctx = authz.WithAgentScope(ctx, authz.AgentScopePayment)
	return context.WithValue(ctx, "client_id", "conversation-a")
}

func writeTestFile(t *testing.T, root string, relativePath string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file %s: %v", relativePath, err)
	}
}

func marshalSearch(result SearchResult) string {
	data, _ := json.Marshal(result)
	return string(data)
}
