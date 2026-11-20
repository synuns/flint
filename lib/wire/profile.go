// profile.go — exposure-profile reading (a shared Store utility). Owns only
// the helper reading the meta exposure_profile value normal (default) | minimal.
// The profile branches only hot and nudge behavior (other commands and exit contracts unchanged).
// knowledge·ops seam(exposure
// Consumed by the knowledge/ops seams (exposure profile reading). The verdict command (flint profile) remains in cli/profile.go.
package wire

// ExposureKey — the exposure-profile meta key. Values normal (default) | minimal.
const ExposureKey = "exposure_profile"

// ExposureProfile — the exposure-profile reading helper. minimal only when the
// value is "minimal"; unset, corrupt, and lookup errors are all normal (the default fallback).
func ExposureProfile(getKV func(string) (string, error)) string {
	v, err := getKV(ExposureKey)
	if err != nil || v != "minimal" {
		return "normal"
	}
	return "minimal"
}
