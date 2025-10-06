package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
func SignJWT(subject string, secret []byte, ttl time.Duration, scopes ...string) (string, error) {
	claims := jwt.MapClaims{
		"sub": subject,
		"exp": time.Now().Add(ttl).Unix(),
	}
	if len(scopes) > 0 {
		claims["scopes"] = scopes
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
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
					reqCtx := context.WithValue(c.Request().Context(), subjectKey{}, sub)
					scopes := extractScopes(claims)
					if len(scopes) > 0 {
						reqCtx = context.WithValue(reqCtx, scopeKey{}, scopes)
						c.Set("scopes", scopes)
					}
					c.Set("user_id", sub)
					c.SetRequest(c.Request().WithContext(reqCtx))
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

type scopeKey struct{}

// ScopesFromContext returns scopes associated with the request context.
func ScopesFromContext(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	if v := ctx.Value(scopeKey{}); v != nil {
		if scopes, ok := v.([]string); ok {
			return scopes, true
		}
	}
	return nil, false
}

// RequireScopes ensures the caller token includes all required scopes.
func RequireScopes(required ...string) echo.MiddlewareFunc {
	reqSet := make([]string, 0, len(required))
	for _, scope := range required {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		reqSet = append(reqSet, scope)
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if len(reqSet) == 0 {
				return next(c)
			}
			existing := getScopesFromContext(c)
			for _, scope := range reqSet {
				if !containsScope(existing, scope) {
					return echo.NewHTTPError(http.StatusForbidden, "missing scope: "+scope)
				}
			}
			return next(c)
		}
	}
}

func extractScopes(claims jwt.MapClaims) []string {
	if raw, ok := claims["scopes"]; ok {
		return normaliseScopes(raw)
	}
	if raw, ok := claims["scope"]; ok {
		return normaliseScopes(raw)
	}
	return nil
}

func normaliseScopes(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Fields(v)
		out := make([]string, 0, len(parts))
		for _, s := range parts {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func getScopesFromContext(c echo.Context) []string {
	if c == nil {
		return nil
	}
	if raw := c.Get("scopes"); raw != nil {
		if scopes, ok := raw.([]string); ok {
			return scopes
		}
	}
	if scopes, ok := ScopesFromContext(c.Request().Context()); ok {
		return scopes
	}
	return nil
}

func containsScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}
