package sqlobserve

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type gormQueryExecutor struct {
	db    *gorm.DB
	sqlDB interface{ Close() error }
}

var logicalTargetPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func openConfiguredMysqlExecutor(ctx context.Context, target string) (QueryExecutor, error) {
	dsn, err := ConfiguredMysqlDSN(ctx, target)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("load mysql connection: %w", err)
	}
	return &gormQueryExecutor{db: db, sqlDB: sqlDB}, nil
}

func (executor *gormQueryExecutor) Query(ctx context.Context, sql string) ([]map[string]any, error) {
	var rows []map[string]any
	if err := executor.db.WithContext(ctx).Raw(sql).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (executor *gormQueryExecutor) Close() error {
	if executor == nil || executor.sqlDB == nil {
		return nil
	}
	return executor.sqlDB.Close()
}

func ConfiguredMysqlDSN(ctx context.Context, target string) (string, error) {
	target = configuredTargetName(ctx, target)
	if !logicalTargetPattern.MatchString(target) {
		return "", fmt.Errorf("invalid logical database target %q", target)
	}
	engine, err := configuredDatabaseEngine(ctx, target)
	if err != nil {
		return "", err
	}
	if engine != EngineMySQL {
		return "", fmt.Errorf("database target %q uses engine %q, not mysql", target, engine)
	}
	prefix := databaseTargetPrefix(ctx, target)
	user, err := configString(ctx, prefix+".user")
	if err != nil {
		return "", err
	}
	pass, err := configString(ctx, prefix+".pass")
	if err != nil {
		return "", err
	}
	host, err := configString(ctx, prefix+".host")
	if err != nil {
		return "", err
	}
	port, err := configString(ctx, prefix+".port")
	if err != nil {
		return "", err
	}
	database, err := configString(ctx, prefix+".name")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local", user, pass, host, port, database), nil
}

func configString(ctx context.Context, key string) (string, error) {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(value.String())
	if text == "" {
		return "", fmt.Errorf("missing config %s", key)
	}
	return text, nil
}
