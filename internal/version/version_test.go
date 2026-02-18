package version

import "testing"

func TestIsDev(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"", true},
		{"v0.1.0", false},
		{"0.1.0", false},
	}
	for _, tt := range tests {
		Version = tt.version
		if got := IsDev(); got != tt.want {
			t.Errorf("IsDev() with Version=%q = %v, want %v", tt.version, got, tt.want)
		}
	}
	Version = "dev" // reset
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{" v0.1.0 ", "0.1.0"},
	}
	for _, tt := range tests {
		if got := normalize(tt.input); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v0.9.0", "v0.10.0", true},
		{"v1.0.0", "v0.9.0", false},
		{"", "v0.1.0", false},
		{"v0.1.0", "", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestCheckSkipsDevVersion(t *testing.T) {
	Version = "dev"
	result, err := Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for dev version")
	}
}

func TestFormatUpdateNotice(t *testing.T) {
	// nil result
	if got := FormatUpdateNotice(nil); got != "" {
		t.Errorf("expected empty string for nil result, got %q", got)
	}

	// not outdated
	r := &CheckResult{Current: "v0.1.0", Latest: "v0.1.0", Outdated: false}
	if got := FormatUpdateNotice(r); got != "" {
		t.Errorf("expected empty string when not outdated, got %q", got)
	}

	// outdated
	r = &CheckResult{
		Current:   "v0.1.0",
		Latest:    "v0.2.0",
		UpdateURL: "https://github.com/pantalk/pantalk/releases/tag/v0.2.0",
		Outdated:  true,
	}
	notice := FormatUpdateNotice(r)
	if notice == "" {
		t.Fatal("expected non-empty notice for outdated version")
	}
}
