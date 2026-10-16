"""Backward-compatibility facade for defuzz_loop.candidate_verification.

The implementation now lives in defuzz_loop.core.candidate_verification.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.candidate_verification")
_sys.modules[__name__] = _real_mod
