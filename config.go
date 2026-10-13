package main

import (
	"crypto/rand"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration loaded from .env / environment.
type Config struct {
	Username      string
	Password      string
	Secret        []byte
	Port          string
	DBPath        string
	DefaultKeshkic float64 // предлог износа за месечни Кешкић
	CookieSecure  bool
	FallbackRates map[string]float64 // RSD per 1 unit of currency
}

func loadConfig() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("ℹ️  .env није пронађен, користим променљиве окружења")
	}

	cfg := Config{
		Username:     strings.TrimSpace(os.Getenv("KESHK_USERNAME")),
		Password:     os.Getenv("KESHK_PASSWORD"),
		Port:         env("PORT", "3000"),
		DBPath:       env("DB_PATH", "data/keshk.db"),
		DefaultKeshkic: envFloat("DEFAULT_KESHKIC", 5000),
		CookieSecure: strings.EqualFold(env("COOKIE_SECURE", "false"), "true"),
		FallbackRates: map[string]float64{
			"RSD": 1,
			"EUR": envFloat("FALLBACK_EUR_RSD", 117.2),
			"USD": envFloat("FALLBACK_USD_RSD", 100),
		},
	}

	if cfg.Username == "" || cfg.Password == "" {
		log.Fatal("❌ KESHK_USERNAME и KESHK_PASSWORD морају бити постављени у .env")
	}

	if s := os.Getenv("SESSION_SECRET"); s != "" {
		cfg.Secret = []byte(s)
	} else {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		cfg.Secret = b
		log.Println("⚠️  SESSION_SECRET није постављен — сесије ће се поништити при рестарту сервера")
	}

	return cfg
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	if err != nil || f <= 0 {
		log.Printf("⚠️  %s=%q није исправан број, користим %v", key, v, def)
		return def
	}
	return f
}
