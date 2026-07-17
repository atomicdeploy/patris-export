//go:build alm_compat

package licensing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	testBoardSerial = "BOARD-1234"
	testProcessorID = "BFEBFBFF000906EA"
	testAppID       = "Digitalogic-Patris-ALM-v1"
	testChallenge   = "CED6E29936807D6E58D036519A5DBB96348593576466264017E841B977E85DB4"
	testLicenseKey  = "7933A7D613DD83057C736E645C0116509F7B0DEDB433976A0CF618BCCA7C5DC7"
)

func setTestMachine(t *testing.T) string {
	t.Helper()
	originalAppID := almAppID
	originalProvider := machineIdentityProvider
	originalExecutablePath := executablePath
	t.Cleanup(func() {
		almAppID = originalAppID
		machineIdentityProvider = originalProvider
		executablePath = originalExecutablePath
	})

	almAppID = testAppID
	machineIdentityProvider = func(context.Context) (machineIdentity, error) {
		return machineIdentity{BoardSerial: testBoardSerial, ProcessorID: testProcessorID}, nil
	}
	licensePath := filepath.Join(t.TempDir(), "config", "license.key")
	t.Setenv("PATRIS_EXPORT_LICENSE_FILE", licensePath)
	executablePath = func() (string, error) {
		return filepath.Join(t.TempDir(), "patris-export.exe"), nil
	}
	return licensePath
}

func TestALMChallengeInstallAndRemove(t *testing.T) {
	licensePath := setTestMachine(t)
	ctx := context.Background()

	challenge, err := Challenge(ctx)
	if err != nil {
		t.Fatalf("Challenge returned error: %v", err)
	}
	if challenge != testChallenge {
		t.Fatalf("Challenge = %q, want %q", challenge, testChallenge)
	}
	status := CurrentStatus(ctx)
	if status.Licensed || status.State != StateMissing {
		t.Fatalf("missing status = %+v", status)
	}
	if err := Enforce(ctx); !errors.Is(err, ErrLicenseRequired) {
		t.Fatalf("Enforce error = %v, want ErrLicenseRequired", err)
	}

	status, err = Install(ctx, testLicenseKey)
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !status.Licensed || status.Source != "per_user" || status.LicensePath != licensePath {
		t.Fatalf("installed status = %+v", status)
	}
	if data, err := os.ReadFile(licensePath); err != nil || string(data) != testLicenseKey {
		t.Fatalf("stored license = %q, err=%v", data, err)
	}
	if err := Enforce(ctx); err != nil {
		t.Fatalf("Enforce after install returned error: %v", err)
	}

	status, err = Remove(ctx)
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if status.Licensed || status.State != StateMissing {
		t.Fatalf("removed status = %+v", status)
	}
	if _, err := os.Stat(licensePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("per-user license still exists: %v", err)
	}
}

func TestALMLegacyAdjacentKeyIsDiscoveryOnly(t *testing.T) {
	setTestMachine(t)
	ctx := context.Background()
	executableDir := t.TempDir()
	executablePath = func() (string, error) {
		return filepath.Join(executableDir, "patris-export.exe"), nil
	}
	legacyPath := filepath.Join(executableDir, "key")
	if err := os.WriteFile(legacyPath, []byte(testLicenseKey+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}

	status := CurrentStatus(ctx)
	if !status.Licensed || status.Source != "legacy_read_only" {
		t.Fatalf("legacy status = %+v", status)
	}
	status, err := Remove(ctx)
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if !status.Licensed || status.Source != "legacy_read_only" {
		t.Fatalf("legacy status after Remove = %+v", status)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("Remove changed the read-only legacy key: %v", err)
	}
}

func TestALMRejectsInvalidKeyWithoutWriting(t *testing.T) {
	licensePath := setTestMachine(t)
	status, err := Install(context.Background(), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if !errors.Is(err, ErrInvalidLicense) {
		t.Fatalf("Install error = %v, want ErrInvalidLicense (status=%+v)", err, status)
	}
	if _, err := os.Stat(licensePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid key was written: %v", err)
	}
}

func TestALMInstallReplacesInvalidPerUserKey(t *testing.T) {
	licensePath := setTestMachine(t)
	if err := os.MkdirAll(filepath.Dir(licensePath), 0700); err != nil {
		t.Fatal(err)
	}
	const invalidKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err := os.WriteFile(licensePath, []byte(invalidKey), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := Install(context.Background(), testLicenseKey)
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !status.Licensed || status.Source != "per_user" {
		t.Fatalf("replacement status = %+v", status)
	}
	data, err := os.ReadFile(licensePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testLicenseKey {
		t.Fatalf("replacement key = %q, want %q", data, testLicenseKey)
	}
}

func TestALMMissingAppIDFailsClosed(t *testing.T) {
	setTestMachine(t)
	almAppID = ""
	status := CurrentStatus(context.Background())
	if status.State != StateMisconfigured || status.Licensed {
		t.Fatalf("misconfigured status = %+v", status)
	}
	if err := Enforce(context.Background()); !errors.Is(err, ErrAppIDRequired) {
		t.Fatalf("Enforce error = %v, want ErrAppIDRequired", err)
	}
}
