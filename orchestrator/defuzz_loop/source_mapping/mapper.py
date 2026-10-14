"""Source mapping through the shared backend, JSON repair and anchor validation."""

from __future__ import annotations

from pathlib import Path

from ..agents.contracts.schemas import Invariant, SourceMappingResult
from ..agents.runner import SubagentRunError, SubagentRunner
from ..experiment_engine.agent_backend import AgentBackend


class SourceMappingError(SubagentRunError):
    """The source-mapper failed its execution or validation contract."""


class SourceMappingRunner:
    def __init__(
        self,
        backend: AgentBackend,
        *,
        project_root: Path,
        output_dir: Path,
        max_retries: int = 2,
        timeout_secs: float = 600,
    ) -> None:
        self._backend = backend
        self._project_root = project_root
        self._output_dir = output_dir
        self._max_retries = max_retries
        self._timeout_secs = timeout_secs

    async def run(self, invariant: Invariant) -> SourceMappingResult:
        from ..experiment_engine.source_mapping_stage import validate_anchors

        source_root = Path(invariant.src_root).resolve(strict=True)
        runner = SubagentRunner(
            self._backend,
            cwd=source_root,
            output_dir=self._output_dir,
            timeout_secs=self._timeout_secs,
            max_retries=self._max_retries,
        )

        def validate(result: SourceMappingResult) -> SourceMappingResult:
            if result.phi_id != invariant.phi_id or result.compiler != invariant.compiler:
                raise ValueError("source-mapper invariant/compiler mismatch")
            if result.isa not in (None, invariant.isa):
                raise ValueError("source-mapper returned another ISA")
            result.isa = invariant.isa
            return validate_anchors(result, source_root)

        try:
            return await runner.run(
                "source-mapper",
                invariant.as_prompt_payload(),
                SourceMappingResult,
                metadata={
                    "use_callgraph": True, "compiler": invariant.compiler, "isa": invariant.isa,
                },
                validator=validate,
            )
        except SubagentRunError as exc:
            raise SourceMappingError(str(exc)) from exc


async def map_invariant_to_source(
    invariant: Invariant,
    *,
    project_root: Path,
    output_dir: Path,
    config_path: Path | None = None,
    backend: AgentBackend | None = None,
    model: str | None = None,
    timeout_secs: float = 600,
) -> SourceMappingResult:
    if backend is None:
        from ..experiment_engine.pi_backend import PiAgentBackend

        if config_path is None:
            raise ValueError("source mapping requires an explicit Pi config_path")
        backend = PiAgentBackend(config_path, model=model)
    return await SourceMappingRunner(
        backend,
        project_root=project_root,
        output_dir=output_dir,
        timeout_secs=timeout_secs,
    ).run(invariant)
