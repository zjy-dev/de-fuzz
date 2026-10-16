"""Backward-compatibility facade for defuzz_loop.checker_bundle.

The implementation now lives in defuzz_loop.core.checker_bundle.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.checker_bundle")
_sys.modules[__name__] = _real_mod
