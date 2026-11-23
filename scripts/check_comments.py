#!/usr/bin/env python3
import io
import os
import subprocess
import sys
import tokenize


def tracked(root):
    out = subprocess.run(
        ["git", "-C", root, "ls-files", "*.sh", "*.py"],
        capture_output=True, text=True, check=True,
    ).stdout
    return [line for line in out.splitlines() if line]


def heredoc_delim(line, i):
    if line[i:i + 3] == "<<<":
        return None
    if line[i:i + 2] != "<<":
        return None
    j = i + 2
    if j < len(line) and line[j] == "-":
        j += 1
    quote = None
    if j < len(line) and line[j] in "'\"":
        quote = line[j]
        j += 1
    k = j
    while k < len(line) and (line[k].isalnum() or line[k] == "_"):
        k += 1
    if k == j:
        return None
    if quote and line.find(quote, k) == -1:
        return None
    return line[j:k]


def scan_bash_line(line):
    i = 0
    quote = None
    while i < len(line):
        ch = line[i]
        if quote == '"':
            if ch == "\\":
                i += 2
                continue
            if ch == '"':
                quote = None
            i += 1
            continue
        if quote == "'":
            if ch == "'":
                quote = None
            i += 1
            continue
        if ch in "'\"":
            quote = ch
            i += 1
            continue
        if ch == "\\":
            i += 2
            continue
        if ch == "#":
            if (i == 0 or line[i - 1] in " \t") and not line.startswith("#!", i):
                return True
            i += 1
            continue
        i += 1
    return False


def scan_bash(text):
    hits = []
    delim = None
    for n, raw in enumerate(text.splitlines(), 1):
        if delim is not None:
            if raw.strip() == delim:
                delim = None
                continue
            if scan_bash_line(raw):
                hits.append((n, raw.strip()))
            continue
        ld = None
        i = 0
        quote = None
        while i < len(raw):
            ch = raw[i]
            if quote == '"':
                if ch == "\\":
                    i += 2
                    continue
                if ch == '"':
                    quote = None
                i += 1
                continue
            if quote == "'":
                if ch == "'":
                    quote = None
                i += 1
                continue
            if ch in "'\"":
                quote = ch
                i += 1
                continue
            if ch == "\\":
                i += 2
                continue
            if ch == "#":
                if (i == 0 or raw[i - 1] in " \t") and not raw.startswith("#!", i):
                    hits.append((n, raw.strip()))
                    break
                i += 1
                continue
            if ch == "<":
                d = heredoc_delim(raw, i)
                if d:
                    ld = d
                i += 1
                continue
            i += 1
        if ld is not None and delim is None:
            delim = ld
    return hits


def scan_python(text):
    hits = []
    for tok in tokenize.generate_tokens(io.StringIO(text).readline):
        if tok.type != tokenize.COMMENT:
            continue
        if tok.start[0] == 1 and tok.string.startswith("#!"):
            continue
        hits.append((tok.start[0], tok.string))
    return hits


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    failed = False
    for rel in tracked(root):
        path = os.path.join(root, rel)
        if not os.path.exists(path):
            continue
        with open(path, encoding="utf-8") as f:
            text = f.read()
        hits = scan_bash(text) if rel.endswith(".sh") else scan_python(text)
        for n, s in hits:
            print(f"{rel}:{n}: {s}")
            failed = True
    if failed:
        sys.exit(1)


if __name__ == "__main__":
    main()
