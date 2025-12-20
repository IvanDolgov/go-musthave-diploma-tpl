package config

import (
	"flag"
	"os"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
)

// ParseServerFlags парсит флаги командной строки для сервера
func ParseServerFlags() models.Config {
	var cfg models.Config

	flag.StringVar(&cfg.Address, "a", "localhost:8080", "HTTP server endpoint address")
	flag.StringVar(&cfg.Server, "s", "", "HTTP server host (optional, overrides -a host)")
	flag.StringVar(&cfg.Port, "p", "8080", "HTTP server port (optional, overrides -a port)")
	flag.StringVar(&cfg.DatabaseDSN, "d", "", "PostgreSQL database connection string")
	flag.StringVar(&cfg.Key, "k", "your-secret-key-change-in-production", "Secret key for hash/JWT")
	flag.Int64Var(&cfg.RateLimit, "l", 100, "Rate limit for requests per second")
	flag.StringVar(&cfg.AccrualSystemAddress, "r", "http://localhost:8081", "Accrual system address")

	// Парсим флаги
	flag.Parse()

	// Если установлены переменные окружения, переопределяем значения флагов
	if envAddr := os.Getenv("RUN_ADDRESS"); envAddr != "" {
		cfg.Address = envAddr
	}
	if envDSN := os.Getenv("DATABASE_URI"); envDSN != "" {
		cfg.DatabaseDSN = envDSN
	}
	if envKey := os.Getenv("SECRET_KEY"); envKey != "" {
		cfg.Key = envKey
	}
	if envAccrual := os.Getenv("ACCRUAL_SYSTEM_ADDRESS"); envAccrual != "" {
		cfg.AccrualSystemAddress = envAccrual
	}

	return cfg
}
