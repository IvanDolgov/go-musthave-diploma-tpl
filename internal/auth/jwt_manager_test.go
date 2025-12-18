package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func TestJWTManager(t *testing.T) {
	secretKey := "test-secret-key"
	manager := NewJWTManager(secretKey)

	userID := 123
	login := "testuser"

	// Тест создания токена
	token, err := manager.CreateToken(userID, login)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if token == "" {
		t.Error("CreateToken returned empty token")
	}

	// Тест проверки токена
	claims, err := manager.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("Expected UserID %d, got %d", userID, claims.UserID)
	}

	if claims.Login != login {
		t.Errorf("Expected Login %s, got %s", login, claims.Login)
	}

	// Тест с истекшим токеном
	oldManager := &JWTManager{secretKey: []byte(secretKey)}
	oldToken, _ := oldManager.createTokenWithExpiry(userID, login, time.Now().Add(-time.Hour))

	_, err = manager.VerifyToken(oldToken)
	if err == nil {
		t.Error("Expected error for expired token")
	}

	// Тест с неверным секретным ключом
	wrongManager := NewJWTManager("wrong-secret-key")
	_, err = wrongManager.VerifyToken(token)
	if err == nil {
		t.Error("Expected error for wrong secret key")
	}
}

// Вспомогательный метод для тестирования
func (m *JWTManager) createTokenWithExpiry(userID int, login string, expiry time.Time) (string, error) {
	claims := &Claims{
		UserID: userID,
		Login:  login,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   login,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secretKey)
}
