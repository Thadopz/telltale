package mem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	MemoryModeOff    = "off"
	MemoryModeShadow = "shadow"
	MemoryModeActive = "active"

	profileStatusActive  = "active"
	profileStatusShadow  = "shadow"
	profileStatusRevoked = "revoked"

	memoryBufferPending    = "pending"
	memoryBufferRunning    = "running"
	memoryBufferProcessed  = "processed"
	memoryBufferDeadLetter = "dead_letter"

	memoryExtractorSchemaVersion = 1
	memoryExtractorVersion       = "profile-event-v1"
)

// MemoryPrincipal is the complete authorization scope for memory operations.
// Callers must not reduce it to a user ID because user IDs can repeat across
// tenants.
type MemoryPrincipal struct {
	TenantID string
	UserID   string
}

func (principal MemoryPrincipal) Validate() error {
	if strings.TrimSpace(principal.TenantID) == "" {
		return fmt.Errorf("memory tenant id is required")
	}
	if strings.TrimSpace(principal.UserID) == "" {
		return fmt.Errorf("memory user id is required")
	}
	return nil
}

type MemoryConfig struct {
	Mode               string
	TriggerTokens      int
	MaxBatchTokens     int
	IdleFlush          time.Duration
	CandidateTTL       time.Duration
	EventActiveAge     time.Duration
	EventRetention     time.Duration
	MaxEventsPerUser   int
	MaxContentRatio    float64
	ProfileRatio       float64
	WorkerPollInterval time.Duration
	LeaseDuration      time.Duration
	ExtractionTimeout  time.Duration
	MaxAttempts        int
}

func ConfiguredMemory(ctx context.Context) MemoryConfig {
	config := MemoryConfig{
		Mode:               MemoryModeShadow,
		TriggerTokens:      1024,
		MaxBatchTokens:     4096,
		IdleFlush:          30 * time.Minute,
		CandidateTTL:       30 * 24 * time.Hour,
		EventActiveAge:     30 * 24 * time.Hour,
		EventRetention:     180 * 24 * time.Hour,
		MaxEventsPerUser:   5000,
		MaxContentRatio:    0.20,
		ProfileRatio:       0.40,
		WorkerPollInterval: 15 * time.Second,
		LeaseDuration:      60 * time.Second,
		ExtractionTimeout:  30 * time.Second,
		MaxAttempts:        5,
	}

	config.Mode = memoryConfigString(ctx, "memory.mode", config.Mode)
	switch config.Mode {
	case MemoryModeOff, MemoryModeShadow, MemoryModeActive:
	default:
		config.Mode = MemoryModeShadow
	}
	config.TriggerTokens = memoryConfigInt(ctx, "memory.buffer.trigger_tokens", config.TriggerTokens)
	config.MaxBatchTokens = memoryConfigInt(ctx, "memory.buffer.max_batch_tokens", config.MaxBatchTokens)
	config.IdleFlush = time.Duration(memoryConfigInt(ctx, "memory.buffer.idle_flush_minutes", 30)) * time.Minute
	config.CandidateTTL = time.Duration(memoryConfigInt(ctx, "memory.profile.candidate_ttl_days", 30)) * 24 * time.Hour
	config.EventActiveAge = time.Duration(memoryConfigInt(ctx, "memory.event.active_days", 30)) * 24 * time.Hour
	config.EventRetention = time.Duration(memoryConfigInt(ctx, "memory.event.retention_days", 180)) * 24 * time.Hour
	config.MaxEventsPerUser = memoryConfigInt(ctx, "memory.event.max_per_user", config.MaxEventsPerUser)
	config.MaxContentRatio = memoryConfigFloat(ctx, "memory.context.max_content_ratio", config.MaxContentRatio)
	config.ProfileRatio = memoryConfigFloat(ctx, "memory.context.profile_ratio", config.ProfileRatio)
	config.WorkerPollInterval = time.Duration(memoryConfigInt(ctx, "memory.worker.poll_seconds", 15)) * time.Second
	config.LeaseDuration = time.Duration(memoryConfigInt(ctx, "memory.worker.lease_seconds", 60)) * time.Second
	config.ExtractionTimeout = time.Duration(memoryConfigInt(ctx, "memory.worker.extraction_timeout_seconds", 30)) * time.Second
	config.MaxAttempts = memoryConfigInt(ctx, "memory.worker.max_attempts", config.MaxAttempts)

	if config.MaxBatchTokens < config.TriggerTokens {
		config.MaxBatchTokens = config.TriggerTokens
	}
	if config.ProfileRatio <= 0 || config.ProfileRatio >= 1 {
		config.ProfileRatio = 0.40
	}
	return config
}

func memoryConfigString(ctx context.Context, key string, fallback string) string {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil || strings.TrimSpace(value.String()) == "" {
		return fallback
	}
	return strings.ToLower(strings.TrimSpace(value.String()))
}

func memoryConfigInt(ctx context.Context, key string, fallback int) int {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil || value.Int() <= 0 {
		return fallback
	}
	return value.Int()
}

