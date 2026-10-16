"""Backward-compatibility facade for defuzz_loop.admission.

The implementation now lives in defuzz_loop.core.admission.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.admission")
_sys.modules[__name__] = _real_mod
