package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectConfigFileUsesDefaultWhenLocalIsAbsent(t *testing.T) {
	root := t.TempDir()
	createConfigFile(t, root, defaultConfig)

	selection := selectConfigFile(root)

	if selection.fileName != defaultConfig || selection.isLocal || !selection.fileFound {
		t.Fatalf("expected default config, got %+v", selection)
	}
}

func TestSelectConfigFileUsesLocalWhenPresent(t *testing.T) {
	root := t.TempDir()
	createConfigFile(t, root, defaultConfig)
	createConfigFile(t, root, localConfig)

	selection := selectConfigFile(root)

	if selection.fileName != localConfig || !selection.isLocal || !selection.fileFound {
		t.Fatalf("expected local config, got %+v", selection)
	}
}

func TestSelectConfigFileFallsBackToDefaultNameWhenMissing(t *testing.T) {
	selection := selectConfigFile(t.TempDir())

	if selection.fileName != defaultConfig || selection.isLocal || selection.fileFound {
		t.Fatalf("expected missing default fallback, got %+v", selection)
	}
}

func TestValidateRuntimeSecurityRejectsFailOpenStandaloneConfiguration(t *testing.T) {
	if err := validateRuntimeSecurity(false, "standalone"); err == nil {
		t.Fatal("expected permissions disabled to be rejected in standalone")
	}
	if err := validateRuntimeSecurity(false, "production"); err == nil {
		t.Fatal("expected permissions disabled to be rejected in production")
	}
	if err := validateRuntimeSecurity(false, "test"); err != nil {
		t.Fatalf("expected test environment exception, got %v", err)
	}
	if err := validateRuntimeSecurity(false, "eval"); err != nil {
		t.Fatalf("expected eval environment exception, got %v", err)
	}
	if err := validateRuntimeSecurity(true, "standalone"); err != nil {
		t.Fatalf("enabled permissions should always pass, got %v", err)
	}
}

func createConfigFile(t *testing.T, root string, fileName string) {
	t.Helper()

	configPath := filepath.Join(root, "manifest", "config")
	if err := os.MkdirAll(configPath, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}

	filePath := filepath.Join(configPath, fileName)
	if err := os.WriteFile(filePath, []byte("server:\n  address: :6872\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}
