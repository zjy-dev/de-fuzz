"""Backward-compatibility facade for defuzz_loop.experiment_engine.checker_reuse.

The implementation now lives in defuzz_loop.stages.s2_checkers.reuse.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s2_checkers.reuse")
_sys.modules[__name__] = _real_mod
