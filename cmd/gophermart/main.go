package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/accrual"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/auth"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/config"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/middleware"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage/postgres"
	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
)

// run запускает приложение с переданной конфигурацией
func run(cfg models.Config) error {
	var dbStorage storage.DatabaseStorage

	// Создаем корневой контекст
	ctx := context.Background()

	if cfg.DatabaseDSN == "" {
		// ВОЗВРАЩАЕМ ОШИБКУ, ЕСЛИ СТРОКА ПОДКЛЮЧЕНИЯ НЕ ЗАДАНА
		return fmt.Errorf("database connection string (DatabaseDSN) is required")
	}

	logger.Log.Info("Using PostgreSQL storage", zap.String("dsn", cfg.DatabaseDSN))

	pgStorage, err := postgres.NewPostgresStorage(ctx, cfg.DatabaseDSN)
	if err != nil {
		// ВОЗВРАЩАЕМ ОШИБКУ ПРИ НЕУДАЧНОЙ ИНИЦИАЛИЗАЦИИ
		return fmt.Errorf("failed to initialize PostgreSQL storage: %w", err)
	}
	store := pgStorage
	dbStorage = pgStorage
	defer pgStorage.Close()

	// Создаем JWTManager с секретным ключом из конфигурации
	// Используем ключ из конфигурации или дефолтный
	jwtSecret := cfg.Key
	if jwtSecret == "" {
		jwtSecret = "your-secret-key-change-in-production"
		logger.Log.Warn("Using default JWT secret key, set -k flag for production")
	}

	jwtManager := auth.NewJWTManager(jwtSecret)
	logger.Log.Info("JWT manager initialized")

	// Создаем хендлеры аутентификации
	authHandlers := NewAuthHandlers(store, jwtManager)

	// Создаем App для обработки всех хендлеров
	app := NewApp(store, jwtManager, &cfg)

	// В функции run:
	if cfg.AccrualSystemAddress != "" {
		workerConfig := accrual.Config{
			AccrualAddress: cfg.AccrualSystemAddress,
			PollInterval:   1 * time.Second, // Уменьшаем интервал для тестов
			RequestTimeout: 5 * time.Second,
		}

		worker := accrual.NewWorker(store, workerConfig)
		worker.Start()
		defer worker.Stop()

		logger.Log.Info("Accrual worker started",
			zap.String("address", cfg.AccrualSystemAddress),
			zap.Duration("poll_interval", workerConfig.PollInterval))
	} else {
		logger.Log.Warn("Accrual system address not configured, worker not started")
	}

	// создаем строку с сервером
	fullPathServer := buildServerAddress(cfg.Server, cfg.Port)

	// создаем роутер
	router := chi.NewRouter()

	// Middleware
	router.Use(middleware.WithLogging)
	router.Use(middleware.WithGzip)

	// Middleware для проверки хеша ДО обработки тела запроса
	router.Use(middleware.HashValidation(cfg.Key))

	// Middleware для добавления хеша в исходящие ответы
	router.Use(middleware.HashResponse(cfg.Key))

	// Middleware аутентификации
	router.Use(middleware.NewAuthMiddleware(jwtManager))

	// Регистрация маршрутов
	router.Post(`/api/user/register`, authHandlers.registrationUsers())
	router.Post(`/api/user/register/`, authHandlers.registrationUsers())

	router.Post(`/api/user/login`, authHandlers.authUsers())
	router.Post(`/api/user/login/`, authHandlers.authUsers())

	// Новые маршруты для работы с заказами и балансом
	router.Post(`/api/user/orders`, app.OrderHandler)
	router.Post(`/api/user/orders/`, app.OrderHandler)

	router.Get(`/api/user/orders`, app.GetOrdersHandler)
	router.Get(`/api/user/orders/`, app.GetOrdersHandler)

	router.Get(`/api/user/balance`, app.GetBalanceHandler)
	router.Get(`/api/user/balance/`, app.GetBalanceHandler)

	router.Post(`/api/user/balance/withdraw`, app.WithdrawHandler)
	router.Post(`/api/user/balance/withdraw/`, app.WithdrawHandler)

	router.Get(`/api/user/withdrawals`, app.GetWithdrawalsHandler)
	router.Get(`/api/user/withdrawals/`, app.GetWithdrawalsHandler)

	// HTTP сервер
	server := &http.Server{
		Addr:         fullPathServer,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	serverErr := make(chan error, 1)

	// Запускаем сервер
	go func() {
		storageType := "postgres"

		logger.Log.Info("Starting server",
			zap.String("address", fullPathServer),
			zap.String("storage_type", storageType),
			zap.Bool("database_enabled", dbStorage != nil),
		)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Ожидаем сигналы завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	select {
	case sig := <-sigChan:
		logger.Log.Info("Received signal, shutting down gracefully",
			zap.String("signal", sig.String()))

	case err := <-serverErr:
		logger.Log.Error("Server error", zap.Error(err))
		return err
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logger.Log.Info("Shutting down server...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("Server shutdown error", zap.Error(err))
		return err
	}

	logger.Log.Info("Server stopped gracefully")
	return nil
}

func main() {
	cfg := config.ParseServerFlags()

	if err := logger.Initialize("info"); err != nil {
		fmt.Fprintf(os.Stderr, "Logger initialization error: %v\n", err)
		os.Exit(1)
	}
	defer logger.Log.Sync()

	if err := run(cfg); err != nil {
		logger.Log.Info("Application error", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Application error: %v\n", err)
		os.Exit(1)
	}
}

func buildServerAddress(server, port string) string {
	if strings.TrimSpace(server) == "" {
		return ":" + port
	}
	return server + ":" + port
}
