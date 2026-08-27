"""Stage 2 — build the retrieval corpus (chunk + metadata) and index it.

The corpus is deliberately *curated*, not a blind tree walk (plan §"不盲扫整树"):
a whitelist of mechanism-bearing GCC files plus the Bugzilla entries the survey
already catalogued. Three chunkers, each emitting ``Chunk`` objects carrying
provenance metadata:

- ``_chunk_c_functions`` — GNU-C source: one chunk per "leading comment block +
  function signature + body". The column-0 opening/closing brace convention of
  GNU style makes the boundaries reliable without a real C parser.
- ``_chunk_md`` — machine-description ``.md``: one chunk per ``(define_* "name")``.
- whole-file — small headers (``cet.h``) indexed as one chunk.

Bugzilla bodies are fetched from the GCC REST API and cached under
``cache/bugzilla/<id>.json`` so a run is reproducible / offline-replayable. Each
bug is one chunk (summary + the report comment).

Every chunk gets a ``mechanism`` tag. For the mechanism-neutral middle-end and
for multi-mechanism backends the tag is refined per-chunk from symbol / text
keywords (``_classify_mechanism``) so the cross-mechanism statistics that RQ1
depends on are meaningful, and so the exit filter's "same mechanism = rediscover
self" test is accurate.
"""

from __future__ import annotations

import json
import re
import time
import urllib.request
from pathlib import Path

from .schema import Chunk, ChunkMeta

GCC_VERSION = "gcc-16.1.0"

# --- source-file whitelist -------------------------------------------------
# (path relative to the gcc/ root, default mechanism, isa). The default
# mechanism is a fallback; per-chunk refinement (_classify_mechanism) overrides
# it when a chunk's symbol/text names a specific mechanism.
_MIDDLE_END: tuple[tuple[str, str, str], ...] = (
    ("tree-object-size.cc", "fortify-source", "generic"),
    ("builtins.cc", "fortify-source", "generic"),
    ("tree-ssa-strlen.cc", "fortify-source", "generic"),
    ("gimple-fold.cc", "fortify-source", "generic"),
    ("cfgexpand.cc", "stack-protector", "generic"),
    ("function.cc", "stack-protector", "generic"),
    ("explow.cc", "stack-clash-protection", "generic"),
    ("ipa-strub.cc", "strub", "generic"),
)
_BACKEND_CC: tuple[tuple[str, str, str], ...] = (
    ("config/aarch64/aarch64.cc", "backend-multi", "aarch64"),
    ("config/i386/i386.cc", "backend-multi", "x86_64"),
    ("config/riscv/riscv.cc", "backend-multi", "riscv64"),
    ("config/arm/arm.cc", "backend-multi", "arm"),
)
_BACKEND_MD: tuple[tuple[str, str, str], ...] = (
    ("config/aarch64/aarch64.md", "backend-multi", "aarch64"),
    ("config/arm/arm.md", "backend-multi", "arm"),
    ("config/i386/predicates.md", "cet", "x86_64"),
)
_HEADERS: tuple[tuple[str, str, str], ...] = (
    ("config/i386/cet.h", "cet", "x86_64"),
)

