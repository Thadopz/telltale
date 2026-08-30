package sqlobserve

import (
	"context"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

func ConfiguredTargetEngine(ctx context.Context, target string) (DatabaseEngine, error) {
	engine, err := configuredDatabaseEngine(ctx, target)
	if err != nil {
		return EngineUnknown, err
	}
	if engine == EngineUnknown {
		return engine, fmt.Errorf("database target %q is not configured with a supported engine", target)
	}
	return engine, nil
}

func configuredAdapterForTarget(ctx context.Context, target string) (DatabaseAdapter, error) {
	engine, err := configuredDatabaseEngine(ctx, target)
	if err != nil {
		return nil, err
	}
	switch engine {
	case EngineMySQL:
		return newMysqlAdapter(), nil
	case EnginePostgreSQL:
		return newPostgreSQLAdapter(), nil
	case EngineSQLite, EngineUnknown:
		return unsupportedAdapter{engine: engine}, nil
	default:
		return unsupportedAdapter{engine: EngineUnknown}, nil
	}
}

func configuredTargetName(ctx context.Context, target string) string {
	target = strings.TrimSpace(target)
	if target != "" && target != "default" {
		return target
	}
	if configuredDefault := optionalConfigString(ctx, "database.default_target"); configuredDefault != "" {
		return configuredDefault
	}
	return "default"
}

func configuredDatabaseEngine(ctx context.Context, target string) (DatabaseEngine, error) {
	target = configuredTargetName(ctx, target)
	if !logicalTargetPattern.MatchString(target) {
		return EngineUnknown, fmt.Errorf("invalid logical database target %q", target)
	}
	prefix := databaseTargetPrefix(ctx, target)
	engineText := strings.ToLower(strings.TrimSpace(optionalConfigString(ctx, prefix+".type")))
	if engineText == "" && prefix == "database.default" {
		// Older configurations did not always declare type. The presence of
		// MySQL connection fields is enough to identify that legacy target.
		if optionalConfigString(ctx, prefix+".host") != "" || optionalConfigString(ctx, prefix+".user") != "" {
			engineText = string(EngineMySQL)
		}
	}
	if engineText == "" {
		return EngineUnknown, nil
	}
	switch engineText {
	case string(EngineMySQL):
		return EngineMySQL, nil
	case string(EnginePostgreSQL), "postgres":
		return EnginePostgreSQL, nil
	case string(EngineSQLite):
		return EngineSQLite, nil
	default:
		return EngineUnknown, nil
	}
}

func databaseTargetPrefix(ctx context.Context, target string) string {
	if target != "default" {
		return "database.targets." + target
	}
	if optionalConfigString(ctx, "database.targets.default.type") != "" {
		return "database.targets.default"
	}
	// database.default is the legacy location. It is intentionally kept as a
	// compatibility read path and is not used for Telltale's own storage.
	return "database.default"
}

func optionalConfigString(ctx context.Context, key string) string {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return ""
	}
	return strings.TrimSpace(value.String())
}
