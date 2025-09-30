package runtime

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
)

// LoadJWTSecret resolves the shared JWT secret from config.
// Preference order: server.jwt_secret, general.jwt_secret, required env NEWSER_JWT_SECRET.
func LoadJWTSecret(cfg *config.Config) ([]byte, error) {
    if cfg == nil {
        return nil, errors.New("config is nil")
    }
    if cfg.Server.JWTSecret != "" {
        return []byte(cfg.Server.JWTSecret), nil
    }
    if cfg.General.JWTSecret != "" {
        return []byte(cfg.General.JWTSecret), nil
    }
    return nil, errors.New("jwt secret not configured (server.jwt_secret or general.jwt_secret)")
}

// SignJWT issues a signed token with the provided subject and TTL.
func SignJWT(subject string, secret []byte, ttl time.Duration) (string, error) {
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "sub": subject,
        "exp": time.Now().Add(ttl).Unix(),
    })
    return token.SignedString(secret)
}

// EchoAuthMiddleware builds an Echo middleware that validates JWT tokens from Authorization header or auth cookie.
func EchoAuthMiddleware(secret []byte) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tok := extractToken(c)
			if tok == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing token")
			}
			parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return secret, nil })
			if err != nil || !parsed.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
			if claims, ok := parsed.Claims.(jwt.MapClaims); ok {
				if sub, ok := claims["sub"].(string); ok {
					c.Set("user_id", sub)
					req := c.Request().WithContext(context.WithValue(c.Request().Context(), subjectKey{}, sub))
					c.SetRequest(req)
					return next(c)
				}
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}
	}
}

func extractToken(c echo.Context) string {
    if h := c.Request().Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
        return h[7:]
    }
    if ck, err := c.Cookie("auth"); err == nil {
        return ck.Value
    }
    return ""
}

// ContextWithSubject helper extracts subject from context.
func ContextWithSubject(ctx context.Context, subject string) context.Context {
    return context.WithValue(ctx, subjectKey{}, subject)
}

type subjectKey struct{}

// SubjectFromContext returns the JWT subject if stored in context via middleware.
func SubjectFromContext(ctx context.Context) (string, bool) {
    if ctx == nil {
        return "", false
    }
    if v := ctx.Value(subjectKey{}); v != nil {
        if s, ok := v.(string); ok {
            return s, true
        }
    }
    return "", false
}
