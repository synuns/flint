package wire

const ExposureKey = "exposure_profile"

func ExposureProfile(getKV func(string) (string, error)) string {
	v, err := getKV(ExposureKey)
	if err != nil || v != "minimal" {
		return "normal"
	}
	return "minimal"
}
