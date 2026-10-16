"""Backward-compatibility facade for defuzz_loop.audit_schema.

The implementation now lives in defuzz_loop.core.audit_schema.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.core.audit_schema")
_sys.modules[__name__] = _real_mod
