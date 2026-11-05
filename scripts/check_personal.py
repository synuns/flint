#!/usr/bin/env python3
"""check_personal.py — 개인 데이터 유출 방지 가드 (v0.16.2 응급 수습 이후 상비).

이 저장소는 flint 오픈소스 패키지만 담는다 — 개인 프로젝트명·개인 문서
발췌·기기 경로·실명·이메일이 추적 파일에 있으면 즉시 실패. make ci와
커밋 훅(하네스) 양쪽에서 실행된다.

마커 목록은 정확 문구만(일반 단어 오경보 방지 — '볼트' 같은 설계 은유는
제외). 새 개인 데이터 유형 발견 시 MARKERS에 추가한다.
"""
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

MARKERS = [
    # 개인 프로젝트명 (시드 유래 — 중성 데모명으로 치환됨)
    "데모 게임 프로젝트",
    "데모 아일랜드",
    "데모 유틸 프로젝트",
    "데모 스크립트 모음",
    "데모 게임",
    "sample-game",
    # 개인 문서·작업 발췌
    "데모 재편",
    "표 위키링크",
    "데모 사다리",
    "데모 항목",
    "이미지 검증 데모",
    "데모 사용자 선호",
    # 기기 경로·신원
    "/Users/dev",
    "57736547+synuns@users.noreply.github.com",
    "synuns",
]


def main() -> int:
    files = subprocess.run(
        ["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True
    ).stdout.split()
    hits = []
    for f in files:
        p = ROOT / f
        if not p.is_file():
            continue
        try:
            s = p.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for m in MARKERS:
            if m in s:
                hits.append(f"{f}: {m}")
    if hits:
        print("FAIL 개인 데이터 마커 감지:")
        for h in hits:
            print(" ", h)
        print("치환/제거 후 재시도 — 기준: scripts/check_personal.py")
        return 1
    print("OK 개인 데이터 마커 없음 (%d개 추적 파일)" % len(files))
    return 0


if __name__ == "__main__":
    sys.exit(main())
