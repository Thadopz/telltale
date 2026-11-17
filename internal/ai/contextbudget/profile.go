package contextbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	DefaultContextWindowTokens = 32 * 1024
	DefaultWorkingPromptCap    = 128 * 1024
	DefaultMaxOutputTokens     = 8192
	DefaultSafetyMarginRatio   = 0.10
	DefaultMinSafetyTokens     = 2048
	DefaultCalibrationFactor   = 1.0
)

// ModelContextProfile describes the limits that the application can safely
// assume for one configured chat model. Provider capability discovery is
// intentionally not used; the deployment owns these values in config.
type ModelContextProfile struct {
	ConfigKey             string
	ModelName             string
	ContextWindowTokens   int
	MaxOutputTokens       int
	WorkingPromptCap      int
	SafetyMarginRatio     float64
	MinSafetyMarginTokens int
	LegacyPromptCap       int
}

// PromptBudget is the result of the preflight calculation. InputLimitTokens
// includes the prompt overhead and safety margin; ContentBudgetTokens is the
// portion available to conversation data and dynamic context layers.
type PromptBudget struct {
	InputLimitTokens         int
	ContentBudgetTokens      int
	OutputReserveTokens      int
	PromptOverheadTokens     int
	SafetyMarginTokens       int
	CalibrationFactor        float64
	EstimatedContentTokens   int
	EffectiveEstimatedTokens int
}

// BudgetReport is safe to attach to context and traces because it contains
// only aggregate sizes, never prompt content.
type BudgetReport struct {
	ToolReserveTokens     int      `json:"tool_reserve_tokens,omitzero"`
	ToolOutputTokens      int      `json:"tool_output_tokens,omitzero"`
	KnowledgeTargetTokens int      `json:"knowledge_target_tokens,omitzero"`
	EstimatedTokens       int      `json:"estimated_tokens,omitempty"`
	EffectiveEstimated    int      `json:"effective_estimated_tokens,omitempty"`
	FinalBudgetTokens     int      `json:"final_budget_tokens,omitempty"`
	InputLimitTokens      int      `json:"input_limit_tokens,omitempty"`
	OutputReserveTokens   int      `json:"output_reserve_tokens,omitempty"`
	PromptOverheadTokens  int      `json:"prompt_overhead_tokens,omitempty"`
	SafetyMarginTokens    int      `json:"safety_margin_tokens,omitempty"`
	CalibrationFactor     float64  `json:"calibration_factor,omitempty"`
	TrimmedLayers         []string `json:"trimmed_layers,omitempty"`
	ModelName             string   `json:"model_name,omitempty"`
	ContextWindowTokens   int      `json:"context_window_tokens,omitempty"`
}

type budgetReportKey struct{}

// WithBudgetReport attaches aggregate budget information to a request.
func WithBudgetReport(ctx context.Context, report BudgetReport) context.Context {
	return context.WithValue(ctx, budgetReportKey{}, report)
}

// BudgetReportFromContext returns the latest budget report, when one exists.
func BudgetReportFromContext(ctx context.Context) (BudgetReport, bool) {
	if ctx == nil {
		return BudgetReport{}, false
	}
	report, ok := ctx.Value(budgetReportKey{}).(BudgetReport)
	return report, ok
}

var (
	warnedMissingWindow sync.Map
	warnedLegacyCap     sync.Map
)

// ConfiguredProfile reads one model's context settings. Missing context
// window settings fall back to a conservative 32K limit and are logged once.
func ConfiguredProfile(ctx context.Context, configKey string) ModelContextProfile {
	profile := ModelContextProfile{
		ConfigKey:             strings.TrimSpace(configKey),
		ModelName:             configString(ctx, configKey+".model", "configured-model"),
		ContextWindowTokens:   configInt(ctx, configKey+".context_window_tokens", 0),
		MaxOutputTokens:       configInt(ctx, configKey+".max_output_tokens", 0),
		WorkingPromptCap:      configInt(ctx, "context.working_prompt_cap_tokens", DefaultWorkingPromptCap),
		SafetyMarginRatio:     configFloat(ctx, "context.safety_margin_ratio", DefaultSafetyMarginRatio),
		MinSafetyMarginTokens: configInt(ctx, "context.min_safety_margin_tokens", DefaultMinSafetyTokens),
		LegacyPromptCap:       configInt(ctx, "context.max_prompt_tokens", 0),
	}

	if profile.ContextWindowTokens <= 0 {
		profile.ContextWindowTokens = configInt(ctx, "context.default_context_window_tokens", DefaultContextWindowTokens)
		warnMissingContextWindow(ctx, profile.ConfigKey, profile.ContextWindowTokens)
	}
	if profile.MaxOutputTokens <= 0 {
		profile.MaxOutputTokens = configInt(ctx, "context.default_max_output_tokens", DefaultMaxOutputTokens)
	}
	if profile.LegacyPromptCap > 0 {
		if _, loaded := warnedLegacyCap.LoadOrStore("legacy", struct{}{}); !loaded {
			g.Log().Warningf(ctx, "context.max_prompt_tokens is deprecated; using it only as a hard upper bound=%d", profile.LegacyPromptCap)
		}
	}
	return normalizeProfile(profile)
}

