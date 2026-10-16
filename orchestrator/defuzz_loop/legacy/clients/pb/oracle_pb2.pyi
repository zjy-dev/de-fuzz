from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Verdict(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    VERDICT_PASS: _ClassVar[Verdict]
    VERDICT_FAIL: _ClassVar[Verdict]
    VERDICT_NOT_APPLICABLE: _ClassVar[Verdict]
    VERDICT_ERROR: _ClassVar[Verdict]
VERDICT_PASS: Verdict
VERDICT_FAIL: Verdict
VERDICT_NOT_APPLICABLE: Verdict
VERDICT_ERROR: Verdict

class Seed(_message.Message):
    __slots__ = ("id", "source", "parent_id", "selected_checkers")
    ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    PARENT_ID_FIELD_NUMBER: _ClassVar[int]
    SELECTED_CHECKERS_FIELD_NUMBER: _ClassVar[int]
    id: str
    source: str
    parent_id: str
    selected_checkers: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, id: _Optional[str] = ..., source: _Optional[str] = ..., parent_id: _Optional[str] = ..., selected_checkers: _Optional[_Iterable[str]] = ...) -> None: ...

class InvariantResult(_message.Message):
    __slots__ = ("id", "category", "verdict", "evidence", "detail", "reason", "isa")
    class DetailEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ID_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    VERDICT_FIELD_NUMBER: _ClassVar[int]
    EVIDENCE_FIELD_NUMBER: _ClassVar[int]
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    ISA_FIELD_NUMBER: _ClassVar[int]
    id: str
    category: str
    verdict: Verdict
    evidence: str
    detail: _containers.ScalarMap[str, str]
    reason: str
    isa: str
    def __init__(self, id: _Optional[str] = ..., category: _Optional[str] = ..., verdict: _Optional[_Union[Verdict, str]] = ..., evidence: _Optional[str] = ..., detail: _Optional[_Mapping[str, str]] = ..., reason: _Optional[str] = ..., isa: _Optional[str] = ...) -> None: ...

class BuildCell(_message.Message):
    __slots__ = ("checker_id", "isa")
    CHECKER_ID_FIELD_NUMBER: _ClassVar[int]
    ISA_FIELD_NUMBER: _ClassVar[int]
    checker_id: str
    isa: str
    def __init__(self, checker_id: _Optional[str] = ..., isa: _Optional[str] = ...) -> None: ...

class BuildRequest(_message.Message):
    __slots__ = ("seed", "cells")
    SEED_FIELD_NUMBER: _ClassVar[int]
    CELLS_FIELD_NUMBER: _ClassVar[int]
    seed: Seed
    cells: _containers.RepeatedCompositeFieldContainer[BuildCell]
    def __init__(self, seed: _Optional[_Union[Seed, _Mapping]] = ..., cells: _Optional[_Iterable[_Union[BuildCell, _Mapping]]] = ...) -> None: ...

class BuildArtifact(_message.Message):
    __slots__ = ("cell", "binary_path", "success", "error")
    CELL_FIELD_NUMBER: _ClassVar[int]
    BINARY_PATH_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    cell: BuildCell
    binary_path: str
    success: bool
    error: str
    def __init__(self, cell: _Optional[_Union[BuildCell, _Mapping]] = ..., binary_path: _Optional[str] = ..., success: _Optional[bool] = ..., error: _Optional[str] = ...) -> None: ...

class BuildResponse(_message.Message):
    __slots__ = ("artifacts",)
    ARTIFACTS_FIELD_NUMBER: _ClassVar[int]
    artifacts: _containers.RepeatedCompositeFieldContainer[BuildArtifact]
    def __init__(self, artifacts: _Optional[_Iterable[_Union[BuildArtifact, _Mapping]]] = ...) -> None: ...

class CoverageRequest(_message.Message):
    __slots__ = ("artifacts", "cumulative_state")
    ARTIFACTS_FIELD_NUMBER: _ClassVar[int]
    CUMULATIVE_STATE_FIELD_NUMBER: _ClassVar[int]
    artifacts: _containers.RepeatedCompositeFieldContainer[BuildArtifact]
    cumulative_state: bytes
    def __init__(self, artifacts: _Optional[_Iterable[_Union[BuildArtifact, _Mapping]]] = ..., cumulative_state: _Optional[bytes] = ...) -> None: ...

class CoverageResponse(_message.Message):
    __slots__ = ("cumulative_state", "delta_json")
    CUMULATIVE_STATE_FIELD_NUMBER: _ClassVar[int]
    DELTA_JSON_FIELD_NUMBER: _ClassVar[int]
    cumulative_state: bytes
    delta_json: str
    def __init__(self, cumulative_state: _Optional[bytes] = ..., delta_json: _Optional[str] = ...) -> None: ...

class OracleRequest(_message.Message):
    __slots__ = ("seed", "artifacts")
    SEED_FIELD_NUMBER: _ClassVar[int]
    ARTIFACTS_FIELD_NUMBER: _ClassVar[int]
    seed: Seed
    artifacts: _containers.RepeatedCompositeFieldContainer[BuildArtifact]
    def __init__(self, seed: _Optional[_Union[Seed, _Mapping]] = ..., artifacts: _Optional[_Iterable[_Union[BuildArtifact, _Mapping]]] = ...) -> None: ...

class OracleResponse(_message.Message):
    __slots__ = ("results", "violated", "failing_checker", "failing_isa", "evidence")
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    VIOLATED_FIELD_NUMBER: _ClassVar[int]
    FAILING_CHECKER_FIELD_NUMBER: _ClassVar[int]
    FAILING_ISA_FIELD_NUMBER: _ClassVar[int]
    EVIDENCE_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[InvariantResult]
    violated: bool
    failing_checker: str
    failing_isa: str
    evidence: str
    def __init__(self, results: _Optional[_Iterable[_Union[InvariantResult, _Mapping]]] = ..., violated: _Optional[bool] = ..., failing_checker: _Optional[str] = ..., failing_isa: _Optional[str] = ..., evidence: _Optional[str] = ...) -> None: ...

class CheckerMetadata(_message.Message):
    __slots__ = ("id", "applicable_isas", "mode", "cost", "category")
    ID_FIELD_NUMBER: _ClassVar[int]
    APPLICABLE_ISAS_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    COST_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    id: str
    applicable_isas: _containers.RepeatedScalarFieldContainer[str]
    mode: str
    cost: str
    category: str
    def __init__(self, id: _Optional[str] = ..., applicable_isas: _Optional[_Iterable[str]] = ..., mode: _Optional[str] = ..., cost: _Optional[str] = ..., category: _Optional[str] = ...) -> None: ...

class ListCheckerMetadataRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListCheckerMetadataResponse(_message.Message):
    __slots__ = ("checkers",)
    CHECKERS_FIELD_NUMBER: _ClassVar[int]
    checkers: _containers.RepeatedCompositeFieldContainer[CheckerMetadata]
    def __init__(self, checkers: _Optional[_Iterable[_Union[CheckerMetadata, _Mapping]]] = ...) -> None: ...
