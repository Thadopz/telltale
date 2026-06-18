package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

const (
	appConfigDir     = "manifest/config"
	appDefaultConfig = "config.yaml"
	appLocalConfig   = "config.local.yaml"
)

type appConfigSelection struct {
	Dir       string
	FileName  string
	IsLocal   bool
	FileFound bool
}

func configureAppConfig(ctx context.Context) {
	selection := selectAppConfigFile(".")
	adapter, ok := g.Cfg().GetAdapter().(*gcfg.AdapterFile)
	if !ok {
		g.Log().Warning(ctx, "skip app config selection: config adapter is not file-based")
		return
	}
	if err := adapter.SetPath(selection.Dir); err != nil {
		g.Log().Warningf(ctx, "set config path failed: dir=%s error=%v", selection.Dir, err)
		return
	}
	adapter.SetFileName(selection.FileName)
	adapter.Clear()
	if selection.IsLocal {
		g.Log().Infof(ctx, "using local config file: %s", filepath.Join(selection.Dir, selection.FileName))
	}
}

func selectAppConfigFile(root string) appConfigSelection {
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, filepath.FromSlash(appConfigDir))
	localPath := filepath.Join(dir, appLocalConfig)
	if regularFileExists(localPath) {
		return appConfigSelection{
			Dir:       dir,
			FileName:  appLocalConfig,
			IsLocal:   true,
			FileFound: true,
		}
	}
	return appConfigSelection{
		Dir:       dir,
		FileName:  appDefaultConfig,
		FileFound: regularFileExists(filepath.Join(dir, appDefaultConfig)),
	}
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
