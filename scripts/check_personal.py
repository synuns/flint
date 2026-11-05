#!/usr/bin/env python3
"""check_personal.py — 개인 데이터 유출 방지 가드.

이 저장소는 flint 오픈소스 패키지만 담는다(개인 DB 사용과 엄격 분리).
make ci와 하네스 PreToolUse 훅(scripts/hook_personal_guard.sh) 양쪽에서
실행된다.

두 층의 검사:
1. 일반 패턴(repo 내장) — 모든 이메일 주소·기기 절대경로(/Users/…,
   /home/…)·주요 개인 문구. 개인 문자열을 repo에 두지 않기 위해
   패턴은 범용 형태만.
2. 로컬 마커 파일(선택) — ~/.config/flint/personal-markers.txt 에 한 줄씩
   개인 고유 문자열을 두면 추가 검사한다(파일 자체는 repo 밖 —
   개인 문자열을 repo에 다시 싣지 않는 원칙).
"""
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LOCAL_MARKERS = Path.home() / ".config" / "flint" / "personal-markers.txt"

# repo 내장 범용 패턴 — (이름, 정규식). 이메일 전면 금지(코드의 예시
# 이메일은 fixtures에서 중성 형식 사용), 기기 절대경로 금지.
GENERIC = [
    ("이메일 주소", re.compile(r"[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.(com|net|org|kr|io|dev|co)\b")),
    ("기기 절대경로", re.compile(r"/Users/[A-Za-z0-9_-]+|/home/[A-Za-z0-9_-]+")),
]
# 이메일 예외: GitHub noreply 형식은 허용(작성자 식별 표준).
NOREPLY = re.compile(r"\d+\+[\w.-]+@users\.noreply\.github\.com")


def local_markers():
    if not LOCAL_MARKERS.is_file():
        return []
    return [l.strip() for l in LOCAL_MARKERS.read_text(encoding="utf-8").splitlines()
            if l.strip() and not l.startswith("#")]


def scan(text: str):
    hits = []
    for m in re.finditer(GENERIC[0][1], text):
        addr = m.group(0)
        if NOREPLY.search(addr):
            continue
        hits.append("이메일: " + addr)
    for name, pat in GENERIC[1:]:
        for m in pat.finditer(text):
            hits.append(f"{name}: {m.group(0)[:40]}")
    for mk in local_markers():
        if mk in text:
            hits.append("로컬 마커: " + mk[:20] + "…")
    return hits


def main() -> int:
    files = subprocess.run(
        ["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True
    ).stdout.split()
    # 스테이지된 신규 파일도 포함(커밋 직전 가드용)
    staged = subprocess.run(
        ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR"],
        cwd=ROOT, capture_output=True, text=True).stdout.split()
    failed = False
    for f in dict.fromkeys(files + staged):
        p = ROOT / f
        if not p.is_file() or p.suffix in {".png", ".jpg", ".zip", ".gz"}:
            continue
        try:
            s = p.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for h in scan(s):
            print(f"FAIL {f}: {h}")
            failed = True
    if failed:
        print("치환/제거 후 재시도 — 기준: scripts/check_personal.py (로컬 마커: ~/.config/flint/personal-markers.txt)")
        return 1
    print(f"OK 개인 데이터 없음 ({len(set(files + staged))}개 파일·로컬 마커 {len(local_markers())}종)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
