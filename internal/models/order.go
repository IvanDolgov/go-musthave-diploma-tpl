package models

import (
	"time"
)

// Order представляет заказ пользователя
type Order struct {
	ID          int        `json:"-"`
	UserID      int        `json:"-"`
	Number      string     `json:"number"`
	Status      string     `json:"status"`
	Accrual     *float64   `json:"accrual,omitempty"`
	UploadedAt  time.Time  `json:"uploaded_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// Balance представляет баланс пользователя
type Balance struct {
	UserID    int     `json:"-"`
	Current   float64 `json:"current"`
	Withdrawn float64 `json:"withdrawn"`
}

// Withdrawal представляет списание средств
type Withdrawal struct {
	ID          int       `json:"-"`
	UserID      int       `json:"-"`
	OrderNumber string    `json:"order"`
	Sum         float64   `json:"sum"`
	ProcessedAt time.Time `json:"processed_at"`
}

// AccrualResponse представляет ответ от системы accrual
type AccrualResponse struct {
	Order   string   `json:"order"`
	Status  string   `json:"status"`
	Accrual *float64 `json:"accrual,omitempty"`
}

// WithdrawRequest представляет запрос на списание средств
type WithdrawRequest struct {
	Order string  `json:"order"`
	Sum   float64 `json:"sum"`
}
