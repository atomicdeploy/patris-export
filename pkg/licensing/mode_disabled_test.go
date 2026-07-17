//go:build !alm_compat

package licensing

import (
	"context"
	"errors"
	"testing"
)

func TestStandardBuildDoesNotRequireLicense(t *testing.T) {
	status := CurrentStatus(context.Background())
	if status.Enabled || status.Required || !status.Licensed || status.State != StateNotRequired {
		t.Fatalf("unexpected standard-build status: %+v", status)
	}
	if err := Enforce(context.Background()); err != nil {
		t.Fatalf("standard build enforcement failed: %v", err)
	}
	if _, err := Challenge(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Challenge error = %v, want ErrDisabled", err)
	}
}
