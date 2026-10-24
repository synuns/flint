package docs

// v091_assets_test.go — README "How It Works" 데모 GIF 고정(2026-10-24).
// GIF·테이프 산출물 존재 + 두 README 참조 — 깨진 링크·재생성 스크립트
// 유실을 방지한다(v06 문서 고정·v09 README 링크 고정 패턴 승계).

import (
	"os"
	"strings"
	"testing"
)

// TestV091ReadmeDemoGif — 두 README 모두 GIF를 참조하고 산출물이 실재.
func TestV091ReadmeDemoGif(t *testing.T) {
	for _, readme := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(readme)
		if err != nil {
			t.Fatalf("readme 읽기 실패 %s: %v", readme, err)
		}
		if !strings.Contains(string(b), "assets/flint-demo.gif") {
			t.Errorf("%s: 데모 GIF 참조 없음(assets/flint-demo.gif)", readme)
		}
	}
	for _, asset := range []string{"../assets/flint-demo.gif", "../assets/flint-demo.tape"} {
		if _, err := os.Stat(asset); err != nil {
			t.Errorf("데모 산출물 없음 %s: %v", asset, err)
		}
	}
}
