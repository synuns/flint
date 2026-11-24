package wire

import "github.com/synuns/flint/lib/store"

type ThresholdMeta = store.ThresholdMeta

func ThresholdMetas() []ThresholdMeta {
	return store.ThresholdMetas()
}

func ThresholdAutoGrade(key string) string {
	return store.ThresholdAutoGrade(key)
}

func ThresholdAutoWhitelist() map[string]string {
	return store.ThresholdAutoWhitelist()
}

func ThresholdMetasAppendForTest(m ThresholdMeta) (restore func()) {
	return store.ThresholdMetasAppendForTest(m)
}

func ThresholdMetasReplaceForTest(m ThresholdMeta) (restore func()) {
	return store.ThresholdMetasReplaceForTest(m)
}
