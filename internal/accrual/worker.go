package accrual

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage"
	"go.uber.org/zap"
)

// Worker представляет воркер для опроса системы accrual
type Worker struct {
	storage        storage.Storage
	accrualAddress string
	client         *http.Client
	pollInterval   time.Duration
	concurrency    int
	requestTimeout time.Duration
	maxRetries     int
	retryDelay     time.Duration
	logger         *zap.Logger
	stopChan       chan struct{}
	wg             sync.WaitGroup
	mu             sync.RWMutex
	isRunning      bool
	processedCount atomic.Int64
	failedCount    atomic.Int64
}

// Config конфигурация для воркера
type Config struct {
	AccrualAddress string        // Адрес системы accrual
	PollInterval   time.Duration // Интервал опроса
	Concurrency    int           // Количество параллельных горутин
	RequestTimeout time.Duration // Таймаут запросов к accrual
	MaxRetries     int           // Максимальное количество повторных попыток
	RetryDelay     time.Duration // Задержка между повторными попытками
}

// NewWorker создает новый воркер
func NewWorker(storage storage.Storage, config Config) *Worker {
	if config.PollInterval == 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.Concurrency == 0 {
		config.Concurrency = 10
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 10 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 1 * time.Second
	}

	return &Worker{
		storage:        storage,
		accrualAddress: strings.TrimSuffix(config.AccrualAddress, "/"),
		client: &http.Client{
			Timeout: config.RequestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        config.Concurrency,
				MaxIdleConnsPerHost: config.Concurrency,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		pollInterval:   config.PollInterval,
		concurrency:    config.Concurrency,
		requestTimeout: config.RequestTimeout,
		maxRetries:     config.MaxRetries,
		retryDelay:     config.RetryDelay,
		logger:         logger.Log,
		stopChan:       make(chan struct{}),
	}
}

// Start запускает воркер
func (w *Worker) Start() {
	w.mu.Lock()
	if w.isRunning {
		w.mu.Unlock()
		return
	}
	w.isRunning = true
	w.mu.Unlock()

	w.logger.Info("Starting accrual worker",
		zap.String("accrual_address", w.accrualAddress),
		zap.Duration("poll_interval", w.pollInterval),
		zap.Int("concurrency", w.concurrency),
		zap.Int("max_retries", w.maxRetries),
	)

	// Сначала обрабатываем заказы, которые могли остаться в обработке после сбоя
	w.recoverProcessingOrders()

	// Запускаем воркеры
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.worker(i)
	}

	w.logger.Info("Accrual worker started successfully")
}

// Stop останавливает воркер
func (w *Worker) Stop() {
	w.mu.Lock()
	if !w.isRunning {
		w.mu.Unlock()
		return
	}
	w.isRunning = false
	w.mu.Unlock()

	w.logger.Info("Stopping accrual worker...")
	close(w.stopChan)
	w.wg.Wait()

	w.logger.Info("Accrual worker stopped",
		zap.Int64("processed", w.processedCount.Load()),
		zap.Int64("failed", w.failedCount.Load()))
}

// recoverProcessingOrders восстанавливает заказы в статусе PROCESSING
func (w *Worker) recoverProcessingOrders() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Получаем заказы в статусе PROCESSING (возможно зависли после сбоя)
	orders, err := w.getProcessingOrders(ctx)
	if err != nil {
		w.logger.Error("Failed to recover processing orders", zap.Error(err))
		return
	}

	if len(orders) > 0 {
		w.logger.Info("Recovering processing orders", zap.Int("count", len(orders)))

		for _, order := range orders {
			// Сбрасываем статус на NEW для повторной обработки
			err := w.storage.UpdateOrderAccrual(ctx, order.Number, "NEW", nil)
			if err != nil {
				w.logger.Error("Failed to reset order status",
					zap.String("order_number", order.Number),
					zap.Error(err))
			} else {
				w.logger.Debug("Order status reset to NEW",
					zap.String("order_number", order.Number))
			}
		}
	}
}

