"""Backward-compatibility facade for defuzz_loop.experiment_engine.gcc_security_corpus.

The implementation now lives in defuzz_loop.stages.s1_invariants.corpus.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s1_invariants.corpus")
_sys.modules[__name__] = _real_mod

if __name__ == "__main__":
    _sys.exit(_real_mod.main())
