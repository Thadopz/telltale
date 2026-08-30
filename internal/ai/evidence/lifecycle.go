package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type runIDContextKey struct{}

func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, strings.TrimSpace(runID))
}

func RunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(runIDContextKey{}).(string)
	return strings.TrimSpace(runID)
}

func NewRunID() string {
	return newID("run")
}

type IssueMetadata struct {
	Issuer         string
	RunID          string
	ConversationID string
	TenantID       string
	TrustLevel     string
	ExpiresAfter   time.Duration
}

// IssueEnvelope gives a successful structured result a local identity. The
// result may describe its source, but it cannot choose the local evidence ID.
func IssueEnvelope(env Envelope, metadata IssueMetadata) Envelope {
	record, ok := env.UsableEvidence()
	if !ok {
		return env
	}

	record.EvidenceID = newID("ev")
	record.Attestation.Issuer = strings.TrimSpace(metadata.Issuer)
	record.Attestation.RunID = strings.TrimSpace(metadata.RunID)
	record.Attestation.ConversationID = strings.TrimSpace(metadata.ConversationID)
	record.Attestation.TenantID = strings.TrimSpace(metadata.TenantID)
	if record.Attestation.ObservedAt.IsZero() {
		record.Attestation.ObservedAt = time.Now().UTC()
	}
	if metadata.ExpiresAfter > 0 {
		expiresAt := record.Attestation.ObservedAt.Add(metadata.ExpiresAfter)
		record.Attestation.ExpiresAt = &expiresAt
	}
	if strings.TrimSpace(metadata.TrustLevel) != "" {
		record.Attestation.IssuerTrust = strings.TrimSpace(metadata.TrustLevel)
	}
	if record.Attestation.IssuerTrust == "" {
		record.Attestation.IssuerTrust = ContentTrustTrustedLocal
	}
	if record.Attestation.IssuerTrust == ContentTrustUntrustedExternal {
		record.Provenance.RuntimeVerified = false
		record.Provenance.VersionAlignment = VersionUnknown
	}
	record.Attestation.DigestVersion = 2
	record.Attestation.ContentDigest = DigestEvidenceRecord(*record)
	return env
}

// MarkLocalFixtureEnvelope adds the same lifecycle metadata that a local tool
// adapter adds, while preserving a deterministic fixture evidence ID. It is
// intentionally meant for in-process eval fixtures; production adapters must
// use IssueEnvelope so external callers cannot choose a local EvidenceID.
func MarkLocalFixtureEnvelope(env Envelope, metadata IssueMetadata) Envelope {
	record, ok := env.UsableEvidence()
	if !ok {
		return env
	}
	record.Attestation.Issuer = strings.TrimSpace(metadata.Issuer)
	record.Attestation.RunID = strings.TrimSpace(metadata.RunID)
	record.Attestation.ConversationID = strings.TrimSpace(metadata.ConversationID)
	record.Attestation.TenantID = strings.TrimSpace(metadata.TenantID)
	if record.Attestation.ObservedAt.IsZero() {
		record.Attestation.ObservedAt = time.Now().UTC()
	}
	if metadata.ExpiresAfter > 0 {
		expiresAt := record.Attestation.ObservedAt.Add(metadata.ExpiresAfter)
		record.Attestation.ExpiresAt = &expiresAt
	}
	if strings.TrimSpace(metadata.TrustLevel) != "" {
		record.Attestation.IssuerTrust = strings.TrimSpace(metadata.TrustLevel)
	}
	if record.Attestation.IssuerTrust == "" {
		record.Attestation.IssuerTrust = ContentTrustTrustedLocal
	}
	if record.Attestation.IssuerTrust == ContentTrustUntrustedExternal {
		record.Provenance.RuntimeVerified = false
		record.Provenance.VersionAlignment = VersionUnknown
	}
	record.Attestation.DigestVersion = 2
	record.Attestation.ContentDigest = DigestEvidenceRecord(*record)
	return env
}

func DigestEvidenceItem(item EvidenceItem) string {
	return DigestEvidenceRecord(EvidenceRecord{
		EvidenceID: item.EvidenceID,
		Provenance: Provenance{
			SourceKind:       item.SourceKind,
			Repository:       item.Repository,
			Revision:         item.Revision,
			SnapshotDigest:   item.SnapshotDigest,
			VersionAlignment: item.VersionAlignment,
			RuntimeVerified:  item.RuntimeVerified,
			PayloadTrust:     item.PayloadTrust,
		},
		Attestation: Attestation{
			Issuer:         item.Issuer,
			RunID:          item.RunID,
			ConversationID: item.ConversationID,
			TenantID:       item.TenantID,
			ObservedAt:     item.ObservedAt,
			ExpiresAt:      item.ExpiresAt,
			ContentDigest:  item.ContentDigest,
			DigestVersion:  item.DigestVersion,
			IssuerTrust:    item.IssuerTrust,
		},
		ResultMetadata: item.ResultMetadata,
		Observations:   item.Observations,
	})
}

func DigestEvidenceRecord(record EvidenceRecord) string {
	canonical := struct {
		Provenance     Provenance     `json:"provenance"`
		ResultMetadata ResultMetadata `json:"result_metadata"`
		Observations   []Observation  `json:"observations"`
	}{
		Provenance:     record.Provenance,
		ResultMetadata: record.ResultMetadata,
		Observations:   record.Observations,
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func IsExpired(item EvidenceItem, now time.Time) bool {
	if item.ExpiresAt == nil || item.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(*item.ExpiresAt)
}
