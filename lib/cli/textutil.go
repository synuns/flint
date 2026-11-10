// textutil.go — 텍스트 출력 보조 유틸 단일 소스. rune 절단 3중 복제
// (import 상한·maintain 보고 접두·pending 한 줄화)가 각자 베껴지며
// 드리프트하던 것을 v0.34.3 감사 스윕(스펙 §1.3)으로 수렴시켰다.
package cli

// truncateRunes — rune 상한 절단(멀티바이트 안전). 상한 이하·빈 문자열은
// 불변 반환. 소비자 관례: import 본문 500룬·maintain 요약 접두 40룬·
// pending 한 줄화 상한(호출자 지정).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
