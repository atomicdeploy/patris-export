//go:build alm_compat

package licensing

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"
)

// almAppID is populated only by an explicit alm_compat build through -ldflags
// -X. It is an identifier/derivation input, not a private key or secret.
var almAppID string

var machineIdentityProvider = queryMachineIdentity

func BuildMode() string { return ModeALMCompat }

func Required() bool { return true }

func CurrentStatus(ctx context.Context) Status {
	status := Status{
		Enabled:     true,
		Required:    true,
		Mode:        ModeALMCompat,
		State:       StateMissing,
		LicensePath: UserLicensePath(),
	}
	appID := strings.TrimSpace(almAppID)
	if appID == "" {
		status.State = StateMisconfigured
		status.Message = ErrAppIDRequired.Error()
		return status
	}

	identity, err := machineIdentityProvider(ctx)
	if err != nil {
		status.State = StateUnavailable
		if errors.Is(err, ErrUnsupported) {
			status.State = StateUnsupported
		}
		status.Message = err.Error()
		return status
	}
	status.Challenge = hardwareChallenge(identity.BoardSerial, identity.ProcessorID)

	key, source, err := discoverLicense()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.Message = ErrLicenseRequired.Error()
			return status
		}
		status.State = StateInvalid
		status.Message = fmt.Sprintf("read license: %v", err)
		return status
	}
	status.Source = source
	expected := expectedLicenseKey(status.Challenge, appID)
	if subtle.ConstantTimeCompare([]byte(key), []byte(expected)) != 1 {
		status.State = StateInvalid
		status.Message = ErrInvalidLicense.Error()
		return status
	}
	status.Licensed = true
	status.State = StateLicensed
	status.Message = "Patris Export license is valid for this machine."
	return status
}

func Enforce(ctx context.Context) error {
	status := CurrentStatus(ctx)
	if status.Licensed {
		return nil
	}
	cause := ErrLicenseRequired
	switch status.State {
	case StateInvalid:
		cause = ErrInvalidLicense
	case StateMisconfigured:
		cause = ErrAppIDRequired
	case StateUnsupported:
		cause = ErrUnsupported
	case StateUnavailable:
		cause = ErrHardwareIdentity
	}
	return &CheckError{Status: status, Cause: cause}
}

func Challenge(ctx context.Context) (string, error) {
	status := CurrentStatus(ctx)
	if status.Challenge != "" {
		return status.Challenge, nil
	}
	if err := Enforce(ctx); err != nil {
		return "", err
	}
	return "", ErrHardwareIdentity
}

func Install(ctx context.Context, value string) (Status, error) {
	key, err := normalizeKey(value)
	if err != nil {
		return CurrentStatus(ctx), err
	}
	status := CurrentStatus(ctx)
	if status.Challenge == "" {
		return status, &CheckError{Status: status, Cause: statusCause(status)}
	}
	expected := expectedLicenseKey(status.Challenge, strings.TrimSpace(almAppID))
	if subtle.ConstantTimeCompare([]byte(key), []byte(expected)) != 1 {
		return status, ErrInvalidLicense
	}
	if err := writeUserLicense(key); err != nil {
		return status, fmt.Errorf("write per-user license: %w", err)
	}
	status = CurrentStatus(ctx)
	if !status.Licensed {
		return status, &CheckError{Status: status, Cause: statusCause(status)}
	}
	return status, nil
}

func Remove(ctx context.Context) (Status, error) {
	path := UserLicensePath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return CurrentStatus(ctx), fmt.Errorf("remove per-user license: %w", err)
	}
	// The legacy adjacent key is deliberately discovery-only. Removing it is a
	// manual administrator action so an application uninstall cannot destroy a
	// license placed beside a legacy executable.
	return CurrentStatus(ctx), nil
}

func discoverLicense() (key, source string, err error) {
	userPath := UserLicensePath()
	key, err = readLicense(userPath)
	if err == nil {
		return key, "per_user", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "per_user", err
	}
	legacyPath := LegacyLicensePath()
	if legacyPath == "" {
		return "", "", os.ErrNotExist
	}
	key, err = readLicense(legacyPath)
	if err != nil {
		return "", "legacy", err
	}
	return key, "legacy_read_only", nil
}

func statusCause(status Status) error {
	switch status.State {
	case StateInvalid:
		return ErrInvalidLicense
	case StateMisconfigured:
		return ErrAppIDRequired
	case StateUnsupported:
		return ErrUnsupported
	case StateUnavailable:
		return ErrHardwareIdentity
	default:
		return ErrLicenseRequired
	}
}

type machineIdentity struct {
	BoardSerial string
	ProcessorID string
}
