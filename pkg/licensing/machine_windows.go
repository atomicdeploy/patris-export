//go:build alm_compat && windows

package licensing

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func queryMachineIdentity(ctx context.Context) (machineIdentity, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	const script = `$ErrorActionPreference='Stop';` +
		`[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false);` +
		`$board=(Get-CimInstance -ClassName Win32_BaseBoard | Select-Object -First 1 -ExpandProperty SerialNumber);` +
		`$cpu=(Get-CimInstance -ClassName Win32_Processor | Select-Object -First 1 -ExpandProperty ProcessorId);` +
		`[pscustomobject]@{board=$board;cpu=$cpu}|ConvertTo-Json -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return machineIdentity{}, fmt.Errorf("%w: %v", ErrHardwareIdentity, ctx.Err())
		}
		return machineIdentity{}, fmt.Errorf("%w: query WMI/CIM: %v", ErrHardwareIdentity, err)
	}
	var payload struct {
		Board string `json:"board"`
		CPU   string `json:"cpu"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return machineIdentity{}, fmt.Errorf("%w: decode WMI/CIM response: %v", ErrHardwareIdentity, err)
	}
	if strings.TrimSpace(payload.Board) == "" || strings.TrimSpace(payload.CPU) == "" {
		return machineIdentity{}, fmt.Errorf("%w: BaseBoard SerialNumber or ProcessorId is empty", ErrHardwareIdentity)
	}
	// Preserve the exact WMI strings for compatibility; TrimSpace above is only
	// an emptiness check.
	return machineIdentity{BoardSerial: payload.Board, ProcessorID: payload.CPU}, nil
}
