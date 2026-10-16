"""Backward-compatibility facade for defuzz_loop.experiment_engine.models.

The implementation now lives in defuzz_loop.core.models.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.models")
_sys.modules[__name__] = _real_mod
