"""Backward-compatibility facade for defuzz_loop.experiments_cli.

The modular command line dispatcher implementation now lives in defuzz_loop.cli.main.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.cli.main")
_sys.modules[__name__] = _real_mod

if __name__ == "__main__":
    _sys.exit(_real_mod.main())