# LLVM files are selected from the evidence paths named by the curated LLVM
# historical-bug corpus.  This is intentionally a small, explicit adapter: a
# recursive llvm-project walk would mix tests, vendored code, and unrelated
# subsystems into the retrieval index.
_LLVM_SOURCES: tuple[tuple[str, str, str], ...] = (
    ("llvm/lib/CodeGen/StackProtector.cpp", "stack-protector", ""),
    ("llvm/lib/CodeGen/SafeStack.cpp", "safestack", ""),
    ("llvm/lib/CodeGen/SelectionDAG/SelectionDAGBuilder.cpp", "bti", ""),
    ("llvm/lib/Target/AArch64/AArch64BranchTargets.cpp", "bti", "aarch64"),
    ("llvm/lib/Target/AArch64/AArch64PrologueEpilogue.cpp", "return-address-signing", "aarch64"),
    ("llvm/lib/Target/ARM/ARMISelLowering.cpp", "cmse", ""),
    ("llvm/lib/Target/ARM/ARMExpandPseudoInsts.cpp", "cmse", ""),
    ("llvm/lib/Target/ARM/ARMFrameLowering.cpp", "codegen", ""),
    ("llvm/lib/Target/ARM/ARMLoadStoreOptimizer.cpp", "codegen", ""),
    ("llvm/lib/Target/RISCV/RISCVTargetObjectFile.cpp", "codegen", ""),
    ("llvm/lib/Target/X86/X86ISelLowering.cpp", "codegen", "x86_64"),
    ("llvm/lib/Target/X86/X86IndirectBranchTracking.cpp", "ibt", "x86_64"),
    ("clang/lib/CodeGen/Targets/AArch64.cpp", "return-address-signing", "aarch64"),
    ("clang/lib/CodeGen/Targets/ARM.cpp", "bti", ""),
    ("compiler-rt/lib/sanitizer_common/sanitizer_file.cpp", "asan", ""),
    ("compiler-rt/lib/sanitizer_common/sanitizer_posix_libcdep.cpp", "asan", ""),
)
_LLVM_HEADERS: tuple[tuple[str, str, str], ...] = (
    ("clang/include/clang/Basic/TargetInfo.h", "bti", ""),
)
_LLVM_CHUNK_LINES = 80

# --- Bugzilla whitelist ----------------------------------------------------
# GCC PR id -> mechanism, taken from the local bug corpus front-matter plus the
# survey's silent-bypass anchor table (§"已知 silent-bypass 锚点").
_BUGZILLA: dict[int, str] = {
    84145: "cet",
    102035: "cmse",
    104540: "codegen",
    109267: "codegen",
    116305: "codegen",
    117920: "codegen",
    84039: "codegen",
    87414: "codegen",
    88917: "codegen",
    104380: "fortify-source",
    120929: "fortify-source",
    38454: "fortify-source",
    61886: "fortify-source",
    87525: "fortify-source",
    87672: "fortify-source",
    93262: "fortify-source",
    96350: "ibt",
    113780: "return-address-signing",
    94514: "return-address-signing",
    94515: "return-address-signing",
    94791: "return-address-signing",
    94891: "return-address-signing",
    83109: "shstk",
    84239: "shstk",
    83641: "stack-clash-protection",
    64820: "stack-protector",
    81708: "stack-protector",
    96191: "stack-protector",
    # survey-named silent-bypass anchors not in the local bug corpus:
    85434: "stack-protector",
    111703: "stack-protector",
}

_BUGZILLA_REST = "https://gcc.gnu.org/bugzilla/rest/bug"
_MAX_BUG_CHARS = 8000

# Per-chunk mechanism classifier. Ordered: first keyword group that matches wins.
_MECH_KEYWORDS: tuple[tuple[str, tuple[str, ...]], ...] = (
    ("stack-clash-protection", ("stack_clash", "probe_stack", "anti_adjust", "guard page")),
    ("stack-protector", ("stack_protect", "stack_chk", "canary", "ssp_")),
    ("return-address-signing", ("paciasp", "autiasp", "pac_ret", "return_address_sign",
                                "branch_protection", "ptrauth", "sign_return")),
    ("bti", ("bti_c", "bti_j", "branch_target", "gen_bti")),
    ("cet", ("endbr", "cet", "ibt", "shstk", "notrack")),
    ("shadowcallstack", ("shadow_call", "scs_push", "scs_pop", "ffixed-x18")),
    ("fortify-source", ("object_size", "_chk", "access_with_size", "counted_by", "fortif")),
)


