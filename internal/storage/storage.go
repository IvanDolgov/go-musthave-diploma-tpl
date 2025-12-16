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

// DatabaseStorage интерфейс для проверки подключения к БД
type DatabaseStorage interface {
    Ping(ctx context.Context) error
    Close() error
}

// Storage интерфейс объединяющий все возможности хранилища
type Storage interface {
    DatabaseStorage
    UserStorage
    // Здесь можно добавить другие интерфейсы по мере необходимости
}
