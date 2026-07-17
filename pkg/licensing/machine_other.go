//go:build alm_compat && !windows

package licensing

import "context"

func queryMachineIdentity(context.Context) (machineIdentity, error) {
	return machineIdentity{}, ErrUnsupported
}
