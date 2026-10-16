"""Backward-compatibility facade for defuzz_loop.token_usage.

The implementation now lives in defuzz_loop.core.token_accounting.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.token_accounting")
_sys.modules[__name__] = _real_mod