// MergeProfiles returns a safe profile for a request that may use primary and
// failover models. The smallest window/cap and largest output reservation are
// used so a failover request receives the same safe prompt size.
func MergeProfiles(primary ModelContextProfile, failover *ModelContextProfile) ModelContextProfile {
	primary = normalizeProfile(primary)
	if failover == nil {
		return primary
	}
	backup := normalizeProfile(*failover)
	primary.ContextWindowTokens = minPositive(primary.ContextWindowTokens, backup.ContextWindowTokens)
	primary.WorkingPromptCap = minPositive(primary.WorkingPromptCap, backup.WorkingPromptCap)
	if backup.MaxOutputTokens > primary.MaxOutputTokens {
		primary.MaxOutputTokens = backup.MaxOutputTokens
	}
	if backup.SafetyMarginRatio > primary.SafetyMarginRatio {
		primary.SafetyMarginRatio = backup.SafetyMarginRatio
	}
	if backup.MinSafetyMarginTokens > primary.MinSafetyMarginTokens {
		primary.MinSafetyMarginTokens = backup.MinSafetyMarginTokens
	}
	if primary.LegacyPromptCap <= 0 || (backup.LegacyPromptCap > 0 && backup.LegacyPromptCap < primary.LegacyPromptCap) {
		primary.LegacyPromptCap = backup.LegacyPromptCap
	}
	return primary
}

// Calculate derives the content budget from the configured model limits and
// the prompt overhead measured by the caller.
func Calculate(profile ModelContextProfile, promptOverhead int, estimatedContent int, calibrationFactor float64) (PromptBudget, error) {
	profile = normalizeProfile(profile)
	if profile.ContextWindowTokens <= profile.MaxOutputTokens {
		return PromptBudget{}, fmt.Errorf("context window %d is not larger than output reserve %d", profile.ContextWindowTokens, profile.MaxOutputTokens)
	}

	inputLimit := profile.ContextWindowTokens - profile.MaxOutputTokens
	inputLimit = minPositive(inputLimit, profile.WorkingPromptCap)
	if profile.LegacyPromptCap > 0 {
		inputLimit = minPositive(inputLimit, profile.LegacyPromptCap)
	}
	if inputLimit <= 0 {
		return PromptBudget{}, fmt.Errorf("no input budget remains after model limits")
	}

	if promptOverhead < 0 {
		promptOverhead = 0
	}
	safety := int(math.Ceil(float64(inputLimit) * profile.SafetyMarginRatio))
	if safety < profile.MinSafetyMarginTokens {
		safety = profile.MinSafetyMarginTokens
	}
	contentBudget := inputLimit - promptOverhead - safety
	if contentBudget <= 0 {
		return PromptBudget{}, fmt.Errorf("prompt overhead %d and safety margin %d exhaust input budget %d", promptOverhead, safety, inputLimit)
	}

	factor := normalizeCalibrationFactor(calibrationFactor)
	if estimatedContent < 0 {
		estimatedContent = 0
	}
	effectiveEstimate := int(math.Ceil(float64(estimatedContent) * factor))
	return PromptBudget{
		InputLimitTokens:         inputLimit,
		ContentBudgetTokens:      contentBudget,
		OutputReserveTokens:      profile.MaxOutputTokens,
		PromptOverheadTokens:     promptOverhead,
		SafetyMarginTokens:       safety,
		CalibrationFactor:        factor,
		EstimatedContentTokens:   estimatedContent,
		EffectiveEstimatedTokens: effectiveEstimate,
	}, nil
}

