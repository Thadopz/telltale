package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/sqlobserve"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/gogf/gf/v2/frame/g"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type MysqlCrudInput struct {
	DSN         string `json:"dsn,omitempty" jsonschema:"description=Deprecated. The server configured DSN is always used; user supplied DSN values are ignored"`
	SQL         string `json:"sql" jsonschema:"description=The SQL query to execute against the configured MySQL database"`
	OperateType string `json:"operate_type" jsonschema:"description=The SQL operation type: query, insert, update, or delete"`
	OperationID string `json:"operation_id,omitempty" jsonschema:"description=Required for write operations and used to query operation state after a timeout"`
}

func NewMysqlCrudTool() (tool.InvokableTool, error) {
	t, err := utils.InferOptionableTool(
		"mysql_crud",
		"Execute SQL queries against the server configured MySQL database and return results in JSON format. Use operate_type=query for reads. Write operations require server-side enablement.",
		func(ctx context.Context, input *MysqlCrudInput, opts ...tool.Option) (string, error) {
			if input == nil {
				return marshalToolError(fmt.Errorf("empty mysql input"), "invalid mysql input"), nil
			}
			input.OperateType = strings.ToLower(strings.TrimSpace(input.OperateType))
			if input.OperateType == "" {
				input.OperateType = "query"
			}
			input.SQL = strings.TrimSpace(input.SQL)
			if input.SQL == "" {
				return marshalToolError(fmt.Errorf("empty sql"), "invalid mysql input"), nil
			}
			if input.OperateType == "query" {
				guardedSQL, guardErr := guardReadOnlySQL(ctx, input.SQL)
				if guardErr != nil {
					return marshalToolError(guardErr, "mysql read query rejected by tool policy"), nil
				}
				input.SQL = guardedSQL
			}
			if IsWriteOperation(input.OperateType) && !mysqlWriteEnabled(ctx) {
				return marshalToolError(
					fmt.Errorf("mysql %s is disabled by tool policy", input.OperateType),
					"mysql write operations require explicit server configuration",
				), nil
			}
			if IsWriteOperation(input.OperateType) && strings.TrimSpace(input.OperationID) == "" {
				return marshalToolError(fmt.Errorf("operation_id is required for mysql writes"), "invalid mysql operation"), nil
			}

			dsn, err := configuredMysqlDSN(ctx)
			if err != nil {
				return marshalToolError(err, "load mysql configuration failed"), nil
			}
			db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
			if err != nil {
				return marshalToolError(err, "connect mysql failed"), nil
			}
			sqlDB, err := db.DB()
			if err == nil {
				defer sqlDB.Close()
			}

			switch input.OperateType {
			case "query":
				var results []map[string]any
				if err = db.WithContext(ctx).Raw(input.SQL).Scan(&results).Error; err != nil {
					return marshalToolError(err, "execute mysql query failed"), nil
				}
				return marshalToolData(results, fmt.Sprintf("query returned %d rows", len(results))), nil
			case "insert", "update", "delete":
				result := db.WithContext(ctx).Exec(input.SQL)
				if result.Error != nil {
					return marshalToolError(result.Error, "execute mysql statement failed"), nil
				}
				return marshalToolData(map[string]any{"rows_affected": result.RowsAffected}, "mysql statement executed"), nil
			default:
				return marshalToolError(fmt.Errorf("unsupported operate_type %q", input.OperateType), "invalid mysql operation"), nil
			}
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func guardReadOnlySQL(ctx context.Context, sql string) (string, error) {
	return sqlobserve.GuardMySQLReadOnly(sql, mysqlMaxRows(ctx))
}

func mysqlMaxRows(ctx context.Context) int {
	v, err := g.Cfg().Get(ctx, "tools.mysql.max_rows")
	if err != nil || v == nil {
		return 100
	}
	maxRows := v.Int()
	if maxRows <= 0 {
		return 100
	}
	return maxRows
}

func mysqlWriteEnabled(ctx context.Context) bool {
	v, err := g.Cfg().Get(ctx, "tools.mysql.allow_write")
	if err != nil {
		return false
	}
	return v.Bool()
}

func configuredMysqlDSN(ctx context.Context) (string, error) {
	return sqlobserve.ConfiguredMysqlDSN(ctx, "default")
}
