package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/error/pgerrors"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/models"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/retry"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
)

// PostgresStorage реализация Storage для PostgreSQL
type PostgresStorage struct {
	db         *sql.DB
	classifier retry.ErrorClassifier
	logger     *zap.Logger
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
		logger:     logger.Log,
	}, nil
}

// Close закрывает подключение к базе данных
func (s *PostgresStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Ping проверяет подключение к базе данных
func (s *PostgresStorage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// CreateUser создает нового пользователя
func (s *PostgresStorage) CreateUser(ctx context.Context, login, hashedPassword string) error {
	query := `
		INSERT INTO users (login, password)
		VALUES ($1, $2)
		ON CONFLICT (login) DO NOTHING
	`

	result, err := s.db.ExecContext(ctx, query, login, hashedPassword)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("user with login '%s' already exists", login)
	}

	return nil
}

// GetUserByLogin возвращает пользователя по логину
func (s *PostgresStorage) GetUserByLogin(ctx context.Context, login string) (models.User, error) {
	query := `
		SELECT id, login, password, created_at, updated_at
		FROM users
		WHERE login = $1
	`

	var user models.User
	err := s.db.QueryRowContext(ctx, query, login).Scan(
		&user.ID,
		&user.Login,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return models.User{}, fmt.Errorf("user not found: %w", err)
		}
		return models.User{}, fmt.Errorf("failed to get user by login: %w", err)
	}

	return user, nil
}

// GetUserByID возвращает пользователя по ID
func (s *PostgresStorage) GetUserByID(ctx context.Context, id int) (models.User, error) {
	query := `
		SELECT id, login, password, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	var user models.User
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Login,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return models.User{}, fmt.Errorf("user not found: %w", err)
		}
		return models.User{}, fmt.Errorf("failed to get user by id: %w", err)
	}

	return user, nil
}

// UserExists проверяет существование пользователя
func (s *PostgresStorage) UserExists(ctx context.Context, login string) (bool, error) {
	query := `
		SELECT EXISTS(SELECT 1 FROM users WHERE login = $1)
	`

	var exists bool
	err := s.db.QueryRowContext(ctx, query, login).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if user exists: %w", err)
	}

	return exists, nil
}

// CreateOrder создает новый заказ
func (s *PostgresStorage) CreateOrder(ctx context.Context, userID int, orderNumber string) error {
	query := `
		INSERT INTO orders (user_id, number, status)
		VALUES ($1, $2, 'NEW')
		ON CONFLICT (number) DO NOTHING
	`

	result, err := s.db.ExecContext(ctx, query, userID, orderNumber)
	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("order with number '%s' already exists", orderNumber)
	}

	return nil
}

// UpdateOrderAccrual обновляет статус и начисления заказа
func (s *PostgresStorage) UpdateOrderAccrual(ctx context.Context, number string, status string, accrual *float64) error {
	query := `
		UPDATE orders
		SET status = $1, accrual = $2, processed_at = $3, updated_at = CURRENT_TIMESTAMP
		WHERE number = $4
	`

	var accrualValue interface{}
	var processedAt interface{}

	if accrual != nil {
		accrualValue = *accrual
		processedAt = time.Now()
	} else {
		accrualValue = nil
		processedAt = nil
	}

	_, err := s.db.ExecContext(ctx, query, status, accrualValue, processedAt, number)
	if err != nil {
		return fmt.Errorf("failed to update order accrual: %w", err)
	}

	return nil
}

// GetOrderByNumber возвращает заказ по номеру
func (s *PostgresStorage) GetOrderByNumber(ctx context.Context, number string) (*models.Order, error) {
	query := `
		SELECT id, user_id, number, status, accrual, uploaded_at, processed_at
		FROM orders
		WHERE number = $1
	`

	var order models.Order
	var accrual sql.NullFloat64
	var processedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, number).Scan(
		&order.ID,
		&order.UserID,
		&order.Number,
		&order.Status,
		&accrual,
		&order.UploadedAt,
		&processedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get order by number: %w", err)
	}

	if accrual.Valid {
		accrualValue := accrual.Float64
		order.Accrual = &accrualValue
	}

	if processedAt.Valid {
		pt := processedAt.Time
		order.ProcessedAt = &pt
	}

	return &order, nil
}

// GetOrdersByUserID возвращает все заказы пользователя
func (s *PostgresStorage) GetOrdersByUserID(ctx context.Context, userID int) ([]models.Order, error) {
	query := `
		SELECT number, status, accrual, uploaded_at, processed_at
		FROM orders
		WHERE user_id = $1
		ORDER BY uploaded_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders by user id: %w", err)
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var order models.Order
		var accrual sql.NullFloat64
		var processedAt sql.NullTime

		err := rows.Scan(
			&order.Number,
			&order.Status,
			&accrual,
			&order.UploadedAt,
			&processedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}

		if accrual.Valid {
			accrualValue := accrual.Float64
			order.Accrual = &accrualValue
		}

		if processedAt.Valid {
			pt := processedAt.Time
			order.ProcessedAt = &pt
		}

		orders = append(orders, order)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return orders, nil
}