def _classify_mechanism(symbol: str, text: str, default: str) -> str:
    """Refine a chunk's mechanism from its symbol / text; fall back to default."""
    hay = f"{symbol}\n{text}".lower()
    for mech, kws in _MECH_KEYWORDS:
        if any(k in hay for k in kws):
            return mech
    return default


# --- source chunker (GNU C style) -----------------------------------------
_SIG_STOP = (";", "}")


def _extract_symbol(sig: str) -> str:
    m = re.search(r"([A-Za-z_]\w*)\s*\(", sig)
    return m.group(1) if m else ""


def _chunk_c_functions(
    text: str,
    path: str,
    mechanism: str,
    isa: str,
    version: str = GCC_VERSION,
) -> list[Chunk]:
    lines = text.split("\n")
    n = len(lines)
    chunks: list[Chunk] = []
    i = 0
    while i < n:
        line = lines[i]
        # A function body opens with a column-0 brace: either a bare "{" line
        # or a one-line "type name(args) {" (rare in GNU style but handled).
        opens = line.startswith("{") or (
            line[:1].strip() != "" and line.rstrip().endswith("{") and "(" in line
        )
        if not opens:
            i += 1
            continue

        # Find the matching close: the next column-0 "}".
        k = i + 1
        while k < n and not lines[k].startswith("}"):
            k += 1
        if k >= n:
            break
        body_end = k

        # Walk up to collect the signature lines.
        if line.startswith("{"):
            sig_lines: list[str] = []
            j = i - 1
            while j >= 0:
                s = lines[j].rstrip()
                if s == "" or s.lstrip().startswith(("*", "/*", "//")):
                    break
                if s.endswith(_SIG_STOP):
                    break
                sig_lines.insert(0, lines[j])
                j -= 1
            sig_start = j + 1
        else:
            sig_lines = [line]
            sig_start = i
            j = i - 1

        sig = " ".join(x.strip() for x in sig_lines)
        symbol = _extract_symbol(sig)
        if not symbol or "(" not in sig:
            i = body_end + 1
            continue

        # Attach the immediately-preceding /* ... */ comment block, if any.
        start = sig_start
        if j >= 0 and lines[j].rstrip().endswith("*/"):
            m = j
            while m >= 0 and "/*" not in lines[m]:
                m -= 1
            if m >= 0:
                start = m

        body = "\n".join(lines[start : body_end + 1])
        mech = _classify_mechanism(symbol, body, mechanism)
        chunks.append(
            Chunk(
                chunk_id=f"{path}:{start + 1}:{symbol}",
                text=body,
                metadata=ChunkMeta(
                    source_kind="source",
                    mechanism=mech,
                    compiler="GCC",
                    version=version,
                    isa=isa,
                    path=path,
                    line=start + 1,
                    symbol=symbol,
                ),
            )
        )
        i = body_end + 1
    return chunks


# --- machine-description chunker ------------------------------------------
_MD_DEFINE = re.compile(r'^\(define_\w+\s+"?([^"\s\)]*)"?')


def _chunk_md(
    text: str,
    path: str,
    mechanism: str,
    isa: str,
    version: str = GCC_VERSION,
) -> list[Chunk]:
    lines = text.split("\n")
    n = len(lines)
    chunks: list[Chunk] = []
    starts: list[tuple[int, str]] = []
    for idx, line in enumerate(lines):
        m = _MD_DEFINE.match(line)
        if m:
            starts.append((idx, m.group(1) or "define"))
    for si, (start, name) in enumerate(starts):
        end = starts[si + 1][0] if si + 1 < len(starts) else n
        body = "\n".join(lines[start:end])
        mech = _classify_mechanism(name, body, mechanism)
        chunks.append(
            Chunk(
                chunk_id=f"{path}:{start + 1}:{name}",
                text=body,
                metadata=ChunkMeta(
                    source_kind="source",
                    mechanism=mech,
                    compiler="GCC",
                    version=version,
                    isa=isa,
                    path=path,
                    line=start + 1,
                    symbol=name,
                ),
            )
        )
    return chunks


