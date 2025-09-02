package server

import (
	"github.com/labstack/echo/v4"
	echoSwagger "github.com/swaggo/echo-swagger"

	// Blank-import the generated Swagger docs package. A minimal stub is provided
	// at internal/server/swagger so builds succeed before generation. After
	// running `make swagger`, this will register the spec at runtime.
	_ "github.com/mohammad-safakhou/newser/internal/server/swagger"
)

// registerDocs registers API documentation endpoints.
//
// - Swagger UI:   /api/swagger/index.html
func registerDocs(e *echo.Echo) {
	// Swagger UI backed by swag-generated docs
	e.GET("/api/swagger/*", echoSwagger.WrapHandler)
}
