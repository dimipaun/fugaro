"""The YAML block reader the lift checks share.

`fugaro.yaml` is a hand-edited file of nested mappings, and the lift checks
(Check 910's snapshot and Check 912's refusal) need the text of one key's block
and the list of the lifted keys a file writes — not a value through a resolver.
A key's own text, comments dropped and blank lines gone, is compared as it
stands: it is what the migration reads and what the drop removes, and a
comparison of text is honest about a change a value parser would normalize away.

Both checks read the same file the same way, so the reader lives here rather
than in each of them: two copies of it drift, and a drift between a snapshot and
the refusal it is compared with is a reading nobody can act on. What stays with
each check is the list of lifted keys it judges and what it makes of an answer
(`LiftedKeys` is `internal/config/lifted.go`'s; the checks carry their own
spelling of it, keyed to the file and not to the Go type).

Imported from a check's own `py` program, which puts `LIVE_CHECK_LIB` (the
directory this file is in) on `sys.path`.
"""

import re


def read_text(path):
    try:
        with open(path, errors="replace") as fh:
            return fh.read()
    except Exception:
        return ""


def flat(text):
    """Whitespace flattened, and `|` turned away from the row separator the
    checks' result blocks are built from: every judgement is made on this,
    because a sentence the terminal wrapped is the same sentence."""
    return re.sub(r"\s+", " ", (text or "").replace("|", "/")).strip()


def cut_comment(line):
    """One line with its comment gone, a quoted `#` kept: a key a person
    commented out is a key the file does not write, and a `#` inside a quoted
    value is part of the value."""
    out, q = [], None
    for i, c in enumerate(line):
        if q:
            out.append(c)
            if c == q:
                q = None
        elif c in "\"'":
            q = c
            out.append(c)
        elif c == "#" and (i == 0 or line[i - 1] in " \t"):
            break
        else:
            out.append(c)
    return "".join(out).rstrip()


def block_spans(lines):
    """Every key path the document writes, mapped to where its block starts and
    at which indentation, by the file's own indentation: a YAML node reader
    would answer a shape, and the checks judge paths (`agent.models.coder`) and
    the text under one, not a value."""
    spans, stack = {}, []
    for i, raw in enumerate(lines):
        line = cut_comment(raw)
        text = line.strip()
        if not text or text in ("---", "..."):
            continue
        indent = len(line) - len(line.lstrip(" \t"))
        if text.startswith("-"):
            key = "-"
        else:
            m = re.match(r"^([^:\s][^:]*):(\s|$)", text)
            if not m:
                continue
            key = m.group(1).strip()
        while stack and stack[-1][0] >= indent:
            stack.pop()
        path = tuple(stack[-1][1]) + (key,) if stack else (key,)
        if path not in spans:
            spans[path] = (i, indent)
        stack.append((indent, path))
    return spans


def children(spans, path):
    """The keys written directly under one path, in path order."""
    n = len(path)
    return sorted({p for p in spans if len(p) == n + 1 and p[:n] == path and p[-1] != "-"})
