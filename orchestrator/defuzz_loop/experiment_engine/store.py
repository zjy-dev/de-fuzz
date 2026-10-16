"""Backward-compatibility facade for defuzz_loop.experiment_engine.store.

The implementation now lives in defuzz_loop.core.store.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.store")
_sys.modules[__name__] = _real_mod
