"""Backward-compatibility facade for defuzz_loop.experiment_engine.compiler_rebuild.

The implementation now lives in defuzz_loop.stages.s4_log_instrumentation.rebuild.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s4_log_instrumentation.rebuild")
_sys.modules[__name__] = _real_mod
