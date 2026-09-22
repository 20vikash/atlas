// Command atlas-wg-mesh installs the Atlas WG Mesh dataplane on one host and manages its BPF state.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

var rootCommand = &cobra.Command{
	Use:               "atlas-wg-mesh",
	Short:             "connect VMs across hosts with eBPF and WireGuard",
	PersistentPreRunE: requireRoot,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	SilenceUsage:      true,
	SilenceErrors:     true,
}

func main() {
	if err := rootCommand.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "atlas-wg-mesh:", err)
		os.Exit(1)
	}
}

func requireRoot(command *cobra.Command, _ []string) error {
	if command.Name() != "version" && os.Geteuid() != 0 {
		return errors.New("run this command as root")
	}

	return nil
}
