//go:build !darwin && !linux

package backend

import (
	"fmt"
	"runtime"
)

func launchExternalTerminal(command string) error {
	return fmt.Errorf("external terminals are not supported on %s", runtime.GOOS)
}
