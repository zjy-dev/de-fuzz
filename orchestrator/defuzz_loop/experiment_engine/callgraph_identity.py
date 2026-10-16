"""Backward-compatibility facade for defuzz_loop.experiment_engine.callgraph_identity.

The implementation now lives in defuzz_loop.stages.s3_source_mapping.callgraph.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s3_source_mapping.callgraph")
_sys.modules[__name__] = _real_mod
