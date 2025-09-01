package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
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
