package auth

import "context"

// Типы ключей для контекста
type contextKey string

// Константы для ключей контекста
const (
	UserIDKey    = contextKey("user_id")
	UserLoginKey = contextKey("user_login")
)

// GetUserIDFromContext возвращает ID пользователя из контекста
func GetUserIDFromContext(ctx context.Context) (int, bool) {
	userID := ctx.Value(UserIDKey)
	if userID == nil {
		return 0, false
	}

	id, ok := userID.(int)
	return id, ok
}

// GetUserLoginFromContext возвращает логин пользователя из контекста
func GetUserLoginFromContext(ctx context.Context) (string, bool) {
	userLogin := ctx.Value(UserLoginKey)
	if userLogin == nil {
		return "", false
	}

	login, ok := userLogin.(string)
	return login, ok
}

// SetUserToContext добавляет информацию о пользователе в контекст
func SetUserToContext(ctx context.Context, userID int, userLogin string) context.Context {
	ctx = context.WithValue(ctx, UserIDKey, userID)
	ctx = context.WithValue(ctx, UserLoginKey, userLogin)
	return ctx
}
