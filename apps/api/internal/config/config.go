package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DemoMode                                                                    bool
	DemoDataDir                                                                 string
	Env, HTTPAddr, DatabaseURL, AppBaseURL                                      string
	OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCRedirectURL                 string
	SessionTTL                                                                  time.Duration
	CookieSecure                                                                bool
	TextMaxLength                                                               int
	OpenAIModel                                                                 string
	VectorSearchEnabled                                                         bool
	OpenAIAPIKey                                                                string
	OpenAITimeout                                                               time.Duration
	WorkerConcurrency                                                           int
	DeletedImageRetention, IncompleteUploadRetention                            time.Duration
	S3Endpoint, S3PresignEndpoint, S3Region, S3Bucket, S3AccessKey, S3SecretKey string
	S3PathStyle                                                                 bool
	S3TTL                                                                       time.Duration
	UploadMaxBytes                                                              int64
	UploadAllowedMIMEs                                                          []string
}

func Load(envFile string) (Config, error) {
	if err := godotenv.Load(envFile); err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("cannot read environment file")
	}
	return Parse(os.Getenv)
}

func Parse(get func(string) string) (Config, error) {
	demo := false
	if raw := get("DEMO_MODE"); raw != "" {
		var err error
		demo, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("DEMO_MODE must be a boolean")
		}
	}
	if demo {
		original := get
		defaults := map[string]string{
			"APP_ENV": "development", "HTTP_ADDR": "127.0.0.1:8080",
			"DATABASE_URL": "postgres://memehub:change-me@localhost:5432/memehub?sslmode=disable",
			"APP_BASE_URL": "http://localhost:4321", "SESSION_COOKIE_SECURE": "false",
			"SESSION_TTL_SECONDS": "604800", "TEXT_MAX_LENGTH": "10000",
			"DEMO_DATA_DIR": "./data",
		}
		get = func(key string) string {
			switch key {
			case "OIDC_ISSUER_URL":
				return "http://localhost/demo"
			case "OIDC_CLIENT_ID", "OPENAI_MODEL":
				return "demo"
			case "OIDC_REDIRECT_URL":
				base := original("APP_BASE_URL")
				if base == "" {
					base = defaults["APP_BASE_URL"]
				}
				return strings.TrimRight(base, "/") + "/api/auth/callback"
			case "S3_ENDPOINT", "S3_PRESIGN_ENDPOINT", "OPENAI_API_KEY":
				return ""
			}
			if value := original(key); value != "" {
				return value
			}
			return defaults[key]
		}
		if get("APP_ENV") != "development" && get("APP_ENV") != "test" {
			return Config{}, fmt.Errorf("DEMO_MODE is only allowed in development or test")
		}
	}
	c := Config{Env: get("APP_ENV"), HTTPAddr: get("HTTP_ADDR"), DatabaseURL: get("DATABASE_URL"), AppBaseURL: get("APP_BASE_URL"), OIDCIssuer: get("OIDC_ISSUER_URL"), OIDCClientID: get("OIDC_CLIENT_ID"), OIDCClientSecret: get("OIDC_CLIENT_SECRET"), OIDCRedirectURL: get("OIDC_REDIRECT_URL"), OpenAIModel: get("OPENAI_MODEL")}
	c.DemoMode = demo
	c.DemoDataDir = get("DEMO_DATA_DIR")
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return c, fmt.Errorf("HTTP_ADDR must be a host:port address")
	}
	dsn, err := url.Parse(c.DatabaseURL)
	if err != nil || dsn == nil || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") || dsn.Hostname() == "" || dsn.Path == "" {
		return c, fmt.Errorf("DATABASE_URL must be a PostgreSQL URL")
	}
	for _, item := range []struct{ name, value string }{{"APP_BASE_URL", c.AppBaseURL}, {"OIDC_ISSUER_URL", c.OIDCIssuer}, {"OIDC_REDIRECT_URL", c.OIDCRedirectURL}} {
		u, err := url.Parse(item.value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return c, fmt.Errorf("%s must be an HTTP URL without credentials, query, or fragment", item.name)
		}
		if c.Env == "production" && u.Scheme != "https" {
			return c, fmt.Errorf("%s requires HTTPS in production", item.name)
		}
		if item.name == "APP_BASE_URL" && u.Path != "" && u.Path != "/" {
			return c, fmt.Errorf("APP_BASE_URL must be an origin URL")
		}
	}
	base, _ := url.Parse(c.AppBaseURL)
	callback, _ := url.Parse(c.OIDCRedirectURL)
	if base.Scheme != callback.Scheme || base.Host != callback.Host || callback.Path != "/api/auth/callback" {
		return c, fmt.Errorf("OIDC_REDIRECT_URL must use APP_BASE_URL with /api/auth/callback")
	}
	c.AppBaseURL = base.Scheme + "://" + base.Host
	if c.OIDCClientID == "" {
		return c, fmt.Errorf("OIDC_CLIENT_ID is required")
	}
	if c.OpenAIModel == "" {
		return c, fmt.Errorf("OPENAI_MODEL is required to queue analysis jobs")
	}
	c.OpenAIAPIKey = get("OPENAI_API_KEY")
	if raw := get("VECTOR_SEARCH_ENABLED"); raw != "" {
		c.VectorSearchEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("VECTOR_SEARCH_ENABLED must be a boolean")
		}
	}
	if demo {
		c.VectorSearchEnabled = false
	}
	if c.VectorSearchEnabled && c.OpenAIAPIKey == "" {
		return c, fmt.Errorf("VECTOR_SEARCH_ENABLED requires OPENAI_API_KEY")
	}
	c.OpenAITimeout = 120 * time.Second
	if raw := get("OPENAI_TIMEOUT_SECONDS"); raw != "" {
		seconds, e := strconv.Atoi(raw)
		if e != nil || seconds < 1 || seconds > 600 {
			return c, fmt.Errorf("OPENAI_TIMEOUT_SECONDS must be between 1 and 600")
		}
		c.OpenAITimeout = time.Duration(seconds) * time.Second
	}
	c.WorkerConcurrency = 2
	c.DeletedImageRetention = 7 * 24 * time.Hour
	c.IncompleteUploadRetention = 24 * time.Hour
	for _, item := range []struct {
		key      string
		min, max int
		unit     time.Duration
		target   *time.Duration
	}{
		{"DELETED_IMAGE_RETENTION_DAYS", 1, 365, 24 * time.Hour, &c.DeletedImageRetention},
		{"INCOMPLETE_UPLOAD_RETENTION_HOURS", 2, 168, time.Hour, &c.IncompleteUploadRetention},
	} {
		if raw := get(item.key); raw != "" {
			value, e := strconv.Atoi(raw)
			if e != nil || value < item.min || value > item.max {
				return c, fmt.Errorf("invalid %s", item.key)
			}
			*item.target = time.Duration(value) * item.unit
		}
	}
	if raw := get("WORKER_CONCURRENCY"); raw != "" {
		c.WorkerConcurrency, err = strconv.Atoi(raw)
		if err != nil || c.WorkerConcurrency < 1 || c.WorkerConcurrency > 16 {
			return c, fmt.Errorf("WORKER_CONCURRENCY must be between 1 and 16")
		}
	}
	secure, err := strconv.ParseBool(get("SESSION_COOKIE_SECURE"))
	if err != nil {
		return c, fmt.Errorf("SESSION_COOKIE_SECURE must be a boolean")
	}
	if c.Env == "production" && !secure {
		return c, fmt.Errorf("SESSION_COOKIE_SECURE must be true in production")
	}
	c.CookieSecure = secure
	ttl, err := strconv.Atoi(get("SESSION_TTL_SECONDS"))
	if err != nil || ttl <= 0 || ttl > 2592000 {
		return c, fmt.Errorf("SESSION_TTL_SECONDS must be between 1 and 2592000")
	}
	c.SessionTTL = time.Duration(ttl) * time.Second
	c.TextMaxLength, err = strconv.Atoi(get("TEXT_MAX_LENGTH"))
	if err != nil || c.TextMaxLength <= 0 || c.TextMaxLength > 100000 {
		return c, fmt.Errorf("TEXT_MAX_LENGTH must be between 1 and 100000")
	}
	c.UploadMaxBytes = 10 << 20
	if raw := get("UPLOAD_MAX_BYTES"); raw != "" {
		c.UploadMaxBytes, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || c.UploadMaxBytes < 1 || c.UploadMaxBytes > 50<<20 {
			return c, fmt.Errorf("UPLOAD_MAX_BYTES must be between 1 and 52428800")
		}
	}
	c.UploadAllowedMIMEs = []string{"image/jpeg", "image/png", "image/webp", "image/gif"}
	if raw := get("UPLOAD_ALLOWED_MIME_TYPES"); raw != "" {
		c.UploadAllowedMIMEs = strings.Split(raw, ",")
	}
	for _, value := range c.UploadAllowedMIMEs {
		if value != "image/jpeg" && value != "image/png" && value != "image/webp" && value != "image/gif" {
			return c, fmt.Errorf("unsupported UPLOAD_ALLOWED_MIME_TYPES")
		}
	}
	c.S3Endpoint, c.S3PresignEndpoint = get("S3_ENDPOINT"), get("S3_PRESIGN_ENDPOINT")
	c.S3Region, c.S3Bucket = get("S3_REGION"), get("S3_BUCKET")
	c.S3AccessKey, c.S3SecretKey = get("S3_ACCESS_KEY_ID"), get("S3_SECRET_ACCESS_KEY")
	c.S3TTL = 15 * time.Minute
	if raw := get("S3_PRESIGN_TTL_SECONDS"); raw != "" {
		seconds, e := strconv.Atoi(raw)
		if e != nil || seconds < 1 || seconds > 3600 {
			return c, fmt.Errorf("S3_PRESIGN_TTL_SECONDS must be between 1 and 3600")
		}
		c.S3TTL = time.Duration(seconds) * time.Second
	}
	if c.S3Endpoint != "" {
		if c.S3PresignEndpoint == "" {
			c.S3PresignEndpoint = c.S3Endpoint
		}
		for _, endpoint := range []string{c.S3Endpoint, c.S3PresignEndpoint} {
			u, e := url.Parse(endpoint)
			if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (c.Env == "production" && u.Scheme != "https") {
				return c, fmt.Errorf("invalid S3 endpoint")
			}
		}
		if c.S3Region == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
			return c, fmt.Errorf("S3 region, bucket, and credentials are required")
		}
		c.S3PathStyle, err = strconv.ParseBool(get("S3_FORCE_PATH_STYLE"))
		if err != nil {
			return c, fmt.Errorf("S3_FORCE_PATH_STYLE must be a boolean")
		}
	}
	return c, nil
}
