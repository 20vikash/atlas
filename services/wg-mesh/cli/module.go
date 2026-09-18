package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// The release build compiled the kernel module for the kernel that the hosts
// run. The CLI embeds it, so a host needs no compiler and no kernel headers.
//
//go:embed atlas-neigh.ko
var kernelModuleObject []byte

const (
	kernelModuleName        = "atlas_neigh"
	kernelModuleFunction    = "atlas_register_neigh"
	kernelModuleLoadFile    = "/etc/modules-load.d/atlas-neigh.conf"
	kernelModuleSymbolsFile = "/proc/kallsyms"
)

var moduleCommand = &cobra.Command{
	Use:   "module",
	Short: "manage the Atlas neighbour kernel module",
}

var moduleInstallCommand = &cobra.Command{
	Use:   "install",
	Short: "install and load the Atlas neighbour kernel module",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return runModuleInstall()
	},
}

// runModuleInstall installs the embedded kernel module for the running kernel
// and loads it. The module must match the running kernel, because the release
// build compiled it for one kernel version.
//
// A loaded module keeps serving its kfunc until a reboot. A changed module
// file replaces the installed one and removes the loaded module, and a failed
// removal only warns, because the next reboot loads the new file anyway.
func runModuleInstall() error {
	release, err := commandOutput("uname", "-r")
	if err != nil {
		return err
	}
	release = strings.TrimSpace(release)

	extraDirectory := filepath.Join("/lib/modules", release, "extra")
	if err := os.MkdirAll(extraDirectory, 0755); err != nil {
		return err
	}

	installed := filepath.Join(extraDirectory, kernelModuleName+".ko")
	current, readErr := os.ReadFile(installed)
	changed := readErr != nil || !bytes.Equal(current, kernelModuleObject)

	if changed {
		if err := writeKernelModule(installed); err != nil {
			return err
		}
		if err := verifyKernelModuleRelease(installed, release); err != nil {
			return err
		}
		if kernelModuleFunctionRegistered() {
			if err := runCommand("rmmod", kernelModuleName); err != nil {
				fmt.Printf("atlas-wg-mesh: warning: the previous module stays loaded until a reboot: %v\n", err)
			}
		}
	}

	if err := runCommand("depmod", release); err != nil {
		return err
	}

	if err := runCommand("modprobe", kernelModuleName); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(kernelModuleLoadFile), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(kernelModuleLoadFile, []byte(kernelModuleName+"\n"), 0644); err != nil {
		return err
	}

	if !kernelModuleFunctionRegistered() {
		return errors.New("the Atlas neighbour kernel module loaded but registered no kfunc")
	}

	fmt.Printf("Atlas neighbour kernel module installed for %s\n", release)
	return nil
}

// writeKernelModule places the embedded module with an atomic rename, so a partial write can never sit in /lib/modules.
func writeKernelModule(path string) error {
	temporary := path + ".tmp"

	if err := os.WriteFile(temporary, kernelModuleObject, 0644); err != nil {
		return err
	}

	return os.Rename(temporary, path)
}

// verifyKernelModuleRelease rejects a module that the release build compiled for another kernel.
func verifyKernelModuleRelease(path, release string) error {
	vermagic, err := commandOutput("modinfo", "-F", "vermagic", path)
	if err != nil {
		return err
	}

	compiledFor := kernelModuleRelease(vermagic)
	if compiledFor == "" {
		return fmt.Errorf("the module %s carries no vermagic", path)
	}
	if compiledFor != release {
		return fmt.Errorf("the release build compiled the module for %s, but this host runs %s", compiledFor, release)
	}

	return nil
}

// kernelModuleRelease reads the kernel release from a modinfo vermagic line. The release is the first field.
func kernelModuleRelease(vermagic string) string {
	fields := strings.Fields(vermagic)

	if len(fields) == 0 {
		return ""
	}

	return fields[0]
}

// kernelModuleFunctionRegistered reports whether the module kfunc is live.
func kernelModuleFunctionRegistered() bool {
	symbols, err := os.ReadFile(kernelModuleSymbolsFile)
	if err != nil {
		return false
	}

	return strings.Contains(string(symbols), kernelModuleFunction)
}
