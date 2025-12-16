package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/error/pgerrors"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/retry"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStorage реализация Storage для PostgreSQL
type PostgresStorage struct {
	db         *sql.DB
	classifier retry.ErrorClassifier
}

// Проверка что PostgresStorage реализует все интерфейсы
var _ storage.Storage = (*PostgresStorage)(nil)

// NewPostgresStorage создает новое подключение к PostgreSQL
func NewPostgresStorage(ctx context.Context, connectionString string) (storage.Storage, error) {
	db, err := sql.Open("pgx", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Настраиваем пул соединений
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Проверяем подключение с использованием переданного контекста
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Применяем миграции с использованием переданного контекста
	if err := ApplyMigrations(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	return &PostgresStorage{
		db:         db,
		classifier: pgerrors.NewPostgresErrorClassifier(),
	}, nil
}

// // SaveToFile - для совместимости с интерфейсом
// func (s *PostgresStorage) SaveToFile(ctx context.Context, filename string) error {
//     return nil
// }

// // LoadFromFile - для совместимости с интерфейсом
// func (s *PostgresStorage) LoadFromFile(ctx context.Context, filename string) error {
//     return nil
// }

// Close закрывает соединение с БД
func (s *PostgresStorage) Close() error {
	return s.db.Close()
}

// Ping проверяет соединение с БД
func (s *PostgresStorage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// CreateUser создает нового пользователя
func (s *PostgresStorage) CreateUser(ctx context.Context, login, hashedPassword string) error {
	query := `
        INSERT INTO users (login, password, created_at, updated_at) 
        VALUES ($1, $2, $3, $4)
    `

	now := time.Now()
	_, err := s.db.ExecContext(ctx, query, login, hashedPassword, now, now)

	if err != nil {
		// Проверяем, является ли ошибка нарушением уникальности
		if strings.Contains(err.Error(), "duplicate key value") ||
			strings.Contains(err.Error(), "unique constraint") {
			return fmt.Errorf("user with login '%s' already exists", login)
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// GetUserByLogin возвращает пользователя по логину
func (s *PostgresStorage) GetUserByLogin(ctx context.Context, login string) (models.User, error) {
	var user models.User

	query := `
        SELECT id, login, password, created_at, updated_at 
        FROM users 
        WHERE login = $1
    `

	err := s.db.QueryRowContext(ctx, query, login).Scan(
		&user.ID,
		&user.Login,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return user, sql.ErrNoRows
		}
		return user, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// GetUserByID возвращает пользователя по ID
func (s *PostgresStorage) GetUserByID(ctx context.Context, id int) (models.User, error) {
	var user models.User

	query := `
        SELECT id, login, password, created_at, updated_at 
        FROM users 
        WHERE id = $1
    `

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Login,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		return user, fmt.Errorf("failed to get user by ID: %w", err)
	}

	return user, nil
}

// UserExists проверяет существование пользователя по логину
func (s *PostgresStorage) UserExists(ctx context.Context, login string) (bool, error) {
	var exists bool

	query := `
        SELECT EXISTS(
            SELECT 1 FROM users WHERE login = $1
        )
    `

	err := s.db.QueryRowContext(ctx, query, login).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check user existence: %w", err)
	}

	return exists, nil
}
