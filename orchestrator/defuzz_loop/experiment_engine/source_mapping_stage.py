"""Backward-compatibility facade for defuzz_loop.experiment_engine.source_mapping_stage.

The implementation now lives in defuzz_loop.stages.s3_source_mapping.stage.
"""

import importlib as _importlib
import sys as _sys

_real_mod = _importlib.import_module("defuzz_loop.stages.s3_source_mapping.stage")
_sys.modules[__name__] = _real_mod
