// +build !windows

package dockerd

import (
	"github.com/spf13/pflag"
)

func initService(daemonCli *DaemonCli) (bool, error) {
	return false, nil
}

func installServiceFlags(flags *pflag.FlagSet) {
}
