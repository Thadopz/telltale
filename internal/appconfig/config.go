package appconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

const (
	configDir     = "manifest/config"
	defaultConfig = "config.yaml"
	localConfig   = "config.local.yaml"
)

type configSelection struct {
	dir       string
	fileName  string
	isLocal   bool
	fileFound bool
}

type quietConfigLogKey struct{}

// WithQuietConfigLogging is used by machine-readable CLI commands so config
// selection messages cannot corrupt JSON on stdout.
func WithQuietConfigLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, quietConfigLogKey{}, true)
}

func quietConfigLogging(ctx context.Context) bool {
	value, _ := ctx.Value(quietConfigLogKey{}).(bool)
	return value
}

// Configure selects config.local.yaml when it exists and otherwise uses config.yaml.
// Call it once at the start of every executable, before reading any configuration.
func Configure(ctx context.Context) {
	ConfigureAt(ctx, ".")
}

// ConfigureAt selects configuration from a project root or directly from a
// config directory. This keeps the normal manifest/config layout while
// allowing CLI users to point at an explicit configuration directory.
func ConfigureAt(ctx context.Context, root string) {
	selection := selectConfigFile(root)
	adapter, ok := g.Cfg().GetAdapter().(*gcfg.AdapterFile)
	if !ok {
		g.Log().Warning(ctx, "skip app config selection: config adapter is not file-based")
		return
	}

	if err := adapter.SetPath(selection.dir); err != nil {
		g.Log().Warningf(ctx, "set config path failed: dir=%s error=%v", selection.dir, err)
		return
	}

	adapter.SetFileName(selection.fileName)
	adapter.Clear()
	if selection.isLocal && !quietConfigLogging(ctx) {
		g.Log().Infof(ctx, "using local config file: %s", filepath.Join(selection.dir, selection.fileName))
	}
}

// ValidateRuntimeSecurity prevents a standalone production process from
// silently running with fail-open authorization. Tests and evals may disable
// permissions explicitly because they inject their own fake authorization
// context.
func ValidateRuntimeSecurity(ctx context.Context) error {
	enabled := true
	if value, err := g.Cfg().Get(ctx, "permissions.enabled"); err == nil && value != nil {
		enabled = value.Bool()
	}
	environment := "standalone"
	if value, err := g.Cfg().Get(ctx, "runtime.environment"); err == nil && value != nil {
		environment = strings.ToLower(strings.TrimSpace(value.String()))
	}
	if err := validateRuntimeSecurity(enabled, environment); err != nil {
		return err
	}
	_, err := EvaluationEvidenceMode(ctx)
	return err
}

// EvaluationEvidenceMode returns the guarded evidence ablation mode. Normal
// runtimes always receive full unless an eval/test config explicitly opts in.
func EvaluationEvidenceMode(ctx context.Context) (string, error) {
	environment := "standalone"
	if value, err := g.Cfg().Get(ctx, "runtime.environment"); err == nil && value != nil {
		environment = strings.ToLower(strings.TrimSpace(value.String()))
	}
	mode := "full"
	if value, err := g.Cfg().Get(ctx, "evaluation.evidence_mode"); err == nil && value != nil {
		mode = strings.ToLower(strings.TrimSpace(value.String()))
	}
	if err := validateEvidenceModeEnvironment(mode, environment); err != nil {
		return "", err
	}
	if mode == "" {
		mode = "full"
	}
	return mode, nil
}

func validateRuntimeSecurity(enabled bool, environment string) error {
	if enabled {
		return nil
	}
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment != "test" && environment != "eval" {
		return fmt.Errorf("permissions.enabled=false is only allowed in test or eval environments")
	}
	return nil
}

func validateEvidenceModeEnvironment(mode string, environment string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "full" {
		return nil
	}
	if mode != "visible_unenforced" && mode != "raw_unenforced" {
		return fmt.Errorf("unsupported evaluation.evidence_mode %q", mode)
	}
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment != "test" && environment != "eval" {
		return fmt.Errorf("evaluation.evidence_mode=%s is only allowed in test or eval environments", mode)
	}
	return nil
}

func selectConfigFile(root string) configSelection {
	if root == "" {
		root = "."
	}

	dir := root
	if !regularFileExists(filepath.Join(dir, localConfig)) && !regularFileExists(filepath.Join(dir, defaultConfig)) {
		dir = filepath.Join(root, filepath.FromSlash(configDir))
	}
	localPath := filepath.Join(dir, localConfig)
	if regularFileExists(localPath) {
		return configSelection{
			dir:       dir,
			fileName:  localConfig,
			isLocal:   true,
			fileFound: true,
		}
	}

	return configSelection{
		dir:       dir,
		fileName:  defaultConfig,
		fileFound: regularFileExists(filepath.Join(dir, defaultConfig)),
	}
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
