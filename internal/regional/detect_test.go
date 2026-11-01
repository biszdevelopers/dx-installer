package regional

import (
	"errors"
	"testing"
)

func TestClassifyLocaleAndTimezoneSignals(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  Region
	}{
		{"zh_CN.UTF-8", MainlandChina},
		{"en-US", Global},
		{"zh_HK.UTF-8", Global},
		{"C.UTF-8", Unknown},
	} {
		if got := classifyLocale(testCase.value); got != testCase.want {
			t.Errorf("classifyLocale(%q) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
	if got := classifyTimezone("Asia/Shanghai"); got != MainlandChina {
		t.Fatalf("China time zone classified as %q", got)
	}
	if got := classifyTimezone("America/Los_Angeles"); got != Global {
		t.Fatalf("global time zone classified as %q", got)
	}
}

func TestPreferenceResolvesRegionOrRequiresChoiceForConflicts(t *testing.T) {
	if fallback, err := (Result{Region: MainlandChina}).UseRegionalFallback(Automatic); err != nil || !fallback {
		t.Fatalf("automatic China policy = %v, %v", fallback, err)
	}
	if fallback, err := (Result{Region: Global}).UseRegionalFallback(Automatic); err != nil || fallback {
		t.Fatalf("automatic global policy = %v, %v", fallback, err)
	}
	if _, err := (Result{Region: Conflict}).UseRegionalFallback(Automatic); !errors.Is(err, ErrConflictingSignals) {
		t.Fatalf("conflicting signals were not surfaced: %v", err)
	}
	if fallback, err := (Result{Region: Conflict}).UseRegionalFallback(China); err != nil || !fallback {
		t.Fatalf("manual China override did not resolve conflict: %v, %v", fallback, err)
	}
}
