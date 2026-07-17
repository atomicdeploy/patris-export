package licensing

import (
	"regexp"
	"testing"
)

func TestALMCompatibilityGoldenVectorUTF8(t *testing.T) {
	const (
		boardSerial    = "BOARD-1234"
		processorID    = "BFEBFBFF000906EA"
		appID          = "Digitalogic-Patris-ALM-v1"
		wantChallenge  = "CED6E29936807D6E58D036519A5DBB96348593576466264017E841B977E85DB4"
		wantLicenseKey = "7933A7D613DD83057C736E645C0116509F7B0DEDB433976A0CF618BCCA7C5DC7"
	)

	challenge := hardwareChallenge(boardSerial, processorID)
	if challenge != wantChallenge {
		t.Fatalf("hardware challenge = %q, want %q", challenge, wantChallenge)
	}
	if key := expectedLicenseKey(challenge, appID); key != wantLicenseKey {
		t.Fatalf("license key = %q, want %q", key, wantLicenseKey)
	}
}

func TestHashUTF8ProducesUppercaseSHA256(t *testing.T) {
	got := HashUTF8("Patris 📦")
	if !regexp.MustCompile(`^[0-9A-F]{64}$`).MatchString(got) {
		t.Fatalf("hash %q is not exactly 64 uppercase hexadecimal characters", got)
	}
}

func TestNormalizeKey(t *testing.T) {
	const key = "7933A7D613DD83057C736E645C0116509F7B0DEDB433976A0CF618BCCA7C5DC7"
	got, err := normalizeKey("  " + key + "\r\n")
	if err != nil {
		t.Fatalf("normalizeKey returned error: %v", err)
	}
	if got != key {
		t.Fatalf("normalizeKey = %q, want %q", got, key)
	}
	if _, err := normalizeKey("not-a-key"); err == nil {
		t.Fatal("normalizeKey accepted an invalid key")
	}
}
