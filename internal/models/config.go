package models

// Config содержит все параметры конфигурации приложения
type Config struct {
    Address     string // Адрес сервера (host:port)
    Server      string // Хост сервера (опционально)
    Port        string // Порт сервера (опционально)
    DatabaseDSN string // Строка подключения к PostgreSQL
    Key         string // Секретный ключ для хеширования и JWT
    RateLimit   int64  // Ограничение скорости запросов
}