def _chunk_whole(
    text: str,
    path: str,
    mechanism: str,
    isa: str,
    version: str = GCC_VERSION,
) -> list[Chunk]:
    return [
        Chunk(
            chunk_id=f"{path}:1",
            text=text,
            metadata=ChunkMeta(
                source_kind="source",
                mechanism=mechanism,
                compiler="GCC",
                version=version,
                isa=isa,
                path=path,
                line=1,
            ),
        )
    ]


def _chunk_llvm_lines(
    text: str,
    path: str,
    mechanism: str,
    isa: str,
    version: str,
    *,
    source_kind: str = "source",
) -> list[Chunk]:
    """Split LLVM C++ into exact, bounded line windows.

    LLVM source mixes free functions, class bodies, lambdas, preprocessor
    branches, and generated fragments. A regex claiming to find function
    boundaries silently mislabels nested calls as symbols. Fixed line windows
    are deliberately less clever: every byte is covered once, every starting
    line is exact, and chunk sizes remain bounded and replayable.
    """
    lines = text.splitlines()
    chunks: list[Chunk] = []
    for start in range(0, len(lines), _LLVM_CHUNK_LINES):
        end = min(start + _LLVM_CHUNK_LINES, len(lines))
        body = "\n".join(lines[start:end])
        if not body.strip():
            continue
        first_line = start + 1
        last_line = end
        chunks.append(
            Chunk(
                chunk_id=f"{path}:{first_line}:lines-{first_line}-{last_line}",
                text=body,
                metadata=ChunkMeta(
                    source_kind=source_kind,
                    mechanism=_classify_mechanism("", body, mechanism),
                    compiler="LLVM",
                    version=version,
                    isa=isa,
                    path=path,
                    line=first_line,
                    symbol="",
                ),
            )
        )
    return chunks


def _read(path: Path) -> str | None:
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return None


# --- Bugzilla fetch --------------------------------------------------------
def _http_json(url: str, *, retries: int = 3, timeout: int = 30) -> dict:
    last: Exception | None = None
    for attempt in range(retries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "specgen/1.0"})
            with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310
                return json.loads(resp.read().decode("utf-8"))
        except Exception as exc:  # network / json — retry then re-raise
            last = exc
            time.sleep(1.5 * (attempt + 1))
    raise RuntimeError(f"bugzilla fetch failed for {url}: {last}")


