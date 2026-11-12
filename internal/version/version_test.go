package version

import "testing"

func TestIsNewerComparesPatchMinorAndMajor(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"0.1.1", "0.1.0", true},
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.0.9", "0.1.0", false},
	}

	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestFormatDollarsCentAndAboveTwoDecimals(t *testing.T) {
	if got := FormatDollars("2500000"); got != "$2.50" {
		t.Errorf("got %q", got)
	}
}

func TestFormatDollarsKeepsSubCentPrecision(t *testing.T) {
	if got := FormatDollars("123"); got != "$0.000123" {
		t.Errorf("got %q", got)
	}
}

func TestFormatDollarsRendersZero(t *testing.T) {
	if got := FormatDollars("0"); got != "$0.00" {
		t.Errorf("got %q", got)
	}
}

func TestVersionComesFromPackageJSON(t *testing.T) {
	if PackageName != "@featherlessai/gpu-cloud-cli" {
		t.Errorf("PackageName = %q", PackageName)
	}

	if Version == "" {
		t.Error("Version is empty")
	}
}