// EstimateToolSchemas estimates the serialized tool declarations that will be
// sent to a model. A failed Info call is omitted because the actual request
// cannot contain a schema that was not produced.
func EstimateToolSchemas(ctx context.Context, tools []tool.BaseTool) int {
	total := 0
	for _, candidate := range tools {
		if candidate == nil {
			continue
		}
		info, err := candidate.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		encoded, err := json.Marshal(info)
		if err != nil {
			continue
		}
		total += EstimateTextTokens(string(encoded)) + 4
	}
	return total
}

// EstimateStaticPrompt returns the token estimate for prompt text plus a
// small protocol allowance. It deliberately uses the same heuristic as the
// memory compactor so preflight and persistence make comparable decisions.
func EstimateStaticPrompt(text string) int {
	return EstimateTextTokens(text) + 32
}

// EstimateTextTokens uses the same conservative language-aware heuristic as
// the memory package without importing it, so the profile package can also be
// used by memory policy resolution without creating an import cycle.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	asciiCount := 0
	nonASCIICount := 0
	for _, r := range text {
		if r < 128 {
			asciiCount++
			continue
		}
		nonASCIICount++
	}
	return (asciiCount+3)/4 + nonASCIICount
}

func normalizeProfile(profile ModelContextProfile) ModelContextProfile {
	if profile.ContextWindowTokens <= 0 {
		profile.ContextWindowTokens = DefaultContextWindowTokens
	}
	if profile.MaxOutputTokens <= 0 {
		profile.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if profile.WorkingPromptCap <= 0 {
		profile.WorkingPromptCap = DefaultWorkingPromptCap
	}
	if profile.SafetyMarginRatio <= 0 || profile.SafetyMarginRatio >= 1 {
		profile.SafetyMarginRatio = DefaultSafetyMarginRatio
	}
	if profile.MinSafetyMarginTokens <= 0 {
		profile.MinSafetyMarginTokens = DefaultMinSafetyTokens
	}
	return profile
}

func normalizeCalibrationFactor(factor float64) float64 {
	if factor < 1 || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return DefaultCalibrationFactor
	}
	if factor > 1.5 {
		return 1.5
	}
	return factor
}

func minPositive(left int, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}

func warnMissingContextWindow(ctx context.Context, configKey string, fallback int) {
	key := strings.TrimSpace(configKey)
	if key == "" {
		key = "default"
	}
	if _, loaded := warnedMissingWindow.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	g.Log().Warningf(ctx, "model %s has no context_window_tokens; using conservative fallback=%d", key, fallback)
}

func configString(ctx context.Context, key string, fallback string) string {
	if value, err := g.Cfg().Get(ctx, key); err == nil && value != nil {
		if configured := strings.TrimSpace(value.String()); configured != "" {
			return configured
		}
	}
	return fallback
}

func configInt(ctx context.Context, key string, fallback int) int {
	if value, err := g.Cfg().Get(ctx, key); err == nil && value != nil && value.Int() > 0 {
		return value.Int()
	}
	return fallback
}

func configFloat(ctx context.Context, key string, fallback float64) float64 {
	if value, err := g.Cfg().Get(ctx, key); err == nil && value != nil && value.Float64() > 0 {
		return value.Float64()
	}
	return fallback
}

