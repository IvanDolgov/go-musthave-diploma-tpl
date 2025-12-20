package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/auth"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage"
	"go.uber.org/zap"
)

// App представляет основное приложение
type App struct {
	storage storage.Storage
	jwt     *auth.JWTManager
	logger  *zap.Logger
	config  *models.Config
}

// NewApp создает новый экземпляр App
func NewApp(storage storage.Storage, jwt *auth.JWTManager, config *models.Config) *App {
	return &App{
		storage: storage,
		jwt:     jwt,
		logger:  logger.Log,
		config:  config,
	}
}

// isValidLuhn проверяет номер заказа с помощью алгоритма Луна
func isValidLuhn(number string) bool {
	sum := 0
	isSecond := false

	// Проходим по цифрам справа налево
	for i := len(number) - 1; i >= 0; i-- {
		digit := int(number[i] - '0')

		if digit < 0 || digit > 9 {
			return false // Не цифра
		}

		if isSecond {
			digit = digit * 2
			if digit > 9 {
				digit = digit - 9
			}
		}

		sum += digit
		isSecond = !isSecond
	}

	return sum%10 == 0
}

// getCurrentUserID получает ID текущего пользователя из контекста
func (a *App) getCurrentUserID(r *http.Request) (int, bool) {
	return auth.GetUserIDFromContext(r.Context())
}

// // getCurrentUserLogin получает логин текущего пользователя из контекста
// func (a *App) getCurrentUserLogin(r *http.Request) (string, bool) {
// 	return auth.GetUserLoginFromContext(r.Context())
// }

