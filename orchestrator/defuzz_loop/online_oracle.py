"""Backward-compatibility facade for defuzz_loop.online_oracle.

The implementation now lives in defuzz_loop.stages.s5_agent_audit.online_oracle.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s5_agent_audit.online_oracle")
_sys.modules[__name__] = _real_mod
