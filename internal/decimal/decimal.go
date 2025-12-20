package decimal

import (
	"fmt"
	"strconv"
	"strings"
)

// ToString конвертирует float64 в строку с 2 знаками после запятой
func ToString(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// FromString конвертирует строку в float64
func FromString(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// Format форматирует число как decimal с 2 знаками
func Format(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

// Parse парсит строку в float64
func Parse(s string) (float64, error) {
	s = strings.TrimSpace(s)
	return strconv.ParseFloat(s, 64)
}

// Add складывает два числа
func Add(a, b float64) float64 {
	return a + b
}

// Subtract вычитает b из a
func Subtract(a, b float64) float64 {
	return a - b
}

// IsGreater проверяет, больше ли a чем b
func IsGreater(a, b float64) bool {
	return a > b
}

// IsGreaterOrEqual проверяет, больше или равно ли a чем b
func IsGreaterOrEqual(a, b float64) bool {
	return a >= b
}
