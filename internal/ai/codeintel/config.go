package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

func Enabled(ctx context.Context) bool {
	return configBool(ctx, "code.enabled", false)
}

func LoadFromConfig(ctx context.Context) (*Service, error) {
	cfg := LoadConfig(ctx)
	if !cfg.Enabled {
		return nil, nil
	}
	return NewService(cfg)
}

func LoadConfig(ctx context.Context) Config {
	config := Config{
		Enabled:                configBool(ctx, "code.enabled", false),
		Repository:             RepositoryName,
		ServiceName:            configString(ctx, "code.repositories.card_service.service_name", "card-service"),
		Root:                   configString(ctx, "code.repositories.card_service.root", ""),
		Revision:               configString(ctx, "code.repositories.card_service.revision", ""),
		DeployedRevision:       configString(ctx, "code.repositories.card_service.deployed_revision", ""),
		DeploymentManifestPath: configString(ctx, "code.repositories.card_service.deployment_manifest", ""),
		Include:                configStrings(ctx, "code.repositories.card_service.include", defaultInclude()),
		Exclude:                configStrings(ctx, "code.repositories.card_service.exclude", defaultExclude()),
		MaxFileBytes:           configInt(ctx, "code.limits.max_file_bytes", 1024*1024),
		MaxSearchResults:       configInt(ctx, "code.limits.max_search_results", 8),
		MaxReadLines:           configInt(ctx, "code.limits.max_read_lines", 160),
		MaxReadChars:           configInt(ctx, "code.limits.max_read_chars", 24000),
		MaxHandles:             configInt(ctx, "code.limits.max_handles", 64),
		HandleTTL:              configDuration(ctx, "code.limits.handle_ttl_seconds", 600) * time.Second,
		HandleUses:             configInt(ctx, "code.limits.handle_uses", 3),
	}
	if revision, imageDigest, ok := loadTrustedDeploymentManifest(config.DeploymentManifestPath); ok {
		config.DeployedRevision = revision
		config.DeploymentImageDigest = imageDigest
		config.DeployedRevisionTrusted = true
	}
	return config
}

type deploymentManifest struct {
	Revision    string `json:"revision"`
	ImageDigest string `json:"image_digest"`
}

func loadTrustedDeploymentManifest(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		g.Log().Warningf(context.Background(), "read deployment manifest failed: path=%s error=%v", path, err)
		return "", "", false
	}
	var manifest deploymentManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		// A tiny line-based fallback supports manifests emitted as simple YAML
		// without adding a second configuration parser to the code indexer.
		for _, line := range strings.Split(string(content), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			switch key {
			case "revision":
				manifest.Revision = value
			case "image_digest":
				manifest.ImageDigest = value
			}
		}
	}
	if strings.TrimSpace(manifest.Revision) == "" {
		g.Log().Warningf(context.Background(), "deployment manifest has no revision: path=%s", path)
		return "", "", false
	}
	return strings.TrimSpace(manifest.Revision), strings.TrimSpace(manifest.ImageDigest), true
}

func defaultInclude() []string {
	return []string{"cmd/**", "internal/**", "api/**", "migrations/**", "manifest/config/**"}
}

func defaultExclude() []string {
	return []string{".git/**", "vendor/**", ".env*", "**/*.key", "**/*.pem", "**/*.crt", "**/*secret*", "**/*credential*"}
}

func configBool(ctx context.Context, key string, fallback bool) bool {
	value, err := g.Cfg().GetWithEnv(ctx, key)
	if err != nil || value == nil {
		return fallback
	}
	return value.Bool()
}

func configInt(ctx context.Context, key string, fallback int) int {
	value, err := g.Cfg().GetWithEnv(ctx, key)
	if err != nil || value == nil || value.Int() <= 0 {
		return fallback
	}
	return value.Int()
}

func configDuration(ctx context.Context, key string, fallback int) time.Duration {
	value, err := g.Cfg().GetWithEnv(ctx, key)
	if err != nil || value == nil || value.Int() <= 0 {
		return time.Duration(fallback)
	}
	return time.Duration(value.Int())
}

func configString(ctx context.Context, key string, fallback string) string {
	value, err := g.Cfg().GetWithEnv(ctx, key)
	if err != nil || value == nil {
		return fallback
	}
	if text := strings.TrimSpace(value.String()); text != "" {
		return text
	}
	return fallback
}

func configStrings(ctx context.Context, key string, fallback []string) []string {
	value, err := g.Cfg().GetWithEnv(ctx, key)
	if err != nil || value == nil {
		return append([]string(nil), fallback...)
	}
	values := value.Strings()
	if len(values) == 0 {
		return append([]string(nil), fallback...)
	}
	return values
}
