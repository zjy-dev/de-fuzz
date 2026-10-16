"""Backward-compatibility facade for defuzz_loop.experiment_engine.agent_backend.

The implementation now lives in defuzz_loop.backends.base.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.backends.base")
_sys.modules[__name__] = _real_mod
