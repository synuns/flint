// tuning_keys_single_source_test.go — the key-set homogeneity guard is owned by
// TestTuningKeysMatchRegistry in tuning_test.go. Here we cover the one remaining drift point that guard
// cannot catch: if a meta management key overlaps a thresholds key,
// tuningList prints the same key twice (a duplicate display row —
// tuningKeyAll's dedup serves only reset --all).
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
