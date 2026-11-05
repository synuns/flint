#!/usr/bin/env python3
"""check_personal noreply 스테이지 예외 회귀(v0.16.3 리뷰).

--cached 스테이지 blob 스캔(scan_index_blobs)이 워킹트리 스캔(scan_text)과
같은 GitHub noreply 예외를 적용하는지 — noreply만 포함한 스테이지 파일은
통과, 일반 이메일 스테이지 파일은 차단. 임시 git repo에서 scripts 복사본으로
실행해 flint 저장소 인덱스를 건드리지 않는다.

실행: python3 tests/regression/check_personal_noreply_test.py (exit 0=통과)
"""
import shutil
import subprocess
import tempfile
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "check_personal.py"


def run_case(files: dict) -> int:
    """files(name→content)를 스테이지한 임시 repo에서 --cached 실행."""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        (root / "scripts").mkdir()
        shutil.copy(SCRIPT, root / "scripts" / "check_personal.py")
        subprocess.run(["git", "init", "-q", "."], cwd=root, check=True)
        subprocess.run(["git", "config", "user.email", "t@t.invalid"],
                       cwd=root, check=True)
        subprocess.run(["git", "config", "user.name", "t"], cwd=root, check=True)
        for name, content in files.items():
            (root / name).write_text(content, encoding="utf-8")
            subprocess.run(["git", "add", name], cwd=root, check=True)
        return subprocess.run(
            ["python3", str(root / "scripts" / "check_personal.py"), "--cached"],
            cwd=root, capture_output=True, text=True).returncode


def main() -> int:
    noreply = "작성자: 12345+dev@users.noreply.github.com\n"
    email = "연락처: leak@example.com\n"
    failures = []

    if run_case({"noreply.md": noreply}) != 0:
        failures.append("noreply-only 스테이지 파일이 차단됨(예외 미적용)")
    if run_case({"leak.md": email}) == 0:
        failures.append("일반 이메일 스테이지 파일이 통과됨(차단 회귀)")
    if run_case({"mixed.md": noreply + email}) == 0:
        failures.append("noreply+일반 혼합 파일이 통과됨(혼합은 차단)")

    if failures:
        for f in failures:
            print("FAIL:", f)
        return 1
    print("OK noreply 스테이지 예외 3케이스 통과")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
