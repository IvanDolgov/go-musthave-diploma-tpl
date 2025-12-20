package accrual

import (
	"encoding/json"
	"net/http"

	"github.com/IvanDolgov/go-musthave-diploma-tpl/internal/logger"
)

// MonitorHandler создает HTTP handler для мониторинга воркера
func (w *Worker) MonitorHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		processed, failed := w.GetStats()

		stats := map[string]interface{}{
			"is_running":  w.isRunning,
			"processed":   processed,
			"failed":      failed,
			"concurrency": w.concurrency,
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(stats)
	}
}

// HealthCheck проверяет здоровье воркера
func (w *Worker) HealthCheck() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if !w.isRunning {
		logger.Log.Warn("Accrual worker is not running")
		return false
	}

	// Проверяем, что были обработаны заказы недавно
	// (можно добавить timestamp последней обработки)

	return true
}
