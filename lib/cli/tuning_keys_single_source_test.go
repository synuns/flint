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
