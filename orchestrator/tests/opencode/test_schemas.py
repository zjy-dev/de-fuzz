"""Pydantic schema tests — pure offline, no OpenCode required."""

from __future__ import annotations

import json

import pytest

from defuzz_loop.opencode.schemas import Invariant, SourceMappingResult

VALID_MAPPING_JSON = """
{
  "phi_id": "SP-001",
  "compiler": "gcc",
  "mapping_confidence": "high",
  "level_1_pass": {
    "name": "pass_stack_protect",
    "file": "gcc/cfgexpand.cc",
    "line_start": 6521,
    "line_end": 6688,
    "evidence": "pass_data structure + gate() check"
  },
  "level_2_functions": [
    {
      "name": "expand_used_vars",
      "file": "gcc/cfgexpand.cc",
      "line": 2183,
      "role": "decides whether to emit stack_protect_prologue"
    }
  ],
  "level_3_critical_structs": [],
  "level_3_critical_conditions": [
    {
      "expression": "flag_stack_protect > 0 && has_vla",
      "file": "gcc/cfgexpand.cc",
      "line": 6604,
      "role": "false-negative disables SP on VLA fns"
    }
  ],
  "evidence_paths": ["gcc/cfgexpand.cc"],
  "unresolved_notes": []
}
"""


def test_invariant_prompt_payload_roundtrip() -> None:
    inv = Invariant(
        phi_id="SP-001",
        phi_text="For any function with a VLA...",
        mechanism="stack_canary",
        compiler="gcc",
        isa="x86_64",
        src_root="/opt/gcc-src",
    )
    payload = inv.as_prompt_payload()
    assert payload.startswith("<invariant>") and payload.endswith("</invariant>")
    inner = payload[len("<invariant>") : -len("</invariant>")]
    parsed = json.loads(inner)
    assert parsed["phi_id"] == "SP-001"
    assert parsed["mechanism"] == "stack_canary"
    assert parsed["src_root"] == "/opt/gcc-src"


def test_source_mapping_result_valid_parse() -> None:
    result = SourceMappingResult.model_validate_json(VALID_MAPPING_JSON)
    assert result.phi_id == "SP-001"
    assert result.mapping_confidence == "high"
    assert result.level_1_pass.name == "pass_stack_protect"
    assert result.level_1_pass.line_start == 6521
    assert len(result.level_2_functions) == 1
    assert len(result.level_3_critical_conditions) == 1
    assert result.level_3_critical_conditions[0].expression == "flag_stack_protect > 0 && has_vla"


def test_source_mapping_confidence_enum() -> None:
    """Only high/medium/low are legal for mapping_confidence."""

    bad = json.loads(VALID_MAPPING_JSON)
    bad["mapping_confidence"] = "excellent"
    with pytest.raises(ValueError):  # pydantic ValidationError is a ValueError
        SourceMappingResult.model_validate(bad)


def test_source_mapping_lists_default_empty() -> None:
    """Optional list fields should default to []."""

    minimal = {
        "phi_id": "X-000",
        "compiler": "llvm",
        "mapping_confidence": "low",
        "level_1_pass": {
            "name": "SafeStackPass",
            "file": "llvm/lib/CodeGen/SafeStack.cpp",
            "line_start": 100,
            "line_end": 200,
            "evidence": "class SafeStack : public FunctionPass",
        },
    }
    result = SourceMappingResult.model_validate(minimal)
    assert result.level_2_functions == []
    assert result.level_3_critical_structs == []
    assert result.level_3_critical_conditions == []
    assert result.evidence_paths == []
    assert result.unresolved_notes == []
