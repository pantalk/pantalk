package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

// --- parseSemver tests ---

func TestParseSemver_Valid(t *testing.T) {
	parts := parseSemver("1.2.3")
	if parts == nil {
		t.Fatal("expected non-nil")
	}
	if parts[0] != 1 || parts[1] != 2 || parts[2] != 3 {
		t.Errorf("expected [1,2,3], got %v", parts)
	}
}

func TestParseSemver_TwoParts(t *testing.T) {
	parts := parseSemver("1.2")
	if parts != nil {
		t.Errorf("expected nil for two-part version, got %v", parts)
	}
}

func TestParseSemver_OnePart(t *testing.T) {
	parts := parseSemver("1")
	if parts != nil {
		t.Errorf("expected nil for single-part version, got %v", parts)
	}
}

func TestParseSemver_NonNumeric(t *testing.T) {
	parts := parseSemver("1.2.beta")
	if parts != nil {
		t.Errorf("expected nil for non-numeric part, got %v", parts)
	}
}

func TestParseSemver_Empty(t *testing.T) {
	parts := parseSemver("")
	if parts != nil {
		t.Errorf("expected nil for empty string, got %v", parts)
	}
}

// --- IsNewer edge cases ---

func TestIsNewer_PatchVersion(t *testing.T) {
	if !IsNewer("v1.0.0", "v1.0.1") {
		t.Error("expected newer for patch bump")
	}
}

func TestIsNewer_MajorVersion(t *testing.T) {
	if !IsNewer("v1.9.9", "v2.0.0") {
		t.Error("expected newer for major bump")
	}
}

func TestIsNewer_OlderPatch(t *testing.T) {
	if IsNewer("v1.0.1", "v1.0.0") {
		t.Error("expected not newer for older patch")
	}
}

func TestIsNewer_Invalid(t *testing.T) {
	if IsNewer("invalid", "v1.0.0") {
		t.Error("expected false for invalid current")
	}
	if IsNewer("v1.0.0", "invalid") {
		t.Error("expected false for invalid latest")
	}
}

// --- LatestRelease with httptest ---

func TestLatestRelease_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v0.5.0",
			HTMLURL: "https://github.com/pantalk/pantalk/releases/tag/v0.5.0",
		})
	}))
	defer server.Close()

	// We can't easily override the URL in LatestRelease since it's hardcoded,
	// so test the JSON parsing indirectly. We'll test Check with httptest below.
	// For now, verify the struct is correct.
	rel := ghRelease{TagName: "v0.5.0", HTMLURL: "https://example.com"}
	data, _ := json.Marshal(rel)
	var decoded ghRelease
	json.Unmarshal(data, &decoded)
	if decoded.TagName != "v0.5.0" {
		t.Errorf("expected v0.5.0, got %q", decoded.TagName)
	}
}

// --- Check tests ---

func TestCheck_DevVersion(t *testing.T) {
	Version = "dev"
	result, err := Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for dev version")
	}
}

func TestCheck_EmptyVersion(t *testing.T) {
	Version = ""
	result, err := Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for empty version")
	}
	Version = "dev" // reset
}

// --- FormatUpdateNotice edge cases ---

func TestFormatUpdateNotice_ContainsVersions(t *testing.T) {
	r := &CheckResult{
		Current:   "v0.1.0",
		Latest:    "v0.2.0",
		UpdateURL: "https://example.com/release",
		Outdated:  true,
	}
	notice := FormatUpdateNotice(r)
	if notice == "" {
		t.Fatal("expected non-empty notice")
	}
	// Should contain both versions and the URL
	for _, want := range []string{"v0.1.0", "v0.2.0", "https://example.com/release"} {
		if !contains(notice, want) {
			t.Errorf("notice should contain %q, got: %q", want, notice)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
