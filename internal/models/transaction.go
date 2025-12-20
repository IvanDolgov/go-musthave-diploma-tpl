package models

import "time"

// TransactionType тип транзакции
type TransactionType string

const (
	TransactionTypeAccrual  TransactionType = "ACCRUAL"
	TransactionTypeWithdraw TransactionType = "WITHDRAW"
	TransactionTypeRefund   TransactionType = "REFUND"
)

// Transaction представляет финансовую транзакцию
type Transaction struct {
	ID          int             `json:"-"`
	UserID      int             `json:"user_id"`
	Type        TransactionType `json:"type"`
	Amount      float64         `json:"amount"`
	Description string          `json:"description,omitempty"`
	OrderNumber *string         `json:"order_number,omitempty"`
	ReferenceID *string         `json:"reference_id,omitempty"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	ProcessedAt *time.Time      `json:"processed_at,omitempty"`
}

// TransactionRequest запрос на создание транзакции
type TransactionRequest struct {
	UserID      int             `json:"user_id"`
	Type        TransactionType `json:"type"`
	Amount      float64         `json:"amount"`
	Description string          `json:"description,omitempty"`
	OrderNumber string          `json:"order_number,omitempty"`
}