// getProcessingOrders возвращает заказы в статусе PROCESSING
func (w *Worker) getProcessingOrders(ctx context.Context) ([]models.Order, error) {
	// Используем GetOrdersForProcessing с фильтром по статусу
	// Сначала получим все заказы для обработки
	allOrders, err := w.storage.GetOrdersForProcessing(ctx, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders for processing: %w", err)
	}

	// Отфильтруем только PROCESSING
	var processingOrders []models.Order
	for _, order := range allOrders {
		if order.Status == "PROCESSING" {
			processingOrders = append(processingOrders, order)
		}
	}

	return processingOrders, nil
}

// getOrderForProcessing получает заказ для обработки с блокировкой
func (w *Worker) getOrderForProcessing(workerID int) (*models.Order, error) {
	ctx, cancel := context.WithTimeout(context.Background(), w.requestTimeout)
	defer cancel()

	// Используем advisory lock для предотвращения одновременной обработки
	// одним и тем же воркером разных инстансов
	lockKey := int64(workerID + 1000) // Уникальный ключ для воркера

	// Пытаемся получить блокировку
	lockObtained, err := w.storage.(interface {
		GetAdvisoryLock(ctx context.Context, key int64) (bool, error)
	}).GetAdvisoryLock(ctx, lockKey)

	if err != nil || !lockObtained {
		return nil, fmt.Errorf("failed to obtain lock")
	}
	defer w.storage.(interface {
		ReleaseAdvisoryLock(ctx context.Context, key int64) error
	}).ReleaseAdvisoryLock(ctx, lockKey)

	// Используем существующий метод GetOrdersForProcessing
	orders, err := w.storage.GetOrdersForProcessing(ctx, 1)
	if err != nil {
		return nil, err
	}

	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}

	// Обновляем статус на PROCESSING
	err = w.storage.UpdateOrderAccrual(ctx, orders[0].Number, "PROCESSING", nil)
	if err != nil {
		return nil, err
	}

	return &orders[0], nil
}

// processSingleOrder обрабатывает один заказ
func (w *Worker) processSingleOrder(ctx context.Context, order *models.Order, workerID int, attempt int) error {
	accrualResp, err := w.checkAccrualStatus(ctx, order.Number)
	if err != nil {
		return fmt.Errorf("failed to check accrual status: %w", err)
	}

	// Обновляем статус заказа
	err = w.storage.UpdateOrderAccrual(ctx, order.Number, accrualResp.Status, accrualResp.Accrual)
	if err != nil {
		return fmt.Errorf("failed to update order accrual: %w", err)
	}

	// Если есть начисления, обновляем баланс
	if accrualResp.Status == "PROCESSED" && accrualResp.Accrual != nil && *accrualResp.Accrual > 0 {
		err = w.storage.AddAccrualToBalance(ctx, order.UserID, *accrualResp.Accrual)
		if err != nil {
			// Если не удалось обновить баланс, откатываем статус
			w.storage.UpdateOrderAccrual(ctx, order.Number, "PROCESSING", nil)
			return fmt.Errorf("failed to add accrual to balance: %w", err)
		}

		w.logger.Info("Accrual added to balance",
			zap.Int("worker_id", workerID),
			zap.String("order_number", order.Number),
			zap.Int("user_id", order.UserID),
			zap.Float64("accrual", *accrualResp.Accrual),
			zap.Int("attempt", attempt))
	}

	return nil
}