func memoryConfigFloat(ctx context.Context, key string, fallback float64) float64 {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil || value.Float64() <= 0 {
		return fallback
	}
	return value.Float64()
}

type ProfileSlot struct {
	ID                   int64      `json:"id"`
	TenantID             string     `json:"tenant_id"`
	UserID               string     `json:"user_id"`
	SlotKey              string     `json:"slot_key"`
	ValueType            string     `json:"value_type"`
	ValueJSON            string     `json:"value_json"`
	ValueText            string     `json:"value_text"`
	Revision             int        `json:"revision"`
	Status               string     `json:"status"`
	Confirmation         string     `json:"confirmation"`
	Confidence           float64    `json:"confidence"`
	SourceConversationID string     `json:"source_conversation_id"`
	SourceTurnIDs        string     `json:"source_turn_ids"`
	SourceExcerpt        string     `json:"source_excerpt"`
	SourceHash           string     `json:"source_hash"`
	ValidFrom            time.Time  `json:"valid_from"`
	ValidUntil           *time.Time `json:"valid_until"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type ProfileCandidate struct {
	ID                   int64     `json:"id"`
	TenantID             string    `json:"tenant_id"`
	UserID               string    `json:"user_id"`
	SlotKey              string    `json:"slot_key"`
	ValueType            string    `json:"value_type"`
	ValueJSON            string    `json:"value_json"`
	ValueText            string    `json:"value_text"`
	Revision             int       `json:"revision"`
	Confidence           float64   `json:"confidence"`
	Status               string    `json:"status"`
	SourceConversationID string    `json:"source_conversation_id"`
	SourceTurnIDs        string    `json:"source_turn_ids"`
	SourceExcerpt        string    `json:"source_excerpt"`
	SourceHash           string    `json:"source_hash"`
	ExpiresAt            time.Time `json:"expires_at"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type MemoryEvent struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	UserID           string     `json:"user_id"`
	ConversationID   string     `json:"conversation_id"`
	IncidentID       string     `json:"incident_id"`
	Summary          string     `json:"summary"`
	TagsJSON         string     `json:"tags_json"`
	EntitiesJSON     string     `json:"entities_json"`
	SourceTurnIDs    string     `json:"source_turn_ids"`
	SourceHash       string     `json:"source_hash"`
	TimeExpression   string     `json:"time_expression"`
	TimeConfidence   string     `json:"time_confidence"`
	OccurredAt       *time.Time `json:"occurred_at"`
	RecordedAt       time.Time  `json:"recorded_at"`
	ProfileDeltaIDs  string     `json:"profile_delta_ids"`
	EvidenceIDs      string     `json:"evidence_ids"`
	EvidenceStatus   string     `json:"evidence_status"`
	Status           string     `json:"status"`
	VectorStatus     string     `json:"vector_status"`
	Vector           string     `json:"-"`
	MentionCount     int        `json:"mention_count"`
	ExtractorVersion string     `json:"extractor_version"`
	IdempotencyKey   string     `json:"idempotency_key"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type memoryBuffer struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id"`
	UserID                string     `json:"user_id"`
	ConversationID        string     `json:"conversation_id"`
	ConversationStorageID string     `json:"conversation_storage_id"`
	PendingTurnIDs        string     `json:"pending_turn_ids"`
	ClaimedTurnIDs        string     `json:"claimed_turn_ids"`
	PendingTokens         int        `json:"pending_tokens"`
	ClaimedTokens         int        `json:"claimed_tokens"`
	Status                string     `json:"status"`
	ExplicitFlush         bool       `json:"explicit_flush"`
	Generation            int        `json:"generation"`
	AttemptCount          int        `json:"attempt_count"`
	NextAttemptAt         time.Time  `json:"next_attempt_at"`
	LeaseUntil            *time.Time `json:"lease_until"`
	LastActivityAt        time.Time  `json:"last_activity_at"`
	LastError             string     `json:"last_error"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func memoryScopeID(principal MemoryPrincipal, conversationID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(principal.TenantID) + "\x00" + strings.TrimSpace(principal.UserID) + "\x00" + strings.TrimSpace(conversationID)))
	return "mb_" + hex.EncodeToString(digest[:])[:32]
}

// TenantConversationID creates the opaque conversation key used by the
// existing turn/session/tool stores. Their public APIs stay unchanged while
// the new diagnosis path no longer shares source rows across tenants.
func TenantConversationID(principal MemoryPrincipal, conversationID string) string {
	conversationID = strings.TrimSpace(conversationID)
	digest := sha256.Sum256([]byte(strings.TrimSpace(principal.TenantID) + "\x00" + strings.TrimSpace(principal.UserID) + "\x00" + conversationID))
	return "tenant_conversation_" + hex.EncodeToString(digest[:])[:32]
}

func memorySourceHash(turnIDs []int64, content string) string {
	encodedIDs, _ := json.Marshal(turnIDs)
	digest := sha256.Sum256(append(encodedIDs, []byte("\x00"+content)...))
	return hex.EncodeToString(digest[:])
}

func boundedMemoryText(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	return string([]rune(value)[:maxRunes])
}

func marshalMemoryJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
