package mem

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

type sqliteMemoryStore struct{}

type mysqlTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type contextSnapshotRow struct {
	Summary           string    `json:"summary"`
	FactsJSON         string    `json:"facts_json"`
	Generation        int       `json:"generation"`
	BeforeTokens      int       `json:"before_tokens"`
	AfterTokens       int       `json:"after_tokens"`
	CompactedMessages int       `json:"compacted_messages"`
	CreatedAt         time.Time `json:"created_at"`
}

func newSQLiteMemoryStore() *sqliteMemoryStore {
	return &sqliteMemoryStore{}
}

func (s *sqliteMemoryStore) Load(id string) (*memorySnapshot, error) {
	ctx := context.Background()

	var turns []mysqlTurn

	summaryRecord, err := g.DB().Model("conversation_summaries").
		Ctx(ctx).
		Fields("summary", "updated_at").
		Where("conversation_id", id).
		One()
	if err != nil {
		return nil, err
	}

	var compactionRows []contextSnapshotRow
	err = g.DB().Model("context_snapshots").
		Ctx(ctx).
		Fields("summary", "facts_json", "generation", "before_tokens", "after_tokens", "compacted_messages", "created_at").
		Where("conversation_id", id).
		OrderDesc("id").
		Limit(1).
		Scan(&compactionRows)
	if err != nil && !isMissingContextSnapshotsTable(err) {
		return nil, err
	}
	var state taskState
	if !summaryRecord.IsEmpty() {
		_ = json.Unmarshal([]byte(summaryRecord["summary"].String()), &state)
	}
	if state.Version != 1 {
		state.CoveredTurns = 0
	}
	if state.CoveredTurns < 0 {
		return nil, fmt.Errorf("invalid compaction checkpoint")
	}
	if err := g.DB().Model("conversation_turns").Ctx(ctx).Fields("role", "content").Where("conversation_id", id).OrderAsc("id").Limit(2147483647).Offset(state.CoveredTurns).Scan(&turns); err != nil {
		return nil, err
	}
	if len(turns) == 0 && summaryRecord.IsEmpty() && len(compactionRows) == 0 {
		return nil, nil
	}

	snapshot := &memorySnapshot{
		ID:            id,
		Messages:      make([]*schema.Message, 0, len(turns)),
		MaxWindowSize: defaultStoredWindowSize,
		UpdatedAt:     time.Now(),
	}
	for _, turn := range turns {
		snapshot.Messages = append(snapshot.Messages, &schema.Message{
			Role:    schema.RoleType(turn.Role),
			Content: turn.Content,
		})
	}
	if !summaryRecord.IsEmpty() {
		snapshot.Summary = summaryRecord["summary"].String()
		if updatedAt := summaryRecord["updated_at"].Time(); !updatedAt.IsZero() {
			snapshot.UpdatedAt = updatedAt
		}
	}
	if len(compactionRows) > 0 {
		row := compactionRows[0]
		if strings.TrimSpace(row.Summary) != "" && strings.TrimSpace(snapshot.Summary) == "" {
			snapshot.Summary = row.Summary
		}
		if strings.TrimSpace(row.FactsJSON) != "" {
			if err := json.Unmarshal([]byte(row.FactsJSON), &snapshot.Facts); err != nil {
				return nil, err
			}
		}
		snapshot.Compaction = CompactionMetadata{
			Generation:        row.Generation,
			BeforeTokens:      row.BeforeTokens,
			AfterTokens:       row.AfterTokens,
			CompactedMessages: row.CompactedMessages,
			CreatedAt:         row.CreatedAt,
		}
	}
	return snapshot, nil
}

func (s *sqliteMemoryStore) Save(snapshot *memorySnapshot) error {
	if snapshot == nil || strings.TrimSpace(snapshot.ID) == "" {
		return nil
	}

	return g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		if err := ensureConversation(ctx, tx, snapshot); err != nil {
			return err
		}

		if _, err := tx.Model("conversation_turns").
			Ctx(ctx).
			Where("conversation_id", snapshot.ID).
			Delete(); err != nil {
			return err
		}

		if len(snapshot.Messages) > 0 {
			turns := make(g.List, 0, len(snapshot.Messages))
			for _, msg := range snapshot.Messages {
				if msg == nil {
					continue
				}
				turns = append(turns, g.Map{
					"conversation_id": snapshot.ID,
					"role":            string(msg.Role),
					"content":         msg.Content,
				})
			}
			if len(turns) > 0 {
				if _, err := tx.Model("conversation_turns").Ctx(ctx).Data(turns).Insert(); err != nil {
					return err
				}
			}
		}

		if strings.TrimSpace(snapshot.Summary) == "" {
			_, err := tx.Model("conversation_summaries").
				Ctx(ctx).
				Where("conversation_id", snapshot.ID).
				Delete()
			if err != nil {
				return err
			}
		} else if err := upsertSummary(ctx, tx, snapshot); err != nil {
			return err
		}
		if snapshot.Compaction.Pending || len(snapshot.Facts) > 0 {
			if err := saveContextSnapshot(ctx, tx, snapshot); err != nil && !isMissingContextSnapshotsTable(err) {
				return err
			}
		}
		return nil
	})
}