// GetBalance возвращает баланс пользователя
func (s *PostgresStorage) GetBalance(ctx context.Context, userID int) (*models.Balance, error) {
	// Используем CTE для гарантированного получения баланса
	query := `
        WITH upsert_balance AS (
            INSERT INTO balances (user_id, current, withdrawn)
            VALUES ($1, 0, 0)
            ON CONFLICT (user_id) DO NOTHING
            RETURNING user_id, current, withdrawn
        )
        SELECT user_id, current, withdrawn
        FROM upsert_balance
        UNION ALL
        SELECT user_id, current, withdrawn
        FROM balances
        WHERE user_id = $1
        AND NOT EXISTS (SELECT 1 FROM upsert_balance)
        LIMIT 1
    `

	var balance models.Balance
	balance.UserID = userID

	var current, withdrawn sql.NullFloat64
	err := s.db.QueryRowContext(ctx, query, userID).Scan(
		&balance.UserID,
		&current,
		&withdrawn,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Возвращаем нулевой баланс, если пользователь существует
			return &models.Balance{
				UserID:    userID,
				Current:   0,
				Withdrawn: 0,
			}, nil
		}
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	if current.Valid {
		balance.Current = current.Float64
	}
	if withdrawn.Valid {
		balance.Withdrawn = withdrawn.Float64
	}

	return &balance, nil
}

