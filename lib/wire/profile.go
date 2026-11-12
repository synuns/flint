// profile.go — v0.11.0 R1 노출 프로파일 판독(공용 Store 유틸). meta
// exposure_profile 값 normal(기본)|minimal을 읽는 헬퍼만 소유한다.
// 프로파일은 hot·nudge 동작만 분기한다(다른 명령·exit 계약 불변).
// v0.40.0 T4: cli 소유에서 wire로 이동 — knowledge·ops seam(exposure
// profile 판독)이 소비. 판정 명령(flint profile)은 cli/profile.go 잔류.
package wire

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