func (s *sqliteMemoryStore) Append(snapshot *memorySnapshot, msg *schema.Message) error {
	if snapshot == nil || msg == nil || strings.TrimSpace(snapshot.ID) == "" {
		return nil
	}

	return g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		if err := ensureConversation(ctx, tx, snapshot); err != nil {
			return err
		}

		if _, err := tx.Model("conversation_turns").
			Ctx(ctx).
			Data(g.Map{
				"conversation_id": snapshot.ID,
				"role":            string(msg.Role),
				"content":         msg.Content,
			}).
			Insert(); err != nil {
			return err
		}
		if err := upsertSummary(ctx, tx, snapshot); err != nil {
			return err
		}
		if snapshot.Compaction.Pending || len(snapshot.Facts) > 0 {
			if err := saveContextSnapshot(ctx, tx, snapshot); err != nil && !isMissingContextSnapshotsTable(err) {
				return err
			}
		}
		return nil
	})
}

// SaveContextFacts persists machine-readable context metadata without
// rewriting conversation_turns. The conversation table is an archive, so a
// fact update must not erase earlier turns just because it has no new message.
func (s *sqliteMemoryStore) SaveContextFacts(snapshot *memorySnapshot) error {
	if snapshot == nil || strings.TrimSpace(snapshot.ID) == "" {
		return nil
	}
	return g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		if err := ensureConversation(ctx, tx, snapshot); err != nil {
			return err
		}
		if strings.TrimSpace(snapshot.Summary) != "" {
			if err := upsertSummary(ctx, tx, snapshot); err != nil {
				return err
			}
		}
		if err := saveContextSnapshot(ctx, tx, snapshot); err != nil && !isMissingContextSnapshotsTable(err) {
			return err
		}
		return nil
	})
}

// mysqlMemoryStore and newMySQLMemoryStore remain as source-compatible test
// aliases for callers migrating from the server edition. The implementation
// now uses the configured local SQLite database.
type mysqlMemoryStore = sqliteMemoryStore

func newMySQLMemoryStore() *sqliteMemoryStore {
	return newSQLiteMemoryStore()
}

func ensureConversation(ctx context.Context, tx gdb.TX, snapshot *memorySnapshot) error {
	if _, err := tx.Model("conversations").
		Ctx(ctx).
		Data(g.Map{
			"conversation_id": snapshot.ID,
			"user_id":         normalizeScopeID(snapshot.UserID, snapshot.ID),
			"updated_at":      snapshot.UpdatedAt,
		}).
		InsertIgnore(); err != nil {
		return err
	}

	_, err := tx.Model("conversations").
		Ctx(ctx).
		Where("conversation_id", snapshot.ID).
		Data(g.Map{"updated_at": snapshot.UpdatedAt}).
		Update()
	return err
}

func upsertSummary(ctx context.Context, tx gdb.TX, snapshot *memorySnapshot) error {
	summary := persistedSummary(snapshot.Summary)
	if strings.TrimSpace(summary) == "" {
		return nil
	}

	if _, err := tx.Model("conversation_summaries").
		Ctx(ctx).
		Data(g.Map{
			"conversation_id": snapshot.ID,
			"summary":         summary,
			"updated_at":      snapshot.UpdatedAt,
		}).
		InsertIgnore(); err != nil {
		return err
	}

	_, err := tx.Model("conversation_summaries").
		Ctx(ctx).
		Where("conversation_id", snapshot.ID).
		Data(g.Map{
			"summary":    summary,
			"updated_at": snapshot.UpdatedAt,
		}).
		Update()
	return err
}

func saveContextSnapshot(ctx context.Context, tx gdb.TX, snapshot *memorySnapshot) error {
	safeFacts := sanitizeContextFacts(snapshot.Facts)
	factsJSON, err := json.Marshal(safeFacts)
	if err != nil {
		return err
	}
	createdAt := snapshot.Compaction.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err = tx.Model("context_snapshots").Ctx(ctx).Data(g.Map{
		"conversation_id":    snapshot.ID,
		"summary":            persistedSummary(snapshot.Summary),
		"facts_json":         string(factsJSON),
		"generation":         snapshot.Compaction.Generation,
		"before_tokens":      snapshot.Compaction.BeforeTokens,
		"after_tokens":       snapshot.Compaction.AfterTokens,
		"compacted_messages": snapshot.Compaction.CompactedMessages,
		"created_at":         createdAt,
	}).Insert()
	return err
}

func sanitizeContextFacts(facts []ContextFact) []ContextFact {
	if len(facts) == 0 {
		return nil
	}
	safe := make([]ContextFact, 0, len(facts))
	for _, fact := range facts {
		switch strings.ToLower(strings.TrimSpace(fact.Kind)) {
		case "claim_id", "claim_type", "assertion_kind", "coverage_status", "hypothesis_id", "hypothesis_status", "evidence_id", "failed_query_id":
			// These are bounded machine identifiers and state enums. They must
			// remain exact so a later diagnostic run can resolve them.
		default:
			fact.Value = redactText(fact.Value)
		}
		safe = append(safe, fact)
	}
	return safe
}

func isMissingContextSnapshotsTable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context_snapshots") &&
		(strings.Contains(message, "no such table") || strings.Contains(message, "doesn't exist"))
}