// worker основной воркер
func (w *Worker) worker(id int) {
	defer w.wg.Done()

	w.logger.Debug("Worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-w.stopChan:
			w.logger.Debug("Worker stopping", zap.Int("worker_id", id))
			return
		default:
			// Получаем заказ для обработки с блокировкой
			order, err := w.getOrderForProcessing(id)
			if err != nil {
				if err != sql.ErrNoRows {
					w.logger.Error("Worker failed to get order",
						zap.Int("worker_id", id),
						zap.Error(err))
				}
				// Если нет заказов, ждем перед следующей попыткой
				time.Sleep(w.pollInterval)
				continue
			}

			if order == nil {
				// Нет заказов для обработки
				time.Sleep(w.pollInterval)
				continue
			}

			// Обрабатываем заказ с повторными попытками
			w.processOrderWithRetry(id, order)
		}
	}
}

// processOrderWithRetry обрабатывает заказ с повторными попытками
func (w *Worker) processOrderWithRetry(workerID int, order *models.Order) {
	var lastErr error

	for attempt := 1; attempt <= w.maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), w.requestTimeout)

		err := w.processSingleOrder(ctx, order, workerID, attempt)
		cancel()

		if err == nil {
			// Успешно обработано
			w.processedCount.Add(1)
			w.logger.Debug("Order processed successfully",
				zap.Int("worker_id", workerID),
				zap.String("order_number", order.Number),
				zap.Int("attempt", attempt))
			return
		}

		lastErr = err

		// Если это временная ошибка (например, сеть), пробуем еще раз
		if w.isTemporaryError(err) && attempt < w.maxRetries {
			w.logger.Warn("Temporary error, retrying",
				zap.Int("worker_id", workerID),
				zap.String("order_number", order.Number),
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", w.maxRetries),
				zap.Error(err))

			// Экспоненциальный бекофф
			delay := w.retryDelay * time.Duration(1<<(attempt-1))
			time.Sleep(delay)
			continue
		}

		// Если это не временная ошибка, выходим
		break
	}

	// Все попытки провалились
	w.failedCount.Add(1)
	w.logger.Error("Failed to process order after all attempts",
		zap.Int("worker_id", workerID),
		zap.String("order_number", order.Number),
		zap.Int("max_attempts", w.maxRetries),
		zap.Error(lastErr))

	// Обновляем статус заказа на INVALID для дальнейшего анализа
	ctx, cancel := context.WithTimeout(context.Background(), w.requestTimeout)
	defer cancel()

	err := w.storage.UpdateOrderAccrual(ctx, order.Number, "INVALID", nil)
	if err != nil {
		w.logger.Error("Failed to mark order as invalid",
			zap.String("order_number", order.Number),
			zap.Error(err))
	}
}

// isTemporaryError проверяет, является ли ошибка временной
func (w *Worker) isTemporaryError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Временные ошибки: сетевые, таймауты, 429 (Too Many Requests)
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit")
}

// checkAccrualStatus проверяет статус заказа в системе accrual
func (w *Worker) checkAccrualStatus(ctx context.Context, orderNumber string) (*models.AccrualResponse, error) {
	url := fmt.Sprintf("%s/api/orders/%s", w.accrualAddress, orderNumber)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Gophermart-Accrual-Worker/1.0")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var accrualResp models.AccrualResponse
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if err := json.Unmarshal(body, &accrualResp); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		return &accrualResp, nil

	case http.StatusNoContent:
		// Заказ не найден в системе accrual
		zeroAccrual := float64(0)
		return &models.AccrualResponse{
			Order:   orderNumber,
			Status:  "INVALID",
			Accrual: &zeroAccrual, // Указатель на 0
		}, nil

	case http.StatusNotFound:
		// Система accrual не знает о заказе
		zeroAccrual := float64(0)
		return &models.AccrualResponse{
			Order:   orderNumber,
			Status:  "INVALID",
			Accrual: &zeroAccrual, // Указатель на 0
		}, nil

	case http.StatusTooManyRequests:
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				time.Sleep(time.Duration(seconds) * time.Second)
			}
		}
		return nil, fmt.Errorf("rate limit exceeded (429)")

	default:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}

// GetStats возвращает статистику работы воркера
func (w *Worker) GetStats() (processed, failed int64) {
	return w.processedCount.Load(), w.failedCount.Load()
}
