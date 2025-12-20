package accrual

import (
	"context"
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
	requestTimeout time.Duration
	logger         *zap.Logger
	stopChan       chan struct{}
	wg             sync.WaitGroup
	isRunning      bool
	processedCount atomic.Int64
}

// Config конфигурация для воркера
type Config struct {
	AccrualAddress string        // Адрес системы accrual
	PollInterval   time.Duration // Интервал опроса
	RequestTimeout time.Duration // Таймаут запросов к accrual
}

// NewWorker создает новый воркер
func NewWorker(storage storage.Storage, config Config) *Worker {
	if config.PollInterval == 0 {
		config.PollInterval = 1 * time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Second
	}

	return &Worker{
		storage:        storage,
		accrualAddress: strings.TrimSuffix(config.AccrualAddress, "/"),
		client: &http.Client{
			Timeout: config.RequestTimeout,
		},
		pollInterval:   config.PollInterval,
		requestTimeout: config.RequestTimeout,
		logger:         logger.Log,
		stopChan:       make(chan struct{}),
	}
}

// Start запускает воркер
func (w *Worker) Start() {
	if w.isRunning {
		return
	}
	w.isRunning = true

	w.logger.Info("Starting accrual worker",
		zap.String("accrual_address", w.accrualAddress),
		zap.Duration("poll_interval", w.pollInterval))

	w.wg.Add(1)
	go w.run()
}

// Stop останавливает воркер
func (w *Worker) Stop() {
	if !w.isRunning {
		return
	}
	w.isRunning = false

	w.logger.Info("Stopping accrual worker")
	close(w.stopChan)
	w.wg.Wait()
	w.logger.Info("Accrual worker stopped",
		zap.Int64("processed", w.processedCount.Load()))
}

// run основной цикл воркера
func (w *Worker) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.processOrders()
		}
	}
}

// processOrders обрабатывает заказы
func (w *Worker) processOrders() {
	ctx, cancel := context.WithTimeout(context.Background(), w.requestTimeout)
	defer cancel()

	// Получаем заказы для обработки
	orders, err := w.storage.GetOrdersForProcessing(ctx, 10)
	if err != nil {
		w.logger.Error("Failed to get orders for processing", zap.Error(err))
		return
	}

	if len(orders) == 0 {
		return
	}

	w.logger.Debug("Processing orders batch", zap.Int("count", len(orders)))

	for _, order := range orders {
		w.processSingleOrder(ctx, order)
	}
}

// processSingleOrder обрабатывает один заказ
func (w *Worker) processSingleOrder(ctx context.Context, order models.Order) {
	accrualResp, err := w.checkAccrualStatus(ctx, order.Number)
	if err != nil {
		w.logger.Error("Failed to check accrual status",
			zap.String("order_number", order.Number),
			zap.Error(err))
		return
	}

	// Проверяем, изменился ли статус
	if order.Status == accrualResp.Status &&
		(order.Accrual != nil && accrualResp.Accrual != nil && *order.Accrual == *accrualResp.Accrual) {
		w.logger.Debug("Order status unchanged, skipping",
			zap.String("order_number", order.Number))
		return
	}

	// Обновляем статус заказа
	err = w.storage.UpdateOrderAccrual(ctx, order.Number, accrualResp.Status, accrualResp.Accrual)
	if err != nil {
		w.logger.Error("Failed to update order accrual",
			zap.String("order_number", order.Number),
			zap.Error(err))
		return
	}

	// Если есть начисления, обновляем баланс
	if accrualResp.Status == "PROCESSED" && accrualResp.Accrual != nil && *accrualResp.Accrual > 0 {
		err = w.storage.AddAccrualToBalance(ctx, order.UserID, *accrualResp.Accrual)
		if err != nil {
			w.logger.Error("Failed to add accrual to balance",
				zap.String("order_number", order.Number),
				zap.Int("user_id", order.UserID),
				zap.Float64("accrual", *accrualResp.Accrual),
				zap.Error(err))
			return
		}

		w.logger.Info("Accrual added to balance",
			zap.String("order_number", order.Number),
			zap.Int("user_id", order.UserID),
			zap.Float64("accrual", *accrualResp.Accrual))

		w.processedCount.Add(1)
	}
}

// checkAccrualStatus проверяет статус заказа в системе accrual
func (w *Worker) checkAccrualStatus(ctx context.Context, orderNumber string) (*models.AccrualResponse, error) {
	url := fmt.Sprintf("%s/api/orders/%s", w.accrualAddress, orderNumber)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

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
			Accrual: &zeroAccrual,
		}, nil

	case http.StatusTooManyRequests:
		// Превышен лимит запросов
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				time.Sleep(time.Duration(seconds) * time.Second)
			}
		}
		return nil, fmt.Errorf("rate limit exceeded")

	case http.StatusNotFound:
		// Система accrual не знает о заказе
		zeroAccrual := float64(0)
		return &models.AccrualResponse{
			Order:   orderNumber,
			Status:  "INVALID",
			Accrual: &zeroAccrual,
		}, nil

	default:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}

// GetProcessedCount возвращает количество обработанных заказов
func (w *Worker) GetProcessedCount() int64 {
	return w.processedCount.Load()
}
