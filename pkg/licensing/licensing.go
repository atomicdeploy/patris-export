package licensing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ModeNone           = "none"
	ModeALMCompat      = "alm_compat_utf8_v1"
	StateNotRequired   = "not_required"
	StateLicensed      = "licensed"
	StateMissing       = "missing"
	StateInvalid       = "invalid"
	StateMisconfigured = "misconfigured"
	StateUnavailable   = "hardware_unavailable"
	StateUnsupported   = "unsupported_platform"
)

var (
	ErrDisabled         = errors.New("license management is disabled in this build")
	ErrLicenseRequired  = errors.New("a valid Patris Export license is required")
	ErrInvalidLicense   = errors.New("the license key is invalid for this machine")
	ErrAppIDRequired    = errors.New("ALM_APP_ID was not embedded in this build")
	ErrUnsupported      = errors.New("ALM compatibility licensing is supported only on Windows")
	ErrHardwareIdentity = errors.New("unable to read the Windows hardware identity")
	executablePath      = os.Executable
)

// Status is safe to show in installers and application diagnostics. It never
// contains the expected license key or an application secret/private key.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Required    bool   `json:"required"`
	Licensed    bool   `json:"licensed"`
	Mode        string `json:"mode"`
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
	Challenge   string `json:"challenge,omitempty"`
	LicensePath string `json:"license_path,omitempty"`
	Source      string `json:"source,omitempty"`
}

type CheckError struct {
	Status Status
	Cause  error
}

func (e *CheckError) Error() string {
	if e == nil {
		return ""
	}
	if e.Status.Message != "" {
		return e.Status.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return ErrLicenseRequired.Error()
}

func (e *CheckError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HashUTF8 implements the current AHK_ALM string-hashing behavior: SHA-256
// over UTF-8 bytes without a trailing NUL, rendered as exactly 64 uppercase
// hexadecimal characters.
func HashUTF8(value string) string {
	digest := sha256.Sum256([]byte(value))
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func hardwareChallenge(boardSerial, processorID string) string {
	return HashUTF8(boardSerial + processorID)
}

func expectedLicenseKey(challenge, appID string) string {
	return HashUTF8(challenge + appID)
}

func UserLicensePath() string {
	if override := strings.TrimSpace(os.Getenv("PATRIS_EXPORT_LICENSE_FILE")); override != "" {
		return filepath.Clean(os.ExpandEnv(override))
	}
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = "."
	}
	dir := "patris-export"
	if runtime.GOOS == "windows" {
		dir = "Patris Export"
	}
	return filepath.Join(base, dir, "license.key")
}

func LegacyLicensePath() string {
	executable, err := executablePath()
	if err != nil || strings.TrimSpace(executable) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "key")
}

func normalizeKey(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return "", ErrInvalidLicense
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrInvalidLicense
	}
	return value, nil
}

func readLicense(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeKey(string(data))
}

func writeUserLicense(key string) error {
	path := UserLicensePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".license-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(key); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
