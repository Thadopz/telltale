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
	normalize(&env)
	if env.Status != StatusSuccess || !env.CanUseAsEvidence || len(env.Observations) == 0 {
		return env
	}

	env.EvidenceID = newID("ev")
	env.Issuer = strings.TrimSpace(metadata.Issuer)
	env.RunID = strings.TrimSpace(metadata.RunID)
	env.ConversationID = strings.TrimSpace(metadata.ConversationID)
	env.TenantID = strings.TrimSpace(metadata.TenantID)
	if env.ObservedAt.IsZero() {
		env.ObservedAt = time.Now().UTC()
	}
	if metadata.ExpiresAfter > 0 {
		expiresAt := env.ObservedAt.Add(metadata.ExpiresAfter)
		env.ExpiresAt = &expiresAt
	}
	if strings.TrimSpace(metadata.TrustLevel) != "" {
		env.TrustLevel = strings.TrimSpace(metadata.TrustLevel)
	}
	if env.TrustLevel == "" {
		env.TrustLevel = ContentTrustTrustedLocal
	}
	if env.TrustLevel == ContentTrustUntrustedExternal {
		env.RuntimeVerified = false
		env.VersionAlignment = VersionUnknown
	}
	env.ContentDigest = digestEnvelope(env)
	return env
}

// MarkLocalFixtureEnvelope adds the same lifecycle metadata that a local tool
// adapter adds, while preserving a deterministic fixture evidence ID. It is
// intentionally meant for in-process eval fixtures; production adapters must
// use IssueEnvelope so external callers cannot choose a local EvidenceID.
func MarkLocalFixtureEnvelope(env Envelope, metadata IssueMetadata) Envelope {
	normalize(&env)
	if env.Status != StatusSuccess || !env.CanUseAsEvidence || len(env.Observations) == 0 {
		return env
	}
	env.Issuer = strings.TrimSpace(metadata.Issuer)
	env.RunID = strings.TrimSpace(metadata.RunID)
	env.ConversationID = strings.TrimSpace(metadata.ConversationID)
	env.TenantID = strings.TrimSpace(metadata.TenantID)
	if env.ObservedAt.IsZero() {
		env.ObservedAt = time.Now().UTC()
	}
	if metadata.ExpiresAfter > 0 {
		expiresAt := env.ObservedAt.Add(metadata.ExpiresAfter)
		env.ExpiresAt = &expiresAt
	}
	if strings.TrimSpace(metadata.TrustLevel) != "" {
		env.TrustLevel = strings.TrimSpace(metadata.TrustLevel)
	}
	if env.TrustLevel == "" {
		env.TrustLevel = ContentTrustTrustedLocal
	}
	if env.TrustLevel == ContentTrustUntrustedExternal {
		env.RuntimeVerified = false
		env.VersionAlignment = VersionUnknown
	}
	env.ContentDigest = digestEnvelope(env)
	return env
}

func DigestEvidenceItem(item EvidenceItem) string {
	copyItem := item
	copyItem.ContentDigest = ""
	data, err := json.Marshal(copyItem)
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

func digestEnvelope(env Envelope) string {
	copyEnv := env
	copyEnv.EvidenceID = ""
	copyEnv.ContentDigest = ""
	data, err := json.Marshal(copyEnv)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
