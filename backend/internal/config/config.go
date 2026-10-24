package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	AppEnv           string
	AppVersion       string
	Port             string
	DatabaseURL      string
	PostgresHost     string
	PostgresPort     string
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string
	PostgresSSLMode  string
	PostgresMaxConns int32
	PostgresMinConns int32
	CORSAllowOrigins string
}

// Load reads environment variables and populates Config with sensible defaults.
func Load() Config {
	host := getEnv("POSTGRES_HOST", "localhost")
	port := getEnv("POSTGRES_PORT", "5432")
	user := getEnv("POSTGRES_USER", "ddo_user")
	pass := getEnv("POSTGRES_PASSWORD", "ddo_secret_password_2026")
	db := getEnv("POSTGRES_DB", "ddo_db")
	sslMode := getEnv("POSTGRES_SSLMODE", "disable")

	defaultDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, sslMode)

	return Config{
		AppEnv:           getEnv("APP_ENV", "production"),
		AppVersion:       getEnv("APP_VERSION", "1.0.0"),
		Port:             getEnv("BACKEND_PORT", "8080"),
		DatabaseURL:      getEnv("DATABASE_URL", defaultDSN),
		PostgresHost:     host,
		PostgresPort:     port,
		PostgresUser:     user,
		PostgresPassword: pass,
		PostgresDB:       db,
		PostgresSSLMode:  sslMode,
		PostgresMaxConns: int32(getEnvInt("POSTGRES_MAX_CONNS", 25)),
		PostgresMinConns: int32(getEnvInt("POSTGRES_MIN_CONNS", 5)),
		CORSAllowOrigins: getEnv("CORS_ALLOW_ORIGINS", "*"),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	v, err := strconv.Atoi(valStr)
	if err != nil {
		return fallback
	}
	return v
}

