package server

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"

	"github.com/mohammad-safakhou/newser/internal/store"
)

type AuthHandler struct {
	Store  *store.Store
	Secret []byte
}

func (a *AuthHandler) Register(g *echo.Group) {
	g.POST("/signup", a.signup)
	g.POST("/login", a.login)
	g.POST("/logout", a.logout)
}

func (a *AuthHandler) signup(c echo.Context) error {
	var req struct{ Email, Password string }
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := a.Store.CreateUser(c.Request().Context(), req.Email, string(hash)); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.NoContent(http.StatusCreated)
}

// naive in-memory limiter per process
var lastLoginAttempt = time.Time{}

func (a *AuthHandler) login(c echo.Context) error {
	// basic rate limit: at most 1 attempt per 300ms per instance
	if time.Since(lastLoginAttempt) < 300*time.Millisecond {
		return echo.NewHTTPError(http.StatusTooManyRequests, "too many requests")
	}
	lastLoginAttempt = time.Now()

	var req struct{ Email, Password string }
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if len(req.Password) < 8 {
		return echo.NewHTTPError(http.StatusBadRequest, "password too short")
	}
	id, hash, err := a.Store.GetUserByEmail(c.Request().Context(), req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": id,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(a.Secret)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	cookie := new(http.Cookie)
	cookie.Name = "auth"
	cookie.Value = signed
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	if os.Getenv("NEWSER_ENV") == "prod" {
		cookie.Secure = true
	}
	c.SetCookie(cookie)
	// also return token for Bearer flows
	c.Response().Header().Set("Authorization", "Bearer "+signed)
	return c.JSON(http.StatusOK, map[string]string{"token": signed})
}

func (a *AuthHandler) logout(c echo.Context) error {
	cookie := new(http.Cookie)
	cookie.Name = "auth"
	cookie.Value = ""
	cookie.Path = "/"
	cookie.MaxAge = -1
	c.SetCookie(cookie)
	return c.NoContent(http.StatusOK)
}

func withAuth(next echo.HandlerFunc, secret []byte) echo.HandlerFunc {
	return func(c echo.Context) error {
		// accept Bearer token or cookie
		var tok string
		if h := c.Request().Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
			tok = h[7:]
		}
		if tok == "" {
			ck, err := c.Cookie("auth")
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing auth cookie")
			}
			tok = ck.Value
		}
		token, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return secret, nil })
		if err != nil || !token.Valid {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
		}
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if sub, ok := claims["sub"].(string); ok {
				c.Set("user_id", sub)
				return next(c)
			}
		}
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
}

func initAuth(ctx context.Context, st *store.Store, jwtSecret []byte) (*AuthHandler, error) {
	if st == nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "store not initialized")
	}
	if len(jwtSecret) == 0 {
		jwtSecret = []byte("dev-secret-change-me")
	}
	return &AuthHandler{Store: st, Secret: jwtSecret}, nil
}
