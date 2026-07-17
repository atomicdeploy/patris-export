//go:build !alm_compat

package licensing

import "context"

func BuildMode() string { return ModeNone }

func Required() bool { return false }

func CurrentStatus(context.Context) Status {
	return Status{
		Enabled:  false,
		Required: false,
		Licensed: true,
		Mode:     ModeNone,
		State:    StateNotRequired,
		Message:  "This Patris Export build does not require a license.",
	}
}

func Enforce(context.Context) error { return nil }

func Challenge(context.Context) (string, error) { return "", ErrDisabled }

func Install(context.Context, string) (Status, error) {
	return CurrentStatus(context.Background()), ErrDisabled
}

func Remove(context.Context) (Status, error) {
	return CurrentStatus(context.Background()), ErrDisabled
}
