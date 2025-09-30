package server

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

	"github.com/mohammad-safakhou/newser/internal/runtime"
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

// Signup
//
//	@Summary		User signup
//	@Description	Create a new user account
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		AuthSignupRequest	true	"Signup payload"
//	@Success		201		{string}	string				"Created"
//	@Failure		400		{object}	HTTPError
//	@Failure		409		{object}	HTTPError
//	@Failure		500		{object}	HTTPError
//	@Router			/api/auth/signup [post]
func (a *AuthHandler) signup(c echo.Context) error {
	var req AuthSignupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := a.Store.CreateUser(c.Request().Context(), req.Email, string(hash)); err != nil {
		if pgErr, ok := err.(*pq.Error); ok && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "email already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusCreated)
}

// Login
//
//	@Summary		Login
//	@Description	Returns JWT in cookie and body; supports Bearer flows
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		AuthLoginRequest	true	"Login payload"
//	@Success		200		{object}	TokenResponse
//	@Failure		400		{object}	HTTPError
//	@Failure		401		{object}	HTTPError
//	@Failure		500		{object}	HTTPError
//	@Router			/api/auth/login [post]
func (a *AuthHandler) login(c echo.Context) error {
	var req AuthLoginRequest
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
	signed, err := runtime.SignJWT(id, a.Secret, 24*time.Hour)
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

// Logout
//
//	@Summary	Logout
//	@Tags		auth
//	@Produce	json
//	@Success	200	{string}	string	"OK"
//	@Router		/api/auth/logout [post]
func (a *AuthHandler) logout(c echo.Context) error {
	cookie := new(http.Cookie)
	cookie.Name = "auth"
	cookie.Value = ""
	cookie.Path = "/"
	cookie.MaxAge = -1
	c.SetCookie(cookie)
	return c.NoContent(http.StatusOK)
}

func initAuth(ctx context.Context, st *store.Store, jwtSecret []byte) (*AuthHandler, error) {
	if st == nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "store not initialized")
	}
	return &AuthHandler{Store: st, Secret: jwtSecret}, nil
}
