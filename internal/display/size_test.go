package display

import "testing"

func TestApproximateSize(t *testing.T) {
	for _, test := range []struct {
		bytes int64
		want  string
	}{{bytes: -1, want: "unknown"}, {bytes: 1_990_000_000, want: "about 1990 MB"}} {
		if got := ApproximateSize(test.bytes); got != test.want {
			t.Errorf("ApproximateSize(%d) = %q, want %q", test.bytes, got, test.want)
		}
	}
}

func TestDownloadProgressUsesKnownTotalOnly(t *testing.T) {
	if got := DownloadProgress(1_000_000, 2_000_000); got != "1.0 MB of 2.0 MB (50%)" {
		t.Fatalf("known download progress = %q", got)
	}
	if got := DownloadProgress(1_500_000, 0); got != "1.5 MB downloaded" {
		t.Fatalf("unknown-total download progress = %q", got)
	}
}
