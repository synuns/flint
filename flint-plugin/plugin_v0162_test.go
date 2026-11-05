package flintplugin

import (
	"strings"
	"testing"
)

// v0.16.2: TestPluginVersionV0161 삭제 — 0.16.1 prefix 강제(버전 동기).
// 본 테스트가 대체. 배지·README가 v0.16.2를 가리키는 시점에 plugin.json도
// 같은 버전이어야 한다(v0.16.1 사이클 내 범프 관례 승계).
// TestPluginVersionV0162 — 0.16.3 전환으로 삭제(선례·TestPluginVersionMatchesBadge가 동기 가드).
