package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectAppConfigFile(t *testing.T) {
	t.Run("uses default config when local config is absent", func(t *testing.T) {
		root := t.TempDir()
		configDir := filepath.Join(root, "manifest", "config")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("mkdir config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, appDefaultConfig), []byte("server:\n  address: :6872\n"), 0o644); err != nil {
			t.Fatalf("write default config: %v", err)
		}

		got := selectAppConfigFile(root)
		if got.FileName != appDefaultConfig || got.IsLocal || !got.FileFound {
			t.Fatalf("expected default config, got %+v", got)
		}
	})

	t.Run("uses local config when present", func(t *testing.T) {
		root := t.TempDir()
		configDir := filepath.Join(root, "manifest", "config")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("mkdir config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, appDefaultConfig), []byte("server:\n  address: :6872\n"), 0o644); err != nil {
			t.Fatalf("write default config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, appLocalConfig), []byte("server:\n  address: :7777\n"), 0o644); err != nil {
			t.Fatalf("write local config: %v", err)
		}

		got := selectAppConfigFile(root)
		if got.FileName != appLocalConfig || !got.IsLocal || !got.FileFound {
			t.Fatalf("expected local config, got %+v", got)
		}
	})

	t.Run("falls back to default filename even if config file is missing", func(t *testing.T) {
		got := selectAppConfigFile(t.TempDir())
		if got.FileName != appDefaultConfig || got.IsLocal || got.FileFound {
			t.Fatalf("expected missing default fallback, got %+v", got)
		}
	})
}
