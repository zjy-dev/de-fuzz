"""Source-mapping stage: driving the source-mapper OpenCode SubAgent from Python.

Given a single :class:`~defuzz_loop.opencode.schemas.Invariant`, this module
spawns the ``source-mapper`` OpenCode subagent inside the compiler source
tree and returns a validated
:class:`~defuzz_loop.opencode.schemas.SourceMappingResult`.

This is Step 3 of the DeFuzz method (see the paper's method overview:
"把不变量映射到编译器源代码"). It is intentionally a **stateless one-shot**;
retries are bounded and no cross-call caching happens here.
"""

from .mapper import (
    SourceMappingError,
    SourceMappingRunner,
    map_invariant_to_source,
)

__all__ = [
    "SourceMappingError",
    "SourceMappingRunner",
    "map_invariant_to_source",
]
