// Package version provides build-time version information and update checking
// for pantalk binaries. When the binary is built via `go build` without setting
// the Version variable, it defaults to "dev" and all update checks are skipped.
package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Values set at build time via -ldflags.
var (
	// Version is the semver tag (e.g. "v0.3.1"). Defaults to "dev" when
	// built without ldflags (i.e. via `go run`).
	Version = "dev"
)

const (
	// releaseRepo is the GitHub owner/repo used to check for new releases.
	releaseRepo = "pantalk/pantalk"

	// checkTimeout limits how long the HTTP call to GitHub may take.
	checkTimeout = 4 * time.Second
)

// IsDev reports whether the binary was built without an explicit version tag.
func IsDev() bool {
	return Version == "dev" || Version == ""
}

// ghRelease is a minimal representation of a GitHub release.
type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease queries the GitHub API for the latest published release of
// the pantalk repository. Returns the tag name, the release URL, and any error.
func LatestRelease() (tag string, url string, err error) {
	client := &http.Client{Timeout: checkTimeout}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", releaseRepo), nil)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var rel ghRelease

	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", err
	}

	return rel.TagName, rel.HTMLURL, nil
}

// normalize strips the leading "v" from a version string for comparison.
func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// IsNewer reports whether latest is a newer version than current. Both values
// are expected to be semver strings (with or without a "v" prefix).
func IsNewer(current, latest string) bool {
	c := parseSemver(normalize(current))
	l := parseSemver(normalize(latest))
	if c == nil || l == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	return false
}

// parseSemver splits a "major.minor.patch" string into three integers.
// Returns nil if the string is not a valid semver triple.
func parseSemver(v string) []int {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// CheckResult holds the outcome of a version check.
type CheckResult struct {
	Current   string
	Latest    string
	UpdateURL string
	Outdated  bool
}

// Check queries GitHub for the latest release and compares it against the
// current Version. If Version is "dev" (i.e. not a release binary), the
// check is skipped and a nil result is returned.
func Check() (*CheckResult, error) {
	if IsDev() {
		return nil, nil
	}

	tag, url, err := LatestRelease()
	if err != nil {
		return nil, err
	}

	return &CheckResult{
		Current:   Version,
		Latest:    tag,
		UpdateURL: url,
		Outdated:  IsNewer(Version, tag),
	}, nil
}

// FormatUpdateNotice returns a human-readable update notice string. Returns
// an empty string if there is no update available.
func FormatUpdateNotice(r *CheckResult) string {
	if r == nil || !r.Outdated {
		return ""
	}

	return fmt.Sprintf(
		"A new version of pantalk is available: %s → %s\nRelease: %s",
		r.Current, r.Latest, r.UpdateURL,
	)
}
