package platform

import (
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestBSDDistributionRemainsSpecificForAdapterSelection(t *testing.T) {
	for _, distribution := range []string{"freebsd", "openbsd", "netbsd", "dragonfly"} {
		if got := operatingSystemDistribution(distribution, tool.BSD); got != distribution {
			t.Errorf("distribution for %s = %q, want %q", distribution, got, distribution)
		}
	}
}
