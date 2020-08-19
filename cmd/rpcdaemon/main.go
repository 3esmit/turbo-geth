package main

import (
	"github.com/ledgerwatch/turbo-geth/cmd/utils"
	"os"

	"github.com/ledgerwatch/turbo-geth/cmd/rpcdaemon/cli"
	"github.com/ledgerwatch/turbo-geth/cmd/rpcdaemon/commands"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/spf13/cobra"
)

func main() {
	cmd, cfg := cli.RootCommand()
	if err := utils.SetupCobra(cmd); err != nil {
		panic(err)
	}
	defer utils.StopDebug()

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		db, txPool, err := cli.OpenDB(*cfg)
		if err != nil {
			log.Error("Could not connect to remoteDb", "error", err)
			return nil
		}

		var rpcAPI = commands.APIList(db, txPool, *cfg, nil)
		cli.StartRpcServer(cmd.Context(), *cfg, rpcAPI)
		return nil
	}

	if err := cmd.ExecuteContext(utils.RootContext()); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
