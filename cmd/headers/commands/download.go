package commands

import (
	"github.com/ledgerwatch/turbo-geth/cmd/headers/download"
	"github.com/spf13/cobra"
)

var (
	bufferSize int // Size of buffer in MiB
)

func init() {
	downloadCmd.Flags().StringVar(&filesDir, "filesdir", "", "path to directory where files will be stored")
	downloadCmd.Flags().IntVar(&bufferSize, "buffersize", 512, "size o the buffer in MiB")
	downloadCmd.Flags().StringVar(&sentryAddr, "sentryAddr", "localhost:9091", "sentry address <host>:<port>")
	downloadCmd.Flags().StringVar(&coreAddr, "coreAddr", "localhost:9092", "core address <host>:<port>")
	rootCmd.AddCommand(downloadCmd)
}

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download headers backwards",
	RunE: func(cmd *cobra.Command, args []string) error {
		return download.Download(filesDir, bufferSize, sentryAddr, coreAddr)
	},
}
