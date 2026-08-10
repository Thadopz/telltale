package skills

import (
	"github.com/Thadopz/telltale/internal/authz"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gogf/gf/v2/frame/g"
	"gopkg.in/yaml.v3"
)

const (
	defaultMaxSelected  = 2
	defaultMaxBodyChars = 6000
)

type Config struct {
	Enabled      bool
	Dirs         []string
	MaxSelected  int
	MaxBodyChars int
}

type Skill struct {
	Name         string
	Description  string
	Domain       string
	AgentScopes  []authz.AgentScope
	Capabilities []string
	Path         string
	Body         string
}

type frontMatter struct {
	Name         string             `yaml:"name"`
	Description  string             `yaml:"description"`
	Domain       string             `yaml:"domain"`
	AgentScopes  []authz.AgentScope `yaml:"agent_scopes"`
	Capabilities []string           `yaml:"capabilities"`
}

// SelectionContext is the explicit authority context used to decide whether a
// skill may be shown to an agent. Query text can rank already-authorized
// skills, but it cannot grant access to another domain.
type SelectionContext struct {
	AgentScope   authz.AgentScope
	Domain       string
	Capabilities []string
}

func LoadSelectedContext(ctx context.Context, query string) string {
	cfg := LoadConfig(ctx)
	if !cfg.Enabled {
		return ""
	}
	selection := SelectionContext{
		AgentScope:   authz.AgentScopeFromContext(ctx),
		Capabilities: authz.AgentCapabilitiesFromContext(ctx),
	}
	if selection.AgentScope == "" {
		selection.AgentScope = authz.AgentScopeCoordinator
	}
	selection.Domain = domainForScope(selection.AgentScope)
	selected, err := SelectFor(ctx, cfg, query, selection)
	if err != nil {
		g.Log().Warningf(ctx, "load skills failed, err=%v", err)
		return ""
	}
	return FormatSelected(selected)
}

func LoadConfig(ctx context.Context) Config {
	cfg := Config{
		Enabled:      configBool(ctx, "skills.enabled", true),
		Dirs:         configStringSlice(ctx, "skills.dirs"),
		MaxSelected:  configInt(ctx, "skills.max_selected", defaultMaxSelected),
		MaxBodyChars: configInt(ctx, "skills.max_body_chars", defaultMaxBodyChars),
	}
	if len(cfg.Dirs) == 0 {
		cfg.Dirs = []string{"skills"}
	}
	if cfg.MaxSelected <= 0 {
		cfg.MaxSelected = defaultMaxSelected
	}
	if cfg.MaxBodyChars <= 0 {
		cfg.MaxBodyChars = defaultMaxBodyChars
	}
	return cfg
}

func Select(ctx context.Context, cfg Config, query string) ([]Skill, error) {
	return SelectFor(ctx, cfg, query, SelectionContext{})
}

func SelectFor(ctx context.Context, cfg Config, query string, selection SelectionContext) ([]Skill, error) {
	_ = ctx
	query = strings.TrimSpace(query)
	if query == "" || cfg.MaxSelected <= 0 || len(cfg.Dirs) == 0 {
		return nil, nil
	}

	all, err := Scan(cfg.Dirs)
	if err != nil {
		return nil, err
	}
	authorized := make([]Skill, 0, len(all))
	for _, skill := range all {
		if skillAllowedForSelection(skill, selection) {
			authorized = append(authorized, skill)
		}
	}
	scored := scoreSkills(query, authorized)
	if len(scored) == 0 {
		return nil, nil
	}
	if len(scored) > cfg.MaxSelected {
		scored = scored[:cfg.MaxSelected]
	}

	selected := make([]Skill, 0, len(scored))
	for _, candidate := range scored {
		skill := candidate.Skill
		skill.Body = trimRunesTail(skill.Body, cfg.MaxBodyChars)
		selected = append(selected, skill)
	}
	return selected, nil
}

func Scan(dirs []string) ([]Skill, error) {
	var skills []Skill
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.EqualFold(entry.Name(), "SKILL.md") {
				skill, err := ParseFile(path)
				if err != nil {
					g.Log().Warningf(context.Background(), "skip invalid skill %s, err=%v", path, err)
					return nil
				}
				skills = append(skills, skill)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Path < skills[j].Path
	})
	return skills, nil
}

