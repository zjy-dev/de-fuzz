"""Binary evidence extraction for the LLM oracle (read-only, deterministic).

The deterministic FORTIFY disasm checkers (O01/O02/O03) only implement an x86_64
backend; on aarch64 they punt to NOT_APPLICABLE. To let the LLM oracle adjudicate
those invariants we hand it the same raw evidence the disasm backend would have
read: the binary's dynamic symbol/relocation view (does any `__<family>_chk`
symbol appear?) plus a disassembly of the functions that contain fortify call
sites. This module shells out to the host binutils (objdump / nm) already required
by the build toolchain; it never modifies the binary and degrades to empty
evidence rather than raising (R8).
"""

from __future__ import annotations

import re
import shutil
import subprocess
from dataclasses import dataclass, field

# libc fortify "__<family>_chk" wrappers we care about as a positive signal.
_CHK_RE = re.compile(r"__[A-Za-z0-9_]+_chk\b")
# Bare fortify-protected sinks whose presence (without a _chk sibling) is the
# W01 silent-bypass signal.
_PROTECTED_SINKS = (
    "memcpy", "memmove", "memset", "strcpy", "strncpy", "strcat", "strncat",
    "stpcpy", "sprintf", "snprintf", "vsprintf", "vsnprintf", "printf",
    "fprintf", "vprintf", "vfprintf",
)

_OBJDUMP_TIMEOUT = 60
_MAX_DISASM_CHARS = 24_000


@dataclass
class BinaryEvidence:
    """Read-only facts extracted from one built binary for LLM adjudication."""

    binary_path: str
    isa: str = ""
    available: bool = False
    chk_symbols: list[str] = field(default_factory=list)
    bare_sinks: list[str] = field(default_factory=list)
    disasm_excerpt: str = ""
    note: str = ""

    def render(self) -> str:
        if not self.available:
            return f"(no binary evidence: {self.note or 'unavailable'})"
        lines = [
            f"binary: {self.binary_path} (isa={self.isa or 'unknown'})",
            f"fortify __*_chk symbols present: {self.chk_symbols or 'NONE'}",
            f"bare protected libc sinks referenced: {self.bare_sinks or 'NONE'}",
            "",
            "disassembly (fortify call sites and neighbours):",
            self.disasm_excerpt or "(no disassembly captured)",
        ]
        return "\n".join(lines)


def _run(cmd: list[str]) -> str:
    try:
        out = subprocess.run(
            cmd, capture_output=True, text=True, timeout=_OBJDUMP_TIMEOUT, check=False
        )
    except (OSError, subprocess.SubprocessError):
        return ""
    return out.stdout


def _symbols(binary_path: str) -> tuple[list[str], list[str]]:
    """Return (chk symbols, bare protected sinks) referenced by the binary."""
    text = ""
    if shutil.which("nm"):
        # -D = dynamic symbols (the PLT imports), -u = undefined (the imports).
        text = _run(["nm", "-D", binary_path]) + "\n" + _run(["nm", "-Du", binary_path])
    if not text and shutil.which("objdump"):
        text = _run(["objdump", "-T", binary_path])

    chk: set[str] = set()
    bare: set[str] = set()
    for line in text.splitlines():
        for m in _CHK_RE.findall(line):
            chk.add(m)
        for token in line.replace("@", " ").split():
            if token in _PROTECTED_SINKS:
                bare.add(token)
    return sorted(chk), sorted(bare)


def _disasm_fortify_sites(binary_path: str) -> str:
    """Disassemble functions, keeping windows around fortify-relevant calls."""
    if not shutil.which("objdump"):
        return ""
    full = _run(["objdump", "-d", "--no-show-raw-insn", binary_path])
    if not full:
        return ""

    lines = full.splitlines()
    keep: list[int] = []
    interesting = set(_PROTECTED_SINKS)
    for i, line in enumerate(lines):
        low = line.lower()
        hit = (
            "_chk" in low
            or "movn" in low  # aarch64 "mov reg, #-1" form -> SIZE_MAX dstlen
            or any(f"<{s}" in low or f" {s}@" in low or f"\t{s}" in low for s in interesting)
        )
        if hit:
            for j in range(max(0, i - 6), min(len(lines), i + 3)):
                keep.append(j)

    if not keep:
        # Nothing matched; hand over the user functions' disassembly (main etc.)
        excerpt = full
    else:
        ordered = sorted(set(keep))
        excerpt = "\n".join(lines[k] for k in ordered)

    if len(excerpt) > _MAX_DISASM_CHARS:
        excerpt = excerpt[:_MAX_DISASM_CHARS] + "\n... (truncated)"
    return excerpt


def collect(binary_path: str, isa: str = "") -> BinaryEvidence:
    """Extract read-only fortify evidence from one binary; never raises."""
    ev = BinaryEvidence(binary_path=binary_path, isa=isa)
    if not binary_path:
        ev.note = "no binary path"
        return ev
    import os

    if not os.path.exists(binary_path):
        ev.note = "binary path does not exist (build produced no artifact)"
        return ev
    if not shutil.which("objdump") and not shutil.which("nm"):
        ev.note = "no binutils (objdump/nm) on PATH"
        return ev

    ev.chk_symbols, ev.bare_sinks = _symbols(binary_path)
    ev.disasm_excerpt = _disasm_fortify_sites(binary_path)
    ev.available = True
    return ev
