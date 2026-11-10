// tuning_keys_single_source_test.go — 키 집합 동질성 가드는
// tuning_test.go TestTuningKeysMatchRegistry가 담당한다. 여기선 그 가드가
// 못 잡는 나머지 드리프트 지점 하나: meta 관리 키가 thresholds 키와
// 겹치면 tuningList가 같은 키를 두 번 출력한다(표시 이중 행 —
// tuningKeyAll의 중복 제거는 reset --all만 구한다).
package cli

import "testing"

func TestTuningMetaKeysDisjointFromThresholds(t *testing.T) {
	th := map[string]bool{}
	for _, k := range thresholdsKeys() {
		th[k] = true
	}
	for _, mk := range tuningMetaKeys() {
		if th[mk.Key] {
			t.Errorf("meta 키 %q가 thresholds 키와 겹침 — tuningList 이중 출력", mk.Key)
		}
	}
}
