"""Client stdout-parsing tests. Pure offline, no OpenCode subprocess."""

from __future__ import annotations

import pytest

from defuzz_loop.opencode.client import (
    OpenCodeInvocationError,
    _extract_final_assistant_text,
    extract_json_block,
)


def test_extract_json_block_happy_path() -> None:
    text = (
        "Here is the mapping:\n\n"
        "```json\n"
        '{"phi_id": "SP-001", "compiler": "gcc"}\n'
        "```\n"
    )
    out = extract_json_block(text)
    assert out.startswith("{")
    assert '"phi_id": "SP-001"' in out


def test_extract_json_block_bare_object_fallback() -> None:
    text = '{"phi_id": "X", "compiler": "gcc"}'
    out = extract_json_block(text)
    assert out.startswith("{")


def test_extract_json_block_missing_raises() -> None:
    with pytest.raises(OpenCodeInvocationError):
        extract_json_block("nope, no json here")


def test_extract_final_assistant_text_from_line_delimited_events() -> None:
    """Simulate opencode --format json stdout with three events."""

    stdout = "\n".join(
        [
            '{"type": "message.start", "role": "assistant"}',
            '{"type": "message.delta", "role": "assistant", "text": "Thinking..."}',
            '{"type": "message", "role": "assistant", "text": "Final answer here."}',
            "",
        ]
    )
    got = _extract_final_assistant_text(stdout)
    assert got == "Final answer here."


def test_extract_final_assistant_text_content_array_shape() -> None:
    stdout = (
        '{"role":"assistant","content":['
        '{"type":"text","text":"hi"}, '
        '{"type":"text","text":"there"}]}\n'
    )
    got = _extract_final_assistant_text(stdout)
    assert got == "hithere"


def test_extract_final_assistant_text_falls_back_to_inline_json_block() -> None:
    """Even if stdout is not line-delimited JSON, we should still recover
    a fenced ```json ...``` block."""

    stdout = 'Some banner\n```json\n{"phi_id":"Y"}\n```\nEOF\n'
    got = _extract_final_assistant_text(stdout)
    assert '"phi_id":"Y"' in got


def test_extract_final_assistant_text_ignores_non_assistant_roles() -> None:
    stdout = "\n".join(
        [
            '{"role": "user", "text": "should be ignored"}',
            '{"role": "assistant", "text": "kept"}',
        ]
    )
    assert _extract_final_assistant_text(stdout) == "kept"