// AddAccrualToBalance добавляет начисления к балансу
func (s *PostgresStorage) AddAccrualToBalance(ctx context.Context, userID int, accrual float64) error {
	s.logger.Info("Adding accrual to balance",
		zap.Int("user_id", userID),
		zap.Float64("accrual", accrual))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logger.Error("Failed to begin transaction",
			zap.Int("user_id", userID),
			zap.Error(err))
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Убедимся, что запись баланса существует
	_, err = tx.ExecContext(ctx, `
        INSERT INTO balances (user_id, current, withdrawn)
        VALUES ($1, 0, 0)
        ON CONFLICT (user_id) DO NOTHING
    `, userID)
	if err != nil {
		s.logger.Error("Failed to ensure balance record exists",
			zap.Int("user_id", userID),
			zap.Error(err))
		return fmt.Errorf("failed to ensure balance record: %w", err)
	}

	// Обновляем баланс (добавляем к current)
	result, err := tx.ExecContext(ctx, `
        UPDATE balances
        SET current = current + $1, updated_at = CURRENT_TIMESTAMP
        WHERE user_id = $2
    `, accrual, userID)

	if err != nil {
		s.logger.Error("Failed to update balance",
			zap.Int("user_id", userID),
			zap.Error(err))
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	s.logger.Debug("Balance updated",
		zap.Int("user_id", userID),
		zap.Int64("rows_affected", rowsAffected))

	// Создаем транзакцию (положительная сумма - приход)
	referenceID := fmt.Sprintf("accrual_%d_%d", userID, time.Now().UnixNano())

	_, err = tx.ExecContext(ctx, `
        INSERT INTO transactions (
            user_id, type, amount, description, reference_id, status, processed_at
        ) VALUES ($1, 'ACCRUAL', $2, $3, $4, 'COMPLETED', CURRENT_TIMESTAMP)
    `, userID, accrual, "Accrual from order processing", referenceID)

	if err != nil {
		s.logger.Error("Failed to create transaction",
			zap.Int("user_id", userID),
			zap.Error(err))
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	s.logger.Debug("Committing transaction",
		zap.Int("user_id", userID))

	if err := tx.Commit(); err != nil {
		s.logger.Error("Failed to commit transaction",
			zap.Int("user_id", userID),
			zap.Error(err))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.logger.Info("Successfully added accrual to balance",
		zap.Int("user_id", userID),
		zap.Float64("accrual", accrual))

	return nil
}

// CreateWithdrawal создает списание через транзакцию
func (s *PostgresStorage) CreateWithdrawal(ctx context.Context, userID int, orderNumber string, sum float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Убедимся, что запись баланса существует
	_, err = tx.ExecContext(ctx, `
        INSERT INTO balances (user_id, current, withdrawn)
        VALUES ($1, 0, 0)
        ON CONFLICT (user_id) DO NOTHING
    `, userID)
	if err != nil {
		return fmt.Errorf("failed to ensure balance record: %w", err)
	}

	// Проверяем, достаточно ли средств (current >= sum)
	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT current FROM balances WHERE user_id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		return fmt.Errorf("failed to get balance for update: %w", err)
	}

	if currentBalance < sum {
		return fmt.Errorf("insufficient funds")
	}

	// Проверяем, не использовался ли уже этот номер заказа для списания
	var existingWithdrawal int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM withdrawals WHERE order_number = $1", orderNumber).Scan(&existingWithdrawal)
	if err == nil {
		return fmt.Errorf("order number already used for withdrawal")
	}

	// Создаем запись о списании
	_, err = tx.ExecContext(ctx, `
        INSERT INTO withdrawals (user_id, order_number, sum)
        VALUES ($1, $2, $3)
    `, userID, orderNumber, sum)

	if err != nil {
		return fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Обновляем баланс: уменьшаем current, увеличиваем withdrawn
	_, err = tx.ExecContext(ctx, `
        UPDATE balances
        SET current = current - $1, 
            withdrawn = withdrawn + $1, 
            updated_at = CURRENT_TIMESTAMP
        WHERE user_id = $2
    `, sum, userID)

	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Создаем транзакцию (отрицательная сумма - расход)
	referenceID := fmt.Sprintf("withdraw_%s_%d", orderNumber, time.Now().UnixNano())
	negativeSum := -sum
	_, err = tx.ExecContext(ctx, `
        INSERT INTO transactions (
            user_id, type, amount, description, order_number, reference_id, status, processed_at
        ) VALUES ($1, 'WITHDRAW', $2, $3, $4, $5, 'COMPLETED', CURRENT_TIMESTAMP)
    `, userID, negativeSum, "Withdrawal for order payment", orderNumber, referenceID)

	if err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	return tx.Commit()
}

// GetWithdrawalsByUserID возвращает списания пользователя
func (s *PostgresStorage) GetWithdrawalsByUserID(ctx context.Context, userID int) ([]models.Withdrawal, error) {
	query := `
		SELECT order_number, sum, processed_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY processed_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get withdrawals: %w", err)
	}
	defer rows.Close()

	var withdrawals []models.Withdrawal
	for rows.Next() {
		var withdrawal models.Withdrawal
		withdrawal.UserID = userID

		err := rows.Scan(
			&withdrawal.OrderNumber,
			&withdrawal.Sum,
			&withdrawal.ProcessedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan withdrawal: %w", err)
		}

		withdrawals = append(withdrawals, withdrawal)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return withdrawals, nil
}

// GetOrdersForProcessing возвращает заказы для обработки в accrual системе
func (s *PostgresStorage) GetOrdersForProcessing(ctx context.Context, limit int) ([]models.Order, error) {
	query := `
		SELECT id, user_id, number, status, accrual, uploaded_at, processed_at
		FROM orders
		WHERE status IN ('NEW', 'PROCESSING')
		ORDER BY uploaded_at ASC
		LIMIT $1
	`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders for processing: %w", err)
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var order models.Order
		var accrual sql.NullFloat64
		var processedAt sql.NullTime

		err := rows.Scan(
			&order.ID,
			&order.UserID,
			&order.Number,
			&order.Status,
			&accrual,
			&order.UploadedAt,
			&processedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}

		if accrual.Valid {
			accrualValue := accrual.Float64
			order.Accrual = &accrualValue
		}

		if processedAt.Valid {
			pt := processedAt.Time
			order.ProcessedAt = &pt
		}

		orders = append(orders, order)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return orders, nil
}

// CreateTransaction создает новую транзакцию
func (s *PostgresStorage) CreateTransaction(ctx context.Context, tx *models.Transaction) error {
	query := `
		INSERT INTO transactions (
			user_id, type, amount, description, order_number, reference_id, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`

	err := s.db.QueryRowContext(ctx, query,
		tx.UserID,
		tx.Type,
		tx.Amount,
		tx.Description,
		tx.OrderNumber,
		tx.ReferenceID,
		tx.Status,
	).Scan(&tx.ID, &tx.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	return nil
}

// GetUserTransactions возвращает транзакции пользователя
func (s *PostgresStorage) GetUserTransactions(ctx context.Context, userID int, limit, offset int) ([]models.Transaction, error) {
	query := `
		SELECT id, user_id, type, amount, description, order_number, reference_id, status, created_at, processed_at
		FROM transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get user transactions: %w", err)
	}
	defer rows.Close()

	var transactions []models.Transaction
	for rows.Next() {
		var tx models.Transaction
		var orderNumber, referenceID sql.NullString
		var processedAt sql.NullTime

		err := rows.Scan(
			&tx.ID,
			&tx.UserID,
			&tx.Type,
			&tx.Amount,
			&tx.Description,
			&orderNumber,
			&referenceID,
			&tx.Status,
			&tx.CreatedAt,
			&processedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}

		if orderNumber.Valid {
			tx.OrderNumber = &orderNumber.String
		}

		if referenceID.Valid {
			tx.ReferenceID = &referenceID.String
		}

		if processedAt.Valid {
			pt := processedAt.Time
			tx.ProcessedAt = &pt
		}

		transactions = append(transactions, tx)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return transactions, nil
}

// GetTransactionByReference возвращает транзакцию по reference ID
func (s *PostgresStorage) GetTransactionByReference(ctx context.Context, referenceID string) (*models.Transaction, error) {
	query := `
		SELECT id, user_id, type, amount, description, order_number, reference_id, status, created_at, processed_at
		FROM transactions
		WHERE reference_id = $1
	`

	var tx models.Transaction
	var orderNumber, refID sql.NullString
	var processedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, referenceID).Scan(
		&tx.ID,
		&tx.UserID,
		&tx.Type,
		&tx.Amount,
		&tx.Description,
		&orderNumber,
		&refID,
		&tx.Status,
		&tx.CreatedAt,
		&processedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get transaction by reference: %w", err)
	}

	if orderNumber.Valid {
		tx.OrderNumber = &orderNumber.String
	}

	if refID.Valid {
		tx.ReferenceID = &refID.String
	}

	if processedAt.Valid {
		pt := processedAt.Time
		tx.ProcessedAt = &pt
	}

	return &tx, nil
}

// GetAdvisoryLock получает advisory lock
func (s *PostgresStorage) GetAdvisoryLock(ctx context.Context, key int64) (bool, error) {
	var lockObtained bool
	err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&lockObtained)
	if err != nil {
		return false, fmt.Errorf("failed to get advisory lock: %w", err)
	}
	return lockObtained, nil
}

// ReleaseAdvisoryLock освобождает advisory lock
func (s *PostgresStorage) ReleaseAdvisoryLock(ctx context.Context, key int64) error {
	_, err := s.db.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key)
	if err != nil {
		return fmt.Errorf("failed to release advisory lock: %w", err)
	}
	return nil
}

// GetDB возвращает соединение с БД (для recovery)
func (s *PostgresStorage) GetDB() *sql.DB {
	return s.db
}
