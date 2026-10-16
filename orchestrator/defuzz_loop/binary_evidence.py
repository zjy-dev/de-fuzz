"""Backward-compatibility facade for defuzz_loop.binary_evidence.

The implementation now lives in defuzz_loop.core.binary_evidence.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.binary_evidence")
_sys.modules[__name__] = _real_mod
