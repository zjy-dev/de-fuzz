"""Backward-compatibility facade for defuzz_loop.experiment_engine.log_instrumentation_stage.

The implementation now lives in defuzz_loop.stages.s4_log_instrumentation.stage.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s4_log_instrumentation.stage")
_sys.modules[__name__] = _real_mod
