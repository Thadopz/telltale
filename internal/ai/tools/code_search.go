package tools

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/codeintel"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type CodeSearchInput struct {
	Query     string `json:"query" jsonschema:"description=Exact code anchor such as an error code, route, symbol, SQL digest, or configuration key"`
	Mode      string `json:"mode,omitempty" jsonschema:"description=Search mode: exact, symbol, reference, or config"`
	PathScope string `json:"path_scope,omitempty" jsonschema:"description=Allowed scope: business, api, config, or migration"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum results, limited by server policy"`
	AnchorID  string `json:"anchor_id,omitempty" jsonschema:"description=Optional runtime evidence anchor id produced by a previous payment tool"`
}

type CodeReadInput struct {
	Handle string `json:"handle" jsonschema:"description=Opaque handle returned by code_search"`
	Mode   string `json:"mode,omitempty" jsonschema:"description=Read mode: match_context or enclosing_symbol"`
}

func NewCodeSearchTool(service *codeintel.Service) (tool.InvokableTool, error) {
	if service == nil {
		return nil, fmt.Errorf("code search service is nil")
	}
	return utils.InferOptionableTool(
		"code_search",
		"Search the fixed, sanitized card_service source snapshot using exact anchors. This returns bounded handles, not whole files.",
		func(ctx context.Context, input *CodeSearchInput, _ ...tool.Option) (string, error) {
			if input == nil {
				return marshalToolError(codeintel.ErrInvalidQuery, "code search input is required"), nil
			}
			if err := validateAnchor(ctx, input.AnchorID, input.Query); err != nil {
				return marshalToolError(err, "code search anchor validation failed"), nil
			}
			result, err := service.Search(ctx, codeintel.SearchRequest{
				Query:     input.Query,
				Mode:      codeintel.SearchMode(input.Mode),
				PathScope: codeintel.PathScope(input.PathScope),
				Limit:     input.Limit,
				AnchorID:  strings.TrimSpace(input.AnchorID),
			})
			if err != nil {
				return marshalToolError(err, "code search failed"), nil
			}
			env := evidence.NewDataWithObservations(result, fmt.Sprintf("code search returned %d bounded hits", len(result.Hits)), codeSearchObservations(result))
			if len(result.Hits) == 0 {
				env.DataStatus = evidence.DataEmpty
			}
			env.Evidence.Provenance = evidence.Provenance{
				SourceKind:       codeSourceKind(result, input.PathScope),
				Repository:       result.Repository,
				Revision:         result.Revision,
				SnapshotDigest:   result.SnapshotDigest,
				VersionAlignment: evidence.VersionAlignment(result.VersionAlign),
				PayloadTrust:     evidence.ContentTrustUntrustedRepository,
			}
			return evidence.Marshal(env), nil
		})
}

func NewCodeReadTool(service *codeintel.Service) (tool.InvokableTool, error) {
	if service == nil {
		return nil, fmt.Errorf("code read service is nil")
	}
	return utils.InferOptionableTool(
		"code_read",
		"Read a bounded sanitized code snippet from a handle returned by code_search. Never accepts arbitrary paths.",
		func(ctx context.Context, input *CodeReadInput, _ ...tool.Option) (string, error) {
			if input == nil {
				return marshalToolError(codeintel.ErrHandleNotFound, "code read input is required"), nil
			}
			result, err := service.Read(ctx, codeintel.ReadRequest{Handle: input.Handle, Mode: input.Mode})
			if err != nil {
				return marshalToolError(err, "code read failed"), nil
			}
			kind := evidence.SourceStaticCode
			observationKind := evidence.ObservationStaticCodeDeclaration
			if isConfigPath(result.Path) {
				kind = evidence.SourceStaticConfig
				observationKind = evidence.ObservationStaticConfig
			} else if result.MatchKind == "static_route" {
				observationKind = evidence.ObservationStaticRoute
			} else if strings.HasSuffix(strings.ToLower(filepath.ToSlash(result.Path)), ".sql") {
				observationKind = evidence.ObservationStaticSQL
			}
			env := evidence.NewDataWithObservations(result, fmt.Sprintf("read sanitized code %s:%d-%d", result.Path, result.StartLine, result.EndLine), []evidence.Observation{{
				Kind:    observationKind,
				Subject: evidence.SubjectRef{Type: "code", Service: result.ServiceName},
				Scope: evidence.EvidenceScope{Fields: map[string]string{
					"repository":        result.Repository,
					"path":              result.Path,
					"start_line":        fmt.Sprintf("%d", result.StartLine),
					"end_line":          fmt.Sprintf("%d", result.EndLine),
					"symbol":            result.Symbol,
					"revision":          result.Revision,
					"version_alignment": result.VersionAlign,
				}},
				Fields: map[string]any{
					"path":             result.Path,
					"symbol":           result.Symbol,
					"match_kind":       result.MatchKind,
					"runtime_verified": false,
					"content_trust":    result.ContentTrust,
				},
				Summary: "static source snippet; runtime execution is not verified",
			}})
			env.Evidence.Provenance = evidence.Provenance{
				SourceKind:       kind,
				Repository:       result.Repository,
				Revision:         result.Revision,
				SnapshotDigest:   result.SnapshotDigest,
				VersionAlignment: evidence.VersionAlignment(result.VersionAlign),
				PayloadTrust:     evidence.ContentTrustUntrustedRepository,
			}
			return evidence.Marshal(env), nil
		})
}