def fetch_bugzilla(bug_id: int, cache_root: Path) -> dict:
    """Return ``{summary, comment0}`` for a bug, caching the raw REST payload."""
    cache_dir = cache_root / "bugzilla"
    cache_dir.mkdir(parents=True, exist_ok=True)
    cache_file = cache_dir / f"{bug_id}.json"
    if cache_file.exists():
        return json.loads(cache_file.read_text(encoding="utf-8"))

    bug = _http_json(f"{_BUGZILLA_REST}/{bug_id}")["bugs"][0]
    comments = _http_json(f"{_BUGZILLA_REST}/{bug_id}/comment")
    key = next(iter(comments["bugs"].keys()))
    clist = comments["bugs"][key]["comments"]
    payload = {
        "id": bug_id,
        "summary": bug.get("summary", ""),
        "status": bug.get("status", ""),
        "resolution": bug.get("resolution", ""),
        "comment0": clist[0]["text"] if clist else "",
    }
    cache_file.write_text(json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8")
    return payload


def _bugzilla_chunk(bug_id: int, mechanism: str, cache_root: Path) -> Chunk:
    data = fetch_bugzilla(bug_id, cache_root)
    text = f"PR{bug_id}: {data['summary']}\n\n{data['comment0']}"[:_MAX_BUG_CHARS]
    url = f"https://gcc.gnu.org/bugzilla/show_bug.cgi?id={bug_id}"
    return Chunk(
        chunk_id=f"bugzilla:{bug_id}",
        text=text,
        metadata=ChunkMeta(
            source_kind="bug-disclosure",
            mechanism=mechanism,
            compiler="GCC",
            version="",
            isa="",
            path=url,
            line=0,
            symbol=f"PR{bug_id}",
        ),
    )


# --- top-level build -------------------------------------------------------
def _build_gcc_corpus(
    gcc_root: Path,
    *,
    cache_root: Path,
    include_bugzilla: bool = True,
    version: str = GCC_VERSION,
) -> list[Chunk]:
    """Chunk the whitelisted GCC files (+ Bugzilla) into the retrieval corpus."""
    chunks: list[Chunk] = []

    for rel, mech, isa in _MIDDLE_END + _BACKEND_CC:
        text = _read(gcc_root / rel)
        if text is not None:
            chunks.extend(_chunk_c_functions(text, rel, mech, isa, version))
    for rel, mech, isa in _BACKEND_MD:
        text = _read(gcc_root / rel)
        if text is not None:
            chunks.extend(_chunk_md(text, rel, mech, isa, version))
    for rel, mech, isa in _HEADERS:
        text = _read(gcc_root / rel)
        if text is not None:
            chunks.extend(_chunk_whole(text, rel, mech, isa, version))

    if include_bugzilla:
        for bug_id, mech in sorted(_BUGZILLA.items()):
            try:
                chunks.append(_bugzilla_chunk(bug_id, mech, cache_root))
            except RuntimeError:
                # A single unreachable bug must not sink a whole corpus build.
                continue

    return chunks


def _build_llvm_corpus(llvm_root: Path, *, version: str) -> list[Chunk]:
    chunks: list[Chunk] = []
    for rel, mechanism, isa in _LLVM_SOURCES:
        text = _read(llvm_root / rel)
        if text is not None:
            chunks.extend(_chunk_llvm_lines(text, rel, mechanism, isa, version))
    for rel, mechanism, isa in _LLVM_HEADERS:
        text = _read(llvm_root / rel)
        if text is not None:
            chunks.extend(
                _chunk_llvm_lines(
                    text, rel, mechanism, isa, version, source_kind="header"
                )
            )
    return chunks


def curated_source_paths(corpus_root: Path, compiler: str) -> list[Path]:
    """Return the ordered, explicit source inputs used by an adapter."""

    normalized = compiler.strip().lower()
    if normalized == "gcc":
        entries = _MIDDLE_END + _BACKEND_CC + _BACKEND_MD + _HEADERS
    elif normalized == "llvm":
        entries = _LLVM_SOURCES + _LLVM_HEADERS
    else:
        raise ValueError("compiler must be 'gcc' or 'llvm'")
    return [corpus_root / rel for rel, _mechanism, _isa in entries]


def build_corpus(
    corpus_root: Path,
    *,
    cache_root: Path,
    include_bugzilla: bool = True,
    compiler: str = "gcc",
    version: str | None = None,
) -> list[Chunk]:
    """Build the curated corpus for one compiler family.

    The default call is byte-for-byte compatible with the original GCC
    adapter. LLVM has its own explicit whitelist and never contacts GCC
    Bugzilla.
    """

    normalized = compiler.strip().lower()
    if normalized == "gcc":
        return _build_gcc_corpus(
            corpus_root,
            cache_root=cache_root,
            include_bugzilla=include_bugzilla,
            version=version or GCC_VERSION,
        )
    if normalized == "llvm":
        return _build_llvm_corpus(corpus_root, version=version or "")
    raise ValueError("compiler must be 'gcc' or 'llvm'")


def write_corpus(chunks: list[Chunk], out_path: Path) -> None:
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with out_path.open("w", encoding="utf-8") as fh:
        for c in chunks:
            fh.write(c.model_dump_json() + "\n")


def load_corpus(path: Path) -> list[Chunk]:
    chunks: list[Chunk] = []
    with path.open(encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line:
                chunks.append(Chunk.model_validate_json(line))
    return chunks
