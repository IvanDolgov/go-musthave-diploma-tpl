package middleware

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/hash"
	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
)

// HashValidation middleware проверяет хеш входящих запросов
func HashValidation(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if key != "" {
				// Читаем тело запроса
				body, err := io.ReadAll(r.Body)
				if err != nil {
					logger.Log.Error("Failed to read request body for hash validation")
					http.Error(w, "Bad request", http.StatusBadRequest)
					return
				}

				// Восстанавливаем тело для дальнейшей обработки
				r.Body = io.NopCloser(bytes.NewBuffer(body))

				// Получаем хеш из заголовка
				receivedHash := r.Header.Get("HashSHA256")

				// Проверяем хеш
				if !hash.VerifyHMACSHA256(body, receivedHash, key) {
					logger.Log.Warn("Hash validation failed")
					http.Error(w, "Bad request", http.StatusBadRequest)
					return
				}

				logger.Log.Debug("Hash validation successful")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// HashResponse middleware добавляет хеш к исходящим ответам
func HashResponse(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Используем буферизированный writer
			h := &bufferedHashWriter{
				ResponseWriter: w,
				key:            key,
				buffer:         &bytes.Buffer{},
				headers:        make(http.Header),
				statusCode:     200, // Значение по умолчанию
			}

			// Запускаем обработку
			next.ServeHTTP(h, r)

			// Вычисляем и добавляем хеш
			if h.buffer.Len() > 0 {
				hashValue := hash.ComputeHMACSHA256(h.buffer.Bytes(), h.key)
				if hashValue != "" {
					h.headers.Set("HashSHA256", hashValue)
				}
			}

			// Копируем все заголовки в оригинальный writer
			for k, v := range h.headers {
				w.Header()[k] = v
			}

			// Отправляем статус код
			w.WriteHeader(h.statusCode)

			// Отправляем данные
			h.buffer.WriteTo(w)
		})
	}
}

// bufferedHashWriter полностью буферизирует ответ перед отправкой
type bufferedHashWriter struct {
	http.ResponseWriter
	key        string
	buffer     *bytes.Buffer
	headers    http.Header
	statusCode int
}

func (h *bufferedHashWriter) Header() http.Header {
	return h.headers
}

func (h *bufferedHashWriter) Write(b []byte) (int, error) {
	return h.buffer.Write(b)
}

func (h *bufferedHashWriter) WriteHeader(statusCode int) {
	h.statusCode = statusCode
}

// Hijack поддерживает WebSocket и другие протоколы
func (h *bufferedHashWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// Для хижака не используем буферизацию
	if hj, ok := h.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
