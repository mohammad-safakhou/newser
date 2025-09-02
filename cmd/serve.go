package cmd

import (
	"github.com/mohammad-safakhou/newser/config"
	srv "github.com/mohammad-safakhou/newser/internal/server"
	"github.com/spf13/cobra"
)

func serveCMD() *cobra.Command {
	var serveAddr string
	var cfgPath string
	var serve = &cobra.Command{
		Use:   "serve",
		Short: "Run HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.LoadConfig(cfgPath)

			return srv.Run(cfg)
		},
	}
	serve.Flags().StringVar(&serveAddr, "addr", ":10001", "listen address")
	serve.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "config file (default is .)")

	return serve
}
