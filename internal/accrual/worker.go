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
	logger         *zap.Logger
	stopChan       chan struct{}
	wg             sync.WaitGroup
	mu             sync.RWMutex
	isRunning      bool
}

// Config конфигурация для воркера
type Config struct {
	AccrualAddress string        // Адрес системы accrual
	PollInterval   time.Duration // Интервал опроса
	Concurrency    int           // Количество параллельных горутин
	RequestTimeout time.Duration // Таймаут запросов к accrual
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
	)

	w.wg.Add(w.concurrency)
	for i := 0; i < w.concurrency; i++ {
		go w.processOrders(i)
	}

	w.wg.Add(1)
	go w.pollOrders()
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

	w.logger.Info("Stopping accrual worker")
	close(w.stopChan)
	w.wg.Wait()
	w.logger.Info("Accrual worker stopped")
}

// pollOrders периодически запускает обработку заказов
func (w *Worker) pollOrders() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.processBatch()
		}
	}
}

// processBatch обрабатывает партию заказов
func (w *Worker) processBatch() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Получаем заказы для обработки
	orders, err := w.storage.GetOrdersForProcessing(ctx, w.concurrency*10)
	if err != nil {
		w.logger.Error("Failed to get orders for processing", zap.Error(err))
		return
	}

	if len(orders) == 0 {
		return
	}

	w.logger.Debug("Processing orders batch", zap.Int("count", len(orders)))

	// Отправляем заказы в канал для обработки
	ordersChan := make(chan models.Order, len(orders))
	for _, order := range orders {
		ordersChan <- order
	}
	close(ordersChan)

	// Обрабатываем заказы параллельно
	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for order := range ordersChan {
				w.processSingleOrder(ctx, order, workerID)
			}
		}(i)
	}
	wg.Wait()
}

// processOrders обработчик для отдельных горутин
func (w *Worker) processOrders(workerID int) {
	defer w.wg.Done()

	for {
		select {
		case <-w.stopChan:
			return
		default:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

			orders, err := w.storage.GetOrdersForProcessing(ctx, 1)
			cancel()

			if err != nil {
				w.logger.Error("Worker failed to get orders",
					zap.Int("worker_id", workerID),
					zap.Error(err))
				time.Sleep(w.pollInterval)
				continue
			}

			if len(orders) == 0 {
				time.Sleep(w.pollInterval)
				continue
			}

			ctx2, cancel2 := context.WithTimeout(context.Background(), w.requestTimeout)
			w.processSingleOrder(ctx2, orders[0], workerID)
			cancel2()
		}
	}
}

// processSingleOrder обрабатывает один заказ
func (w *Worker) processSingleOrder(ctx context.Context, order models.Order, workerID int) {
	accrualResp, err := w.checkAccrualStatus(ctx, order.Number)
	if err != nil {
		w.logger.Error("Failed to check accrual status",
			zap.Int("worker_id", workerID),
			zap.String("order_number", order.Number),
			zap.Error(err))
		return
	}

	// Обновляем статус заказа
	err = w.storage.UpdateOrderAccrual(ctx, order.Number, accrualResp.Status, accrualResp.Accrual)
	if err != nil {
		w.logger.Error("Failed to update order accrual",
			zap.Int("worker_id", workerID),
			zap.String("order_number", order.Number),
			zap.Error(err))
		return
	}

	// Если есть начисления, обновляем баланс
	if accrualResp.Status == "PROCESSED" && accrualResp.Accrual != nil && *accrualResp.Accrual > 0 {
		err = w.storage.AddAccrualToBalance(ctx, order.UserID, *accrualResp.Accrual)
		if err != nil {
			w.logger.Error("Failed to add accrual to balance",
				zap.Int("worker_id", workerID),
				zap.String("order_number", order.Number),
				zap.Int("user_id", order.UserID),
				zap.Float64("accrual", *accrualResp.Accrual),
				zap.Error(err))

			// Откатываем статус заказа для повторной обработки
			w.storage.UpdateOrderAccrual(ctx, order.Number, "PROCESSING", nil)
			return
		}

		w.logger.Info("Accrual added to balance",
			zap.Int("worker_id", workerID),
			zap.String("order_number", order.Number),
			zap.Int("user_id", order.UserID),
			zap.Float64("accrual", *accrualResp.Accrual))
	}

	w.logger.Debug("Order processed",
		zap.Int("worker_id", workerID),
		zap.String("order_number", order.Number),
		zap.String("status", accrualResp.Status))
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
		return &models.AccrualResponse{
			Order:  orderNumber,
			Status: "INVALID",
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
		return &models.AccrualResponse{
			Order:  orderNumber,
			Status: "INVALID",
		}, nil

	default:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}
