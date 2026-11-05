#!/usr/bin/env python3
"""check_personal.py — 개인 데이터 유출 방지 가드.

이 저장소는 flint 오픈소스 패키지만 담는다. make ci와 하네스
PreToolUse 훅(scripts/hook_personal_guard.sh) 양쪽에서 실행된다.

검사 층:
1. 범용 패턴(repo 내장) — 모든 이메일(TLD 화이트리스트 아닌 일반 형식,
   GitHub noreply만 예외)·기기 절대경로(/Users/…·/home/…).
2. 로컬 마커(선택) — ~/.config/flint/personal-markers.txt 한 줄씩.
   repo에 개인 문자열을 두지 않는 원칙.

--cached 플래그(훅 경로): 워킹트리와 함께 스테이지된 blob도 검사
(TOCTOU 방어 — 리뷰 F3).

구조적 한계(수용·문서화): 바이너리 내용·비평문 인코딩(base64 등)은
검출 못 함. GitHub CI엔 로컬 마커가 없어 개인 고유 문자열의 최종
방어는 로컬 훅이다.
"""
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LOCAL_MARKERS = Path.home() / ".config" / "flint" / "personal-markers.txt"
BINARY_SUFFIX = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip",
                 ".gz", ".tar", ".tgz", ".ico", ".icns", ".woff", ".woff2"}

# 이메일: 일반 형식 전면 검출(TLD 제한 없음 — F2). 예외는 GitHub noreply
# 식별과 RFC 2606/5737 예약 도메인(테스트 전용 — 개인일 수 없음).
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
NOREPLY = re.compile(r"^\d+\+[\w.-]+@users\.noreply\.github\.com$")
RESERVED = re.compile(
    r"@([\w.-]+)\.(example|invalid|test|localhost)$|"
    r"@(example\.(com|net|org))$", re.IGNORECASE)


def email_allowed(addr: str) -> bool:
    return bool(NOREPLY.match(addr) or RESERVED.search(addr))
GENERIC = [
    ("기기 절대경로", re.compile(r"/Users/[A-Za-z0-9_.-]+|/home/[A-Za-z0-9_.-]+")),
]


def local_markers():
    if not LOCAL_MARKERS.is_file():
        return []
    return [l.strip() for l in LOCAL_MARKERS.read_text(encoding="utf-8").splitlines()
            if l.strip() and not l.startswith("#")]


def scan_text(text, markers):
    hits = []
    for m in EMAIL.finditer(text):
        addr = m.group(0)
        if email_allowed(addr):
            continue
        hits.append("이메일: " + addr)
    for name, pat in GENERIC:
        for m in pat.finditer(text):
            hits.append(f"{name}: {m.group(0)[:40]}")
    for mk in markers:
        if mk in text:
            hits.append("로컬 마커: " + mk[:20] + "…")
    return hits


def ls_files():
    out = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT,
                         capture_output=True, text=True, check=True).stdout
    return [f for f in out.split("\0") if f]


def scan_working_tree(files, markers):
    hits = []
    for f in files:
        p = ROOT / f
        if not p.is_file() or p.suffix.lower() in BINARY_SUFFIX:
            continue
        try:
            s = p.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for h in scan_text(s, markers):
            print(f"FAIL {f}: {h}")
            hits.append(f)
    return hits


def scan_index_blobs(markers):
    """스테이지된 blob 검사(--cached) — 워킹트리 조작과 무관한
    커밋 실체 방어(F3). git grep --cached로 blob 내용 직독."""
    hits = []
    patterns = [EMAIL] + [re.compile(re.escape(mk)) for mk in markers]
    for pat in patterns:
        r = subprocess.run(["git", "grep", "--cached", "-I", "-E", "-e", pat.pattern],
                           cwd=ROOT, capture_output=True, text=True)
        for line in r.stdout.splitlines():
            if not line:
                continue
            # 이메일 패턴 행은 워킹트리 검사(scan_text)와 같은 noreply
            # 예외를 적용한다 — 행 내 이메일 전부가 GitHub noreply면
            # 합법(작성자 식별 표준)으로 통과시킨다.
            if pat is EMAIL and EMAIL.search(line) and all(
                    email_allowed(a) for a in EMAIL.findall(line)):
                continue
            print("FAIL(스테이지) " + line[:100])
            hits.append(line)
    return hits


def main() -> int:
    markers = local_markers()
    failed = bool(scan_working_tree(ls_files(), markers))
    if "--cached" in sys.argv:
        failed = bool(scan_index_blobs(markers)) or failed
    if failed:
        print("치환/제거 후 재시도 — 기준: scripts/check_personal.py (로컬 마커: ~/.config/flint/personal-markers.txt)")
        return 1
    print(f"OK 개인 데이터 없음 (추적 파일 스캔·로컬 마커 {len(markers)}종)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
