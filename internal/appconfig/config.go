package appconfig

import (
	"context"
	"os"
	"path/filepath"

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

// Configure selects config.local.yaml when it exists and otherwise uses config.yaml.
// Call it once at the start of every executable, before reading any configuration.
func Configure(ctx context.Context) {
	selection := selectConfigFile(".")
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
	if selection.isLocal {
		g.Log().Infof(ctx, "using local config file: %s", filepath.Join(selection.dir, selection.fileName))
	}
}

func selectConfigFile(root string) configSelection {
	if root == "" {
		root = "."
	}

	dir := filepath.Join(root, filepath.FromSlash(configDir))
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
