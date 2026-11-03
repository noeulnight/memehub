package config

import (
	"strings"
	"testing"
)

func TestConfigurationValidation(t *testing.T) {
	baseline := map[string]string{"APP_ENV": "development", "HTTP_ADDR": "127.0.0.1:8080", "DATABASE_URL": "postgres://local:secret@localhost/memehub", "APP_BASE_URL": "http://localhost:4321", "OIDC_ISSUER_URL": "https://id.example.com", "OIDC_CLIENT_ID": "memehub", "OIDC_REDIRECT_URL": "http://localhost:4321/api/auth/callback", "OPENAI_MODEL": "configured-model", "SESSION_COOKIE_SECURE": "false", "SESSION_TTL_SECONDS": "604800", "TEXT_MAX_LENGTH": "10000"}
	for _, tc := range []struct {
		name, key, value string
		wantError        bool
	}{
		{name: "valid"},
		{"invalid vector switch", "VECTOR_SEARCH_ENABLED", "maybe", true},
		{"vector search without key", "VECTOR_SEARCH_ENABLED", "true", true},
		{"invalid deleted retention", "DELETED_IMAGE_RETENTION_DAYS", "0", true},
		{"excessive deleted retention", "DELETED_IMAGE_RETENTION_DAYS", "366", true},
		{"unsafe upload retention", "INCOMPLETE_UPLOAD_RETENTION_HOURS", "1", true},
		{"excessive upload retention", "INCOMPLETE_UPLOAD_RETENTION_HOURS", "169", true},
		{"invalid database", "DATABASE_URL", "postgres://secret@", true},
		{"cross origin callback", "OIDC_REDIRECT_URL", "https://evil.example/api/auth/callback", true},
		{"insecure production", "APP_ENV", "production", true},
		{"invalid boolean", "SESSION_COOKIE_SECURE", "maybe", true},
		{"unbounded session", "SESSION_TTL_SECONDS", "999999999999", true},
		{"missing model", "OPENAI_MODEL", "", true},
		{"invalid analysis timeout", "OPENAI_TIMEOUT_SECONDS", "0", true},
		{"excessive analysis timeout", "OPENAI_TIMEOUT_SECONDS", "601", true},
		{"invalid worker concurrency", "WORKER_CONCURRENCY", "0", true},
		{"excessive worker concurrency", "WORKER_CONCURRENCY", "17", true},
		{"invalid text limit", "TEXT_MAX_LENGTH", "-1", true},
		{"invalid upload limit", "UPLOAD_MAX_BYTES", "52428801", true},
		{"unsupported image type", "UPLOAD_ALLOWED_MIME_TYPES", "image/svg+xml", true},
		{"excessive signed URL lifetime", "S3_PRESIGN_TTL_SECONDS", "86400", true},
		{"storage without credentials", "S3_ENDPOINT", "http://localhost:9000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			for k, v := range baseline {
				values[k] = v
			}
			if tc.key != "" {
				values[tc.key] = tc.value
			}
			_, err := Parse(func(key string) string { return values[key] })
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("configuration error exposed a secret")
			}
		})
	}
}

func TestProductionUsesConfiguredDatabaseTransport(t *testing.T) {
	for _, mode := range []string{"disable", "verify-full"} {
		t.Run(mode, func(t *testing.T) {
			values := map[string]string{"APP_ENV": "production", "HTTP_ADDR": "0.0.0.0:8080", "DATABASE_URL": "postgres://memehub:secret@postgres-service.postgres.svc.cluster.local/memehub?sslmode=" + mode, "APP_BASE_URL": "https://meme.lth.so", "OIDC_ISSUER_URL": "https://id.example.com", "OIDC_CLIENT_ID": "memehub", "OIDC_REDIRECT_URL": "https://meme.lth.so/api/auth/callback", "OPENAI_MODEL": "configured-model", "SESSION_COOKIE_SECURE": "true", "SESSION_TTL_SECONDS": "604800", "TEXT_MAX_LENGTH": "10000"}
			if _, err := Parse(func(key string) string { return values[key] }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDemoConfiguration(t *testing.T) {
	values := map[string]string{"DEMO_MODE": "true", "OPENAI_API_KEY": "must-not-be-used", "S3_ENDPOINT": "http://localhost:9000"}
	get := func(key string) string { return values[key] }
	cfg, err := Parse(get)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DemoMode || cfg.Env != "development" || cfg.HTTPAddr != "127.0.0.1:8080" || cfg.OpenAIAPIKey != "" || cfg.S3Endpoint != "" || cfg.DatabaseURL == "" {
		t.Fatalf("unexpected demo configuration: %+v", cfg)
	}
	values["APP_BASE_URL"] = "http://localhost:4444"
	cfg, err = Parse(get)
	if err != nil || cfg.OIDCRedirectURL != "http://localhost:4444/api/auth/callback" {
		t.Fatalf("demo origin override failed: %v", err)
	}
	values["APP_ENV"] = "production"
	if _, err := Parse(get); err == nil {
		t.Fatal("production accepted demo mode")
	}
	values["DEMO_MODE"] = "invalid"
	if _, err := Parse(get); err == nil {
		t.Fatal("invalid demo flag accepted")
	}
}
