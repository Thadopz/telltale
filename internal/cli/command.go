package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
)

const (
	ExitSuccess  = 0
	ExitRuntime  = 1
	ExitUsage    = 2
	ExitCanceled = 130
)

// Error carries the process exit code without making handlers call os.Exit.
// That keeps command handlers testable and lets old wrappers reuse them.
type Error struct {
	Code     int
	Err      error
	Reported bool
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var commandErr *Error
	if errors.As(err, &commandErr) && commandErr.Code != 0 {
		return commandErr.Code
	}
	if errors.Is(err, context.Canceled) {
		return ExitCanceled
	}
	return ExitRuntime
}

func IsReported(err error) bool {
	var commandErr *Error
	return errors.As(err, &commandErr) && commandErr.Reported
}

func usageError(err error) error {
	return &Error{Code: ExitUsage, Err: err}
}

func runtimeError(err error) error {
	if err == nil {
		return nil
	}
	code := ExitRuntime
	if errors.Is(err, context.Canceled) {
		code = ExitCanceled
	}
	return &Error{Code: code, Err: err}
}

func configure(ctx context.Context, configDir string) {
	if strings.TrimSpace(configDir) == "" {
		appconfig.Configure(ctx)
		return
	}
	// The caller has to select the configuration before initializing a
	// runtime. This helper is deliberately small so every command follows the
	// same rule.
	configureAt(ctx, configDir)
}

func quietCommandContext(ctx context.Context) context.Context {
	ctx = appconfig.WithQuietConfigLogging(ctx)
	return authz.WithQuietLogging(ctx)
}

func configureAt(ctx context.Context, configDir string) {
	appconfig.ConfigureAt(ctx, configDir)
}

func writeJSONError(w io.Writer, err error) {
	message := "command failed"
	if err != nil {
		message = err.Error()
	}
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"message": message},
	})
}

func authorizeCLIRead(ctx context.Context, toolName string, permission authz.Permission) error {
	decision := authz.NewAuthorizer().Authorize(ctx, authz.ToolCall{
		Name:       toolName,
		Permission: permission,
		Risk:       authz.RiskReadOnly,
	})
	if !decision.Allowed {
		return fmt.Errorf("CLI permission denied for %s: %s", toolName, decision.Reason)
	}
	return nil
}

func splitConfigDir(args []string) (string, []string, error) {
	cleaned := make([]string, 0, len(args))
	configDir := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "--config-dir=") {
			if configDir != "" {
				return "", nil, fmt.Errorf("--config-dir may only be provided once")
			}
			configDir = strings.TrimPrefix(arg, "--config-dir=")
			if strings.TrimSpace(configDir) == "" {
				return "", nil, fmt.Errorf("--config-dir requires a path")
			}
			continue
		}
		if arg == "--config-dir" {
			if configDir != "" {
				return "", nil, fmt.Errorf("--config-dir may only be provided once")
			}
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return "", nil, fmt.Errorf("--config-dir requires a path")
			}
			configDir = args[index+1]
			index++
			continue
		}
		cleaned = append(cleaned, arg)
	}
	return configDir, cleaned, nil
}
