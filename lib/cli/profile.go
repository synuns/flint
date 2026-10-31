// profile.go — v0.11.0 R1 노출 프로파일: meta exposure_profile 판독.
// 프로파일은 hot·nudge 동작만 분기한다(다른 명령·exit 계약 불변).
package cli

// ExposureKey — 노출 프로파일 meta 키. 값 normal(기본)|minimal.
const ExposureKey = "exposure_profile"

// ExposureProfile — 노출 프로파일 판독 헬퍼. 값 "minimal"일 때만 minimal,
// 미설정·파손값·조회 오류는 전부 normal(기본값 폴백).
func ExposureProfile(getKV func(string) (string, error)) string {
	v, err := getKV(ExposureKey)
	if err != nil || v != "minimal" {
		return "normal"
	}
	return "minimal"
}
