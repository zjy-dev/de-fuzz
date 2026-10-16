"""Backward-compatibility facade for defuzz_loop.prompt_bundle.

The implementation now lives in defuzz_loop.core.prompt_bundle.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.prompt_bundle")
_sys.modules[__name__] = _real_mod