// OrderHandler обработчик для загрузки номера заказа
func (a *App) OrderHandler(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID пользователя
	userID, ok := a.getCurrentUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Читаем номер заказа
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	orderNumber := strings.TrimSpace(string(body))
	if orderNumber == "" {
		http.Error(w, "Empty order number", http.StatusBadRequest)
		return
	}

	// Проверяем номер заказа с помощью алгоритма Луна
	if !isValidLuhn(orderNumber) {
		http.Error(w, "Invalid order number", http.StatusUnprocessableEntity)
		return
	}

	// Проверяем, существует ли уже такой заказ
	existingOrder, err := a.storage.GetOrderByNumber(r.Context(), orderNumber)
	if err != nil {
		a.logger.Error("Failed to check existing order", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if existingOrder != nil {
		if existingOrder.UserID == userID {
			// Заказ уже загружен этим пользователем
			w.WriteHeader(http.StatusOK)
			return
		} else {
			// Заказ уже загружен другим пользователем
			http.Error(w, "Order already uploaded by another user", http.StatusConflict)
			return
		}
	}

	// Создаем новый заказ
	err = a.storage.CreateOrder(r.Context(), userID, orderNumber)
	if err != nil {
		a.logger.Error("Failed to create order", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Запускаем асинхронную проверку статуса заказа
	go a.processOrderAsync(orderNumber)

	// Возвращаем успешный ответ
	w.WriteHeader(http.StatusAccepted)
}

// GetOrdersHandler возвращает список заказов пользователя
func (a *App) GetOrdersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID пользователя
	userID, ok := a.getCurrentUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем заказы пользователя
	orders, err := a.storage.GetOrdersByUserID(r.Context(), userID)
	if err != nil {
		a.logger.Error("Failed to get user orders", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Формируем ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Используем json.NewEncoder для сериализации
	if err := json.NewEncoder(w).Encode(orders); err != nil {
		a.logger.Error("Failed to encode orders", zap.Error(err))
	}
}

// GetBalanceHandler возвращает баланс пользователя
func (a *App) GetBalanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID пользователя
	userID, ok := a.getCurrentUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем баланс
	balance, err := a.storage.GetBalance(r.Context(), userID)
	if err != nil {
		a.logger.Error("Failed to get user balance", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Формируем ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(balance); err != nil {
		a.logger.Error("Failed to encode balance", zap.Error(err))
	}
}

// WithdrawHandler обрабатывает списание средств
func (a *App) WithdrawHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID пользователя
	userID, ok := a.getCurrentUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Декодируем запрос
	var withdrawReq models.WithdrawRequest
	if err := json.NewDecoder(r.Body).Decode(&withdrawReq); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Проверяем номер заказа
	if !isValidLuhn(withdrawReq.Order) {
		http.Error(w, "Invalid order number", http.StatusUnprocessableEntity)
		return
	}

	// Проверяем сумму
	if withdrawReq.Sum <= 0 {
		http.Error(w, "Invalid sum", http.StatusBadRequest)
		return
	}

	// Создаем списание
	err := a.storage.CreateWithdrawal(r.Context(), userID, withdrawReq.Order, withdrawReq.Sum)
	if err != nil {
		if strings.Contains(err.Error(), "insufficient funds") {
			http.Error(w, "Insufficient funds", http.StatusPaymentRequired)
			return
		}

		if strings.Contains(err.Error(), "unique constraint") ||
			strings.Contains(err.Error(), "already used") {
			http.Error(w, "Order number already used for withdrawal", http.StatusConflict)
			return
		}

		a.logger.Error("Failed to create withdrawal", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetWithdrawalsHandler возвращает историю списаний
func (a *App) GetWithdrawalsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем ID пользователя
	userID, ok := a.getCurrentUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем списания
	withdrawals, err := a.storage.GetWithdrawalsByUserID(r.Context(), userID)
	if err != nil {
		a.logger.Error("Failed to get user withdrawals", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(withdrawals) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Формируем ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(withdrawals); err != nil {
		a.logger.Error("Failed to encode withdrawals", zap.Error(err))
	}
}

// processOrderAsync асинхронно обрабатывает заказ в системе accrual
func (a *App) processOrderAsync(orderNumber string) {
	ctx := context.Background()

	// Используем экспоненциальный бекофф для повторных попыток
	maxAttempts := 100
	delay := time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Проверяем статус заказа в системе accrual
		err := a.checkOrderStatus(ctx, orderNumber)
		if err == nil {
			// Успешно обработано
			a.logger.Info("Order processed successfully",
				zap.String("order_number", orderNumber))
			return
		}

		// Если заказ еще в обработке, продолжаем опрос
		if strings.Contains(err.Error(), "order still processing") {
			a.logger.Debug("Order still processing, will retry",
				zap.String("order_number", orderNumber),
				zap.Int("attempt", attempt))

			// Увеличиваем задержку экспоненциально
			time.Sleep(delay)
			delay = time.Duration(float64(delay) * 1.5)
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			continue
		}

		// Если произошла другая ошибка, логируем и выходим
		a.logger.Error("Failed to check order status",
			zap.String("order_number", orderNumber),
			zap.Error(err),
			zap.Int("attempt", attempt))
		return
	}

	a.logger.Error("Max attempts reached for order processing",
		zap.String("order_number", orderNumber),
		zap.Int("max_attempts", maxAttempts))
}

// checkOrderStatus проверяет статус заказа в системе accrual
func (a *App) checkOrderStatus(ctx context.Context, orderNumber string) error {
	if a.config.AccrualSystemAddress == "" {
		return fmt.Errorf("accrual system address not configured")
	}

	url := fmt.Sprintf("%s/api/orders/%s", a.config.AccrualSystemAddress, orderNumber)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var accrualResp models.AccrualResponse
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		if err := json.Unmarshal(body, &accrualResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		// Обновляем статус заказа в базе данных
		err = a.storage.UpdateOrderAccrual(ctx, orderNumber, accrualResp.Status, accrualResp.Accrual)
		if err != nil {
			return fmt.Errorf("failed to update order accrual: %w", err)
		}

		// Если есть начисления, обновляем баланс
		if accrualResp.Status == "PROCESSED" && accrualResp.Accrual != nil && *accrualResp.Accrual > 0 {
			// Получаем userID по номеру заказа
			order, err := a.storage.GetOrderByNumber(ctx, orderNumber)
			if err != nil || order == nil {
				return fmt.Errorf("failed to get order: %w", err)
			}

			err = a.storage.AddAccrualToBalance(ctx, order.UserID, *accrualResp.Accrual)
			if err != nil {
				return fmt.Errorf("failed to add accrual to balance: %w", err)
			}
		}

		// Если заказ еще в обработке, возвращаем ошибку для продолжения опроса
		if accrualResp.Status == "PROCESSING" || accrualResp.Status == "REGISTERED" {
			return fmt.Errorf("order still processing")
		}

		return nil

	case http.StatusNoContent:
		// Заказ не найден в системе accrual
		err := a.storage.UpdateOrderAccrual(ctx, orderNumber, "INVALID", nil)
		if err != nil {
			return fmt.Errorf("failed to mark order as invalid: %w", err)
		}
		return nil

	case http.StatusTooManyRequests:
		// Превышен лимит запросов, нужно подождать
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				time.Sleep(time.Duration(seconds) * time.Second)
			}
		}
		return fmt.Errorf("rate limit exceeded")

	default:
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}
