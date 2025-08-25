package server

import (
    "net/http"

    "github.com/labstack/echo/v4"
)

// registerDocs registers OpenAPI spec and docs UI endpoints.
func registerDocs(e *echo.Echo) {
    // Serve the OpenAPI yaml
    e.File("/api/openapi.yaml", "docs/openapi.yaml")

    // Simple ReDoc page
    e.GET("/api/docs", func(c echo.Context) error {
        html := `<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Newser API Docs</title>
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <style>body{margin:0;padding:0;} .redoc-wrap{height:100vh;}</style>
  </head>
  <body>
    <div id="redoc-container" class="redoc-wrap"></div>
    <script src="https://cdn.jsdelivr.net/npm/redoc/bundles/redoc.standalone.js"></script>
    <script>
      Redoc.init('/api/openapi.yaml', {}, document.getElementById('redoc-container'))
    </script>
  </body>
  </html>`
        return c.HTML(http.StatusOK, html)
    })
}


