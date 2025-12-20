package storage

import (
	"context"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
)

// UserStorage интерфейс для работы с пользователями
type UserStorage interface {
	CreateUser(ctx context.Context, login, hashedPassword string) error
	GetUserByLogin(ctx context.Context, login string) (models.User, error)
	GetUserByID(ctx context.Context, id int) (models.User, error)
	UserExists(ctx context.Context, login string) (bool, error)
}

// OrderStorage интерфейс для работы с заказами
type OrderStorage interface {
	CreateOrder(ctx context.Context, userID int, orderNumber string) error
	GetOrderByNumber(ctx context.Context, number string) (*models.Order, error)
	GetOrdersByUserID(ctx context.Context, userID int) ([]models.Order, error)
	UpdateOrderAccrual(ctx context.Context, number string, status string, accrual *float64) error
}

// BalanceStorage интерфейс для работы с балансом
type BalanceStorage interface {
	GetBalance(ctx context.Context, userID int) (*models.Balance, error)
	AddAccrualToBalance(ctx context.Context, userID int, accrual float64) error
	CreateWithdrawal(ctx context.Context, userID int, orderNumber string, sum float64) error
	GetWithdrawalsByUserID(ctx context.Context, userID int) ([]models.Withdrawal, error)
}

// DatabaseStorage интерфейс для проверки подключения к БД
type DatabaseStorage interface {
	Ping(ctx context.Context) error
	Close() error
}

// Storage интерфейс объединяющий все возможности хранилища
type Storage interface {
	DatabaseStorage
	UserStorage
	OrderStorage
	BalanceStorage
	// Здесь можно добавить другие интерфейсы по мере необходимости
}
