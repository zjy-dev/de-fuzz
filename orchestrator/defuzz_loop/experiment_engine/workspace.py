"""Backward-compatibility facade for defuzz_loop.experiment_engine.workspace.

The implementation now lives in defuzz_loop.core.workspace.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.workspace")
_sys.modules[__name__] = _real_mod