func ParseFile(path string) (Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body, err := parseFrontMatter(string(raw))
	if err != nil {
		return Skill{}, err
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	body = strings.TrimSpace(body)
	if meta.Name == "" {
		return Skill{}, fmt.Errorf("missing skill name")
	}
	if meta.Description == "" {
		return Skill{}, fmt.Errorf("missing skill description")
	}
	return Skill{
		Name:         meta.Name,
		Description:  meta.Description,
		Domain:       strings.ToLower(strings.TrimSpace(meta.Domain)),
		AgentScopes:  normalizeAgentScopes(meta.AgentScopes),
		Capabilities: normalizeCapabilities(meta.Capabilities),
		Path:         path,
		Body:         body,
	}, nil
}

func skillAllowedForSelection(skill Skill, selection SelectionContext) bool {
	if selection.AgentScope == "" && strings.TrimSpace(selection.Domain) == "" && len(selection.Capabilities) == 0 {
		return true
	}
	scope := authz.AgentScope(strings.ToLower(strings.TrimSpace(string(selection.AgentScope))))
	domain := strings.ToLower(strings.TrimSpace(selection.Domain))
	if len(skill.AgentScopes) > 0 {
		matched := false
		for _, candidate := range skill.AgentScopes {
			if candidate == scope {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	} else if scope != authz.AgentScopeCoordinator {
		// A domain agent cannot consume an unscoped skill. This keeps legacy
		// descriptions from becoming an implicit cross-domain capability.
		return false
	}
	if strings.TrimSpace(skill.Domain) != "" && strings.ToLower(skill.Domain) != domain {
		return false
	}
	available := make(map[string]bool, len(selection.Capabilities))
	for _, capability := range normalizeCapabilities(selection.Capabilities) {
		available[capability] = true
	}
	for _, required := range skill.Capabilities {
		if !available[required] {
			return false
		}
	}
	return true
}

func domainForScope(scope authz.AgentScope) string {
	switch scope {
	case authz.AgentScopePayment:
		return "payment"
	case authz.AgentScopeObservability:
		return "observability"
	case authz.AgentScopeDatabase:
		return "database"
	case authz.AgentScopeKnowledge:
		return "knowledge"
	default:
		return "general"
	}
}

func normalizeAgentScopes(scopes []authz.AgentScope) []authz.AgentScope {
	seen := make(map[authz.AgentScope]bool, len(scopes))
	result := make([]authz.AgentScope, 0, len(scopes))
	for _, scope := range scopes {
		scope = authz.AgentScope(strings.ToLower(strings.TrimSpace(string(scope))))
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func normalizeCapabilities(capabilities []string) []string {
	seen := make(map[string]bool, len(capabilities))
	result := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if capability == "" || seen[capability] {
			continue
		}
		seen[capability] = true
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func FormatSelected(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}
	var b strings.Builder
	for _, skill := range selected {
		if strings.TrimSpace(skill.Body) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("==== Skill: ")
		b.WriteString(skill.Name)
		b.WriteString(" ====\n")
		b.WriteString(skill.Body)
		b.WriteString("\n==== End Skill ====")
	}
	return b.String()
}

func parseFrontMatter(content string) (frontMatter, string, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return frontMatter{}, "", fmt.Errorf("missing frontmatter")
	}

	var yamlPart strings.Builder
	var body strings.Builder
	closed := false
	for i := 1; i < len(lines); i++ {
		if !closed && strings.TrimSpace(lines[i]) == "---" {
			closed = true
			if i+1 < len(lines) {
				body.WriteString(strings.Join(lines[i+1:], ""))
			}
			break
		}
		yamlPart.WriteString(lines[i])
	}
	if !closed {
		return frontMatter{}, "", fmt.Errorf("unterminated frontmatter")
	}

	var meta frontMatter
	if err := yaml.Unmarshal([]byte(yamlPart.String()), &meta); err != nil {
		return frontMatter{}, "", err
	}
	return meta, body.String(), nil
}

type scoredSkill struct {
	Skill Skill
	Score int
}

func scoreSkills(query string, all []Skill) []scoredSkill {
	normalizedQuery := strings.ToLower(query)
	tokens := queryTokens(normalizedQuery)
	scored := make([]scoredSkill, 0, len(all))
	for _, skill := range all {
		haystack := strings.ToLower(skill.Name + " " + skill.Description)
		score := 0
		if strings.Contains(normalizedQuery, strings.ToLower(skill.Name)) {
			score += 5
		}
		for _, token := range tokens {
			if strings.Contains(haystack, token) {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredSkill{Skill: skill, Score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Skill.Name < scored[j].Skill.Name
		}
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func queryTokens(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if utf8.RuneCountInString(field) >= 2 {
			tokens = append(tokens, field)
		}
		if containsHan(field) {
			tokens = append(tokens, runeNgrams(field, 2)...)
			tokens = append(tokens, runeNgrams(field, 3)...)
		}
	}
	return tokens
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}

func runeNgrams(s string, n int) []string {
	if n <= 0 {
		return nil
	}
	runes := []rune(s)
	if len(runes) < n {
		return nil
	}
	out := make([]string, 0, len(runes)-n+1)
	for i := 0; i+n <= len(runes); i++ {
		out = append(out, string(runes[i:i+n]))
	}
	return out
}

func trimRunesTail(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func configStringSlice(ctx context.Context, key string) []string {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return nil
	}
	return v.Strings()
}

func configBool(ctx context.Context, key string, fallback bool) bool {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return fallback
	}
	return v.Bool()
}

func configInt(ctx context.Context, key string, fallback int) int {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return fallback
	}
	out := v.Int()
	if out == 0 {
		return fallback
	}
	return out
}