// CalibrationRecord contains aggregate model accounting only. It never
// stores prompt text or user data.
type CalibrationRecord struct {
	ModelKey       string    `json:"model_key"`
	EstimateFactor float64   `json:"estimate_factor"`
	SampleCount    int       `json:"sample_count"`
	LastPrompt     int       `json:"last_prompt_tokens"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CalibrationStore interface {
	Load(context.Context, string) (CalibrationRecord, error)
	Save(context.Context, CalibrationRecord) error
}

type Calibrator struct {
	mu      sync.Mutex
	store   CalibrationStore
	records map[string]CalibrationRecord
}

var defaultCalibrator = NewCalibrator(NewSQLiteCalibrationStore())

// DefaultCalibrator is shared by chat requests in the process. Its store is
// SQLite-backed when the standalone schema has been initialized, and the
// in-memory record remains usable when persistence is unavailable.
func DefaultCalibrator() *Calibrator {
	return defaultCalibrator
}

// CalibrationKey separates model roles and model names without including API
// keys or other sensitive configuration values.
func CalibrationKey(profile ModelContextProfile) string {
	configKey := strings.TrimSpace(profile.ConfigKey)
	modelName := strings.TrimSpace(profile.ModelName)
	if configKey == "" {
		configKey = "model"
	}
	if modelName == "" {
		modelName = "configured-model"
	}
	return configKey + ":" + modelName
}

// CalibrationKeyForModel maps provider-reported model names back to the
// configured role so quick, reasoning, and failover models do not share an
// estimate accidentally.
func CalibrationKeyForModel(ctx context.Context, modelName string) string {
	modelName = strings.TrimSpace(modelName)
	for _, configKey := range []string{"quick_chat_model", "think_chat_model", "failover_chat_model"} {
		profile := ConfiguredProfile(ctx, configKey)
		if strings.EqualFold(strings.TrimSpace(profile.ModelName), modelName) {
			return CalibrationKey(profile)
		}
	}
	if modelName == "" {
		modelName = "configured-model"
	}
	return "model:" + modelName
}

func NewCalibrator(store CalibrationStore) *Calibrator {
	return &Calibrator{store: store, records: make(map[string]CalibrationRecord)}
}

func (c *Calibrator) Factor(ctx context.Context, modelKey string) float64 {
	if c == nil {
		return DefaultCalibrationFactor
	}
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return DefaultCalibrationFactor
	}

	c.mu.Lock()
	if record, ok := c.records[modelKey]; ok {
		c.mu.Unlock()
		return normalizeCalibrationFactor(record.EstimateFactor)
	}
	c.mu.Unlock()

	if c.store != nil {
		if record, err := c.store.Load(ctx, modelKey); err == nil && record.ModelKey != "" {
			c.mu.Lock()
			c.records[modelKey] = record
			c.mu.Unlock()
			return normalizeCalibrationFactor(record.EstimateFactor)
		}
	}
	return DefaultCalibrationFactor
}

func (c *Calibrator) Observe(ctx context.Context, modelKey string, estimated int, actual int) {
	if c == nil || estimated <= 0 || actual <= 0 {
		return
	}
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return
	}

	observed := float64(actual) / float64(estimated)
	if observed < 1 {
		observed = 1
	}
	if observed > 1.5 {
		observed = 1.5
	}

	c.mu.Lock()
	record, ok := c.records[modelKey]
	if !ok && c.store != nil {
		if loaded, err := c.store.Load(ctx, modelKey); err == nil {
			record = loaded
		}
	}
	if record.EstimateFactor < 1 || record.EstimateFactor > 1.5 {
		record.EstimateFactor = 1
	}
	record.ModelKey = modelKey
	record.EstimateFactor = record.EstimateFactor*0.8 + observed*0.2
	record.EstimateFactor = normalizeCalibrationFactor(record.EstimateFactor)
	record.SampleCount++
	record.LastPrompt = actual
	record.UpdatedAt = time.Now()
	c.records[modelKey] = record
	c.mu.Unlock()

	if c.store != nil {
		_ = c.store.Save(ctx, record)
	}
}

type sqliteCalibrationStore struct{}

// NewSQLiteCalibrationStore returns a best-effort SQLite-backed calibration
// store. Callers may use NewCalibrator(nil) for an in-memory-only instance.
func NewSQLiteCalibrationStore() CalibrationStore {
	return sqliteCalibrationStore{}
}

func (sqliteCalibrationStore) Load(ctx context.Context, modelKey string) (record CalibrationRecord, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("sqlite calibration store unavailable: %v", recovered)
		}
	}()
	err = g.DB().Model("context_budget_calibration").Ctx(ctx).Where("model_key", modelKey).Scan(&record)
	return record, err
}

func (sqliteCalibrationStore) Save(ctx context.Context, record CalibrationRecord) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("sqlite calibration store unavailable: %v", recovered)
		}
	}()
	data := g.Map{
		"model_key":          record.ModelKey,
		"estimate_factor":    record.EstimateFactor,
		"sample_count":       record.SampleCount,
		"last_prompt_tokens": record.LastPrompt,
		"updated_at":         record.UpdatedAt,
	}
	if _, err := g.DB().Model("context_budget_calibration").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return err
	}
	_, err = g.DB().Model("context_budget_calibration").Ctx(ctx).Where("model_key", record.ModelKey).Data(data).Update()
	return err
}