func validateAnchor(ctx context.Context, anchorID string, query string) error {
	anchorID = strings.TrimSpace(anchorID)
	if anchorID == "" {
		return nil
	}
	store := evidence.AnchorStoreFromContext(ctx)
	if store == nil {
		return fmt.Errorf("runtime anchor store is unavailable")
	}
	anchor, ok := store.Get(anchorID)
	if !ok {
		return fmt.Errorf("runtime anchor %q was not found", anchorID)
	}
	if !strings.Contains(strings.ToLower(query), strings.ToLower(anchor.Value)) {
		return fmt.Errorf("code query does not contain the selected runtime anchor")
	}
	return nil
}

func codeSearchObservations(result codeintel.SearchResult) []evidence.Observation {
	if len(result.Hits) == 0 {
		return []evidence.Observation{{
			Kind:    evidence.ObservationEmptyResult,
			Subject: evidence.SubjectRef{Type: "code", Service: result.ServiceName},
			Fields: map[string]any{
				"repository":        result.Repository,
				"version_alignment": result.VersionAlign,
			},
			Summary: "no code hit in the exact configured repository scope",
		}}
	}
	observations := make([]evidence.Observation, 0, len(result.Hits))
	for _, hit := range result.Hits {
		kind := evidence.ObservationStaticCodeDeclaration
		if strings.HasPrefix(hit.MatchKind, "static_code_call_site") {
			kind = evidence.ObservationStaticCodeCallSite
		} else if hit.MatchKind == "static_route" {
			kind = evidence.ObservationStaticRoute
		}
		if isConfigPath(hit.Path) {
			kind = evidence.ObservationStaticConfig
		} else if strings.HasSuffix(strings.ToLower(filepath.ToSlash(hit.Path)), ".sql") {
			kind = evidence.ObservationStaticSQL
		}
		observations = append(observations, evidence.Observation{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "code", Service: result.ServiceName},
			Scope: evidence.EvidenceScope{Fields: map[string]string{
				"repository":        hit.Repository,
				"path":              hit.Path,
				"start_line":        fmt.Sprintf("%d", hit.StartLine),
				"end_line":          fmt.Sprintf("%d", hit.EndLine),
				"revision":          hit.Revision,
				"version_alignment": hit.VersionAlign,
			}},
			Fields: map[string]any{
				"symbol":           hit.Symbol,
				"match_kind":       hit.MatchKind,
				"generated":        hit.Generated,
				"runtime_verified": false,
			},
			Summary: hit.MatchReason,
		})
	}
	return observations
}

func codeSourceKind(result codeintel.SearchResult, pathScope string) evidence.EvidenceSourceKind {
	if strings.EqualFold(strings.TrimSpace(pathScope), string(codeintel.ScopeConfig)) {
		return evidence.SourceStaticConfig
	}
	for _, hit := range result.Hits {
		if isConfigPath(hit.Path) {
			return evidence.SourceStaticConfig
		}
	}
	return evidence.SourceStaticCode
}

func isConfigPath(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	return strings.HasPrefix(path, "manifest/config/") || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".toml")
}
