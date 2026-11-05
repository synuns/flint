#!/usr/bin/env python3
"""hook_personal_guard.py — 하네스 PreToolUse(Bash) 가드.

stdin: 훅 JSON({"tool_input":{"command": ...}}). 판정·검사 전체를
python으로 수행(셸 방언·heredoc stdin 충돌 무관 — 실패는 닫힘).

차단 대상:
- git 상태변경(commit/push/add/…) 접두·체이닝·-C·플래그 무관 감지 후
  check_personal.py --cached(워킹트리+스테이지 blob) 실패 시 차단.
- --author 주입(작성자는 로컬 git config noreply 고정).
"""
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

GIT_STATE = re.compile(
    r"\bgit\b(?:\s+(?:-[A-Za-z-]+|--[A-Za-z-]+)(?:\s+(?:\"[^\"]*\"|'[^']*'|\S+))?)*"
    r"\s+(commit|push|add|cherry-pick|merge|update-index|restore|stage|stash|rebase|am|apply)\b"
)

# 따옴표 구간(커밋 메시지 등 산문) 제거 — 플래그 판정은 명령 부분만.
QUOTED = re.compile(r"\"[^\"]*\"|'[^']*'")


def fail(msg: str) -> None:
    print(msg, file=sys.stderr)
    sys.exit(2)


def main() -> None:
    try:
        d = json.load(sys.stdin)
        cmd = (d.get("tool_input") or {}).get("command", "") or ""
    except Exception:
        fail("BLOCK 훅 입력 파싱 실패(fail-closed)")
    bare = QUOTED.sub(" ", cmd)
    if not GIT_STATE.search(bare):
        return
    if "--author" in bare:
        fail("BLOCK --author 주입 금지 — 커밋 작성자는 로컬 git config(noreply)로 고정.")
    r = subprocess.run(
        [sys.executable, os.path.join(HERE, "check_personal.py"), "--cached"],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        fail("BLOCK: 개인 데이터 마커 감지 — scripts/check_personal.py 기준 위반. 치환 후 재시도.\n" + r.stdout.strip()[:800])


if __name__ == "__main__":
    main()
