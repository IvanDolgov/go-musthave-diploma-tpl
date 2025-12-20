package auth

// JWTService интерфейс для работы с JWT токенами
type JWTService interface {
    CreateToken(userID int, login string) (string, error)
    VerifyToken(tokenString string) (*Claims, error)
}
