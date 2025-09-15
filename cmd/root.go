// Package cmd bootstraps the CLI and HTTP server.
//
// Swagger general API metadata
//
//	@title						Newser API
//	@version					0.1.0
//	@description				API for Newser service
//	@schemes					http https
//	@BasePath					/
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//
//	@securityDefinitions.apikey	CookieAuth
//	@in							cookie
//	@name						auth
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

func Execute() {
	var root = &cobra.Command{Use: "newser"}

	root.AddCommand(serveCMD(), migrateCMD())
	_ = root.Execute()
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}
