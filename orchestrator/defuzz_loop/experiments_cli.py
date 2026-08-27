"""Unified command line dispatcher for reproducible DeFuzz experiments."""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import importlib
import json
import os
import shlex
import shutil
import subprocess
import sys
from collections.abc import Awaitable, Callable, Mapping, Sequence
from pathlib import Path
from typing import Any, Literal, cast

from defuzz_loop.audit_schema import normalize_isa, normalize_mechanism
from defuzz_loop.experiment_engine import (
    AgentBackend,
    AgentRequest,
    AgentResult,
    ExecAgentBackend,
    ExperimentPlan,
    HTTPAgentConfig,
    HTTPResponsesAgentBackend,
    RunStore,
    StageResult,
    TokenUsageSink,
    load_http_agent_config_snapshot,
)
from defuzz_loop.token_usage import TokenUsageContext, use_token_usage

EXIT_RUNTIME_FAILURE = 1
EXIT_CONFIGURATION_ERROR = 2
# Kept as a compatibility name for callers of the former scaffold CLI.
EXIT_BACKEND_UNAVAILABLE = EXIT_RUNTIME_FAILURE
_DEFAULT_OUTPUT_ROOT_DISPLAY = "orchestrator/runs/experiments"
_DEFAULT_REFERENCE_ROOT = Path("/Users/bytedance/projects/research/defend-reviewer/main")
_REQUIRED_REFERENCE_PATHS = (
    Path(".claude/agents/defend-reviewer.md"),
    Path("docs/prompts/full-review.md"),
    Path("docs/bugs"),
    Path("docs/invariants"),
)
_CHECKER_BUNDLE_MANIFEST = "checker-bundle-manifest.json"
_StageRunner = Callable[[ExperimentPlan, int, Path, AgentBackend | None], Awaitable[StageResult]]


def _as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, (str, Path)):
        return [value]
    return list(value)


class _LazyStageModule:
    """Load a stage only on execution, keeping optional deps out of --help."""

    def __init__(self, module: str) -> None:
        self._module = module

    async def run(
        self,
        plan: ExperimentPlan,
        repetition: int,
        output_dir: Path,
        backend: AgentBackend | None = None,
    ) -> StageResult:
        module = importlib.import_module(self._module)
        return await module.run(plan, repetition, output_dir, backend)


invariant_generation = _LazyStageModule("defuzz_loop.experiment_engine.invariant_generation")
checker_authoring = _LazyStageModule("defuzz_loop.experiment_engine.checker_authoring")
agent_audit = _LazyStageModule("defuzz_loop.agent_audit")


class _HelpFormatter(argparse.RawDescriptionHelpFormatter):
    """Keep prose readable while retaining argparse's aligned options."""


class _TokenSinkBackend:
    """Ensure external agent calls use the repetition-scoped usage sink."""

    def __init__(self, backend: AgentBackend, sink: TokenUsageSink) -> None:
        self._backend = backend
        self._sink = sink

    def __getattr__(self, name: str) -> Any:
        return getattr(self._backend, name)

    def _selected_sink(self, requested: Any) -> Any:
        if requested is None or requested is self._sink:
            return self._sink
        # Part II wraps the ambient sink to retain per-attempt call IDs. Sending
        # that wrapper through a fan-out sink would append the same call twice.
        if getattr(requested, "delegate", None) is self._sink:
            return requested
        return _CombinedTokenSink(requested, self._sink)

    async def run(self, request: AgentRequest) -> AgentResult:
        selected_sink = self._selected_sink(request.token_sink)
        scoped = request.model_copy(update={"token_sink": selected_sink})
        return await self._backend.run(scoped)

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        kwargs["token_sink"] = self._selected_sink(kwargs.get("token_sink"))
        complete = cast(Any, self._backend).complete
        return await complete(prompt, schema, **kwargs)


class _CombinedTokenSink:
    """Fan one external-agent usage event into stage and run-level sinks."""

    def __init__(self, stage_sink: Any, run_sink: Any) -> None:
        self._stage_sink = stage_sink
        self._run_sink = run_sink

    def record_external_usage(self, payload: Any, **kwargs: Any) -> Any:
        record = self._stage_sink.record_external_usage(payload, **kwargs)
        self._run_sink.record_external_usage(payload, **kwargs)
        return record


def _positive_int(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return parsed


def _positive_float(value: str) -> float:
    parsed = float(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return parsed


def _argv(value: str) -> list[str]:
    parsed = shlex.split(value)
    if not parsed:
        raise argparse.ArgumentTypeError("must contain at least one argument")
    return parsed


def _parser(*args: Any, **kwargs: Any) -> argparse.ArgumentParser:
    kwargs.setdefault("formatter_class", _HelpFormatter)
    return argparse.ArgumentParser(*args, **kwargs)


def _default_output_root() -> Path:
    cwd = Path.cwd()
    if (cwd / "orchestrator" / "pyproject.toml").is_file():
        return cwd / "orchestrator" / "runs" / "experiments"
    return cwd / "runs" / "experiments"


def _default_reference_root() -> Path:
    return Path(os.environ.get("DEFUZZ_REFERENCE_ROOT", _DEFAULT_REFERENCE_ROOT))


def _add_common_arguments(parser: argparse.ArgumentParser) -> None:
    backend = parser.add_argument_group("agent backend")
    backend.add_argument(
        "--backend",
        choices=("traex", "codex", "http"),
        default="traex",
        help="non-interactive agent adapter (default: traex)",
    )
    backend.add_argument(
        "--http-config",
        type=Path,
        metavar="PATH",
        help=(
            "HTTP Responses backend YAML/JSON config (required with --backend http; "
            "credentials stay in its api_key_env environment variable)"
        ),
    )
    backend.add_argument(
        "--agent-binary",
        metavar="PATH",
        help="agent executable (default: selected backend name)",
    )
    backend.add_argument("--model", help="optional model override passed to the agent")

    group = parser.add_argument_group("shared run controls")
    group.add_argument(
        "--run-id", default=None, help="stable base identifier (default: command name)"
    )
    group.add_argument(
        "--resume",
        action="store_true",
        help="resume an existing run with the identical plan",
    )
    group.add_argument(
        "--output-root",
        type=Path,
        default=_default_output_root(),
        metavar="PATH",
        help=f"experiment artifact root (default: {_DEFAULT_OUTPUT_ROOT_DISPLAY})",
    )
    group.add_argument(
        "--token-budget",
        type=_positive_int,
        default=100_000,
        metavar="TOKENS",
        help="maximum provider-reported tokens per repetition (default: 100000)",
    )
    group.add_argument(
        "--time-budget-minutes",
        type=_positive_float,
        default=60.0,
        metavar="MINUTES",
        help="wall-clock budget per repetition (default: 60)",
    )
    group.add_argument(
        "--repetitions",
        type=_positive_int,
        default=1,
        metavar="N",
        help="number of independently recorded repetitions (default: 1)",
    )
    group.add_argument(
        "--show-plan",
        action="store_true",
        help="print the resolved plan and backend availability without side effects",
    )


def _add_invariant_arguments(
    parser: argparse.ArgumentParser, *, fixed_segmented: bool = False
) -> None:
    specific = parser.add_argument_group("invariant generation")
    if not fixed_segmented:
        specific.add_argument(
            "--generation-path",
            choices=("combined", "segmented-cot", "rag"),
            default="combined",
            help="candidate-generation path (default: combined)",
        )
    specific.add_argument(
        "--corpus-root",
        type=Path,
        default=None,
        metavar="PATH",
        help="compiler or document corpus to segment (required to execute)",
    )
    specific.add_argument(
        "--reference-root",
        type=Path,
        default=_default_reference_root(),
        metavar="PATH",
        help=(
            "DeFuzz reference corpus root "
            "(default: DEFUZZ_REFERENCE_ROOT or defend-reviewer checkout)"
        ),
    )
    specific.add_argument(
        "--compiler",
        choices=("gcc", "llvm"),
        default="gcc",
        help="compiler corpus to study (default: gcc)",
    )
    selection = parser.add_argument_group(
        "segmented corpus selection",
        description=(
            "Partial ranges, shards, and caps are for pilots or distributed shards only. "
            "Formal full-corpus evidence requires a complete, non-overlapping shard union."
        ),
    )
    selection.add_argument(
        "--segment-start",
        type=int,
        default=0,
        metavar="INDEX",
        help="zero-based inclusive global segment index (default: 0)",
    )
    selection.add_argument(
        "--segment-end",
        type=int,
        default=None,
        metavar="INDEX",
        help="exclusive global segment index; selecting a range is pilot-only",
    )
    selection.add_argument(
        "--shard-index",
        type=int,
        default=0,
        metavar="INDEX",
        help="zero-based deterministic shard index (default: 0)",
    )
    selection.add_argument(
        "--shard-count",
        type=int,
        default=1,
        metavar="N",
        help="number of deterministic shards; formal evidence requires their complete union",
    )
    selection.add_argument(
        "--max-segments",
        type=int,
        default=None,
        metavar="N",
        help="cap selected segments for a pilot; a capped run is not full-corpus evidence",
    )
    selection.add_argument(
        "--minimum-segments",
        type=int,
        default=1,
        metavar="N",
        help="minimum expected selected segments used by preflight validation (default: 1)",
    )
    selection.add_argument(
        "--max-concurrency",
        type=int,
        default=1,
        metavar="N",
        help="maximum concurrent segmented-review workers (default: 1)",
    )


def _add_invariant_generation_parser(subparsers: Any) -> None:
    parser = subparsers.add_parser(
        "invariant-generation",
        help="Part I: generate and ground security invariants",
        description=(
            "Part I — Invariant generation\n\n"
            "Run Segmented CoT, historical-bug RAG, or both, then emit grounded and "
            "deduplicated invariant candidates."
        ),
        epilog=(
            "example: defuzz-experiment invariant-generation --generation-path combined "
            "--reference-root ../defend-reviewer --show-plan"
        ),
        formatter_class=_HelpFormatter,
    )
    _add_common_arguments(parser)
    _add_invariant_arguments(parser)


def _add_pipeline_input_arguments(
    parser: argparse.ArgumentParser, *, inputs_help: str, repeatable: bool = False
) -> None:
    group = parser.add_argument_group("pipeline input")
    exclusive = group.add_mutually_exclusive_group()
    exclusive.add_argument(
        "--inputs",
        "--invariants" if not repeatable else "--checker-inputs",
        dest="inputs",
        type=Path,
        action="append" if repeatable else "store",
        metavar="PATH",
        help=inputs_help,
    )
    exclusive.add_argument(
        "--from-run",
        type=Path,
        metavar="RUN_DIR",
        help="consume the matching repetition from a previous experiment run",
    )


def _add_checker_arguments(parser: argparse.ArgumentParser) -> None:
    _add_pipeline_input_arguments(
        parser, inputs_help="accepted-invariants JSONL file or containing directory"
    )
    specific = parser.add_argument_group("checker authoring")
    specific.add_argument(
        "--reference-root",
        type=Path,
        default=_default_reference_root(),
        metavar="PATH",
        help=(
            "reviewer corpus whose findings subtree is denied to the authoring agent "
            "(default: DEFUZZ_REFERENCE_ROOT or defend-reviewer checkout)"
        ),
    )
    specific.add_argument(
        "--source-root",
        type=Path,
        default=Path("."),
        metavar="PATH",
        help="source tree copied into isolated authoring workspaces (default: current directory)",
    )
    specific.add_argument(
        "--checker-root",
        type=Path,
        default=Path("core/internal/oracle"),
        metavar="PATH",
        help="checker path relative to source root (default: core/internal/oracle)",
    )
    specific.add_argument(
        "--checker-kind",
        choices=("auto", "static", "dynamic"),
        default="auto",
        help="checker implementation strategy recorded in the plan (default: auto)",
    )


def _add_checker_authoring_parser(subparsers: Any) -> None:
    parser = subparsers.add_parser(
        "checker-authoring",
        help="Part II: turn accepted invariants into executable checkers",
        description=(
            "Part II — Checker authoring\n\n"
            "Convert accepted invariants into isolated, validated checker patches."
        ),
        epilog=(
            "example: defuzz-experiment checker-authoring --from-run runs/invariants-r1 "
            "--source-root .. --show-plan"
        ),
        formatter_class=_HelpFormatter,
    )
    _add_common_arguments(parser)
    _add_checker_arguments(parser)


def _add_audit_arguments(
    parser: argparse.ArgumentParser,
    *,
    allow_pipeline_inputs: bool = True,
    allow_online_oracle: bool = True,
    allow_checker_bundle: bool = True,
) -> None:
    if allow_pipeline_inputs:
        _add_pipeline_input_arguments(
            parser,
            inputs_help="checker or oracle document (repeat option for multiple inputs)",
            repeatable=True,
        )
    if allow_checker_bundle:
        bundle = parser.add_argument_group("validated checker bundle")
        bundle.add_argument(
            "--checker-bundle-manifest",
            type=Path,
            metavar="JSON",
            help=(
                "ready Part II checker-bundle manifest; supplies Full online feedback "
                "and the shared offline verification dispatcher"
            ),
        )
        bundle.add_argument(
            "--toolchains-config",
            type=Path,
            metavar="YAML",
            help="toolchain configuration used by the checker-bundle dispatcher",
        )
    scope = parser.add_argument_group("audit scope")
    scope.add_argument(
        "--reference-root",
        type=Path,
        default=_default_reference_root(),
        metavar="PATH",
        help=(
            "reviewer reference root used to build worker prompts "
            "(default: DEFUZZ_REFERENCE_ROOT or defend-reviewer checkout)"
        ),
    )
    scope.add_argument(
        "--target-tree",
        type=Path,
        default=None,
        metavar="PATH",
        help="compiler source tree exposed read-only to audit workers (required to execute)",
    )
    scope.add_argument(
        "--compiler",
        choices=("gcc", "llvm"),
        default="gcc",
        help="compiler under audit (default: gcc)",
    )
    scope.add_argument(
        "--mechanism",
        action="append",
        default=[],
        metavar="NAME",
        help="defense mechanism to audit (repeatable; default: all configured)",
    )
    scope.add_argument(
        "--isa",
        action="append",
        default=[],
        metavar="ISA",
        help="target ISA (repeatable; default: all configured)",
    )
    scope.add_argument(
        "--max-concurrency",
        type=_positive_int,
        default=1,
        metavar="N",
        help="maximum concurrent audit workers (default: 1)",
    )
    if allow_online_oracle:
        oracle = parser.add_argument_group("online oracle")
        oracle.add_argument(
            "--online-oracle-command",
            action="append",
            type=_argv,
            default=[],
            metavar="ARGV",
            help=(
                "candidate-bound checker command used during Full audit feedback "
                "(repeatable; each template requires {candidate_fingerprint})"
            ),
        )
        oracle.add_argument(
            "--oracle-rounds",
            type=_positive_int,
            default=1,
            metavar="N",
            help="maximum candidate-checker-feedback review rounds (default: 1)",
        )
    evaluation = parser.add_argument_group("offline evaluation")
    evaluation.add_argument(
        "--demo-parity",
        action="store_true",
        help=(
            "compare verified candidates with the selected evaluator-only demo "
            "profile after workers exit"
        ),
    )
    evaluation.add_argument(
        "--parity-profile",
        choices=("demo-workset", "poc-verified"),
        default="demo-workset",
        help=(
            "demo corpus profile: demo-workset is the engineering parity/superset "
            "workset (schema-valid, non-retracted, including drafts); poc-verified "
            "is the stronger-evidence subset and is not a formal paper result "
            "(default: demo-workset)"
        ),
    )
    evaluation.add_argument(
        "--parity-threshold",
        type=float,
        metavar="RATIO",
        help=(
            "record whether the selected demo-parity metric reaches RATIO (0 to 1; non-blocking)"
        ),
    )
    evaluation.add_argument(
        "--parity-threshold-metric",
        choices=("recall", "f1"),
        default="recall",
        help=(
            "metric used by --parity-threshold: recall measures selected-profile "
            "superset coverage; f1 is retained for compatibility (default: recall)"
        ),
    )
    evaluation.add_argument(
        "--verification-command",
        action="append",
        type=_argv,
        default=[],
        metavar="ARGV",
        help=(
            "trusted shell-free checker/PoC command for offline verification "
            "(repeatable; parsed with shell quoting, never sourced from Agent output)"
        ),
    )


def _add_agent_audit_parser(subparsers: Any) -> None:
    parser = subparsers.add_parser(
        "agent-audit",
        help="Part III: run invariant- and checker-guided agent audits",
        description=(
            "Part III — Agent audit\n\n"
            "Run structured compiler-defense workers and preserve deterministic admission evidence."
        ),
        epilog=(
            "example: defuzz-experiment agent-audit --target-tree ../gcc "
            "--mechanism canary --isa x86-64 --show-plan"
        ),
        formatter_class=_HelpFormatter,
    )
    _add_common_arguments(parser)
    _add_audit_arguments(parser)


_ABLATION_DESCRIPTIONS = {
    "without-rag": ("Run Part I with Segmented CoT only; historical-bug RAG is disabled."),
    "without-oracle": (
        "Run Part III without online dedicated-checker feedback; admission remains isolated."
    ),
    "bare-agent": (
        "Run Part III without DeFuzz invariants, checkers, or structured workflow guidance."
    ),
}


def _add_ablation_parser(subparsers: Any) -> None:
    parser = subparsers.add_parser(
        "ablation",
        help="run one of the three fixed, budget-matched ablation variants",
        description=(
            "Ablation experiments\n\n"
            "Choose one fixed comparison under the same model, token, wall-clock, and "
            "repetition controls as the full experiments."
        ),
        epilog="variants: without-rag | without-oracle | bare-agent",
        formatter_class=_HelpFormatter,
    )
    variants = parser.add_subparsers(
        dest="variant",
        title="variants",
        description="the complete supported ablation set",
        required=True,
    )
    for name, description in _ABLATION_DESCRIPTIONS.items():
        example_arguments = {
            "without-rag": (
                "--baseline-run FULL_RUN --corpus-root CORPUS "
                "--reference-root REFERENCE_ROOT --show-plan"
            ),
            "without-oracle": (
                "--baseline-run FULL_RUN --target-tree TARGET_TREE "
                "--reference-root REFERENCE_ROOT "
                "--checker-bundle-manifest CHECKER_BUNDLE "
                "--toolchains-config TOOLCHAINS --show-plan"
            ),
            "bare-agent": (
                "--baseline-run FULL_RUN --target-tree TARGET_TREE "
                "--reference-root REFERENCE_ROOT --show-plan"
            ),
        }[name]
        variant = variants.add_parser(
            name,
            help=description,
            description=f"Ablation — {name}\n\n{description}",
            epilog=(f"example: defuzz-experiment ablation {name} {example_arguments}"),
            formatter_class=_HelpFormatter,
        )
        _add_common_arguments(variant)
        variant.add_argument(
            "--baseline-run",
            type=Path,
            required=True,
            metavar="RUN_DIR",
            help=(
                "completed full-arm run whose model, budgets, source, and "
                "repetition count this ablation must match"
            ),
        )
        if name == "without-rag":
            _add_invariant_arguments(variant, fixed_segmented=True)
        else:
            _add_audit_arguments(
                variant,
                allow_pipeline_inputs=name != "bare-agent",
                allow_online_oracle=False,
                allow_checker_bundle=name != "bare-agent",
            )


def _configure_pipeline_parser(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--config",
        type=Path,
        required=True,
        metavar="YAML",
        help="versioned pipeline configuration (paths resolve relative to this file)",
    )
    controls = parser.add_mutually_exclusive_group()
    controls.add_argument(
        "--show-plan",
        action="store_true",
        help="validate inputs and print the frozen content-hashed plan without writes",
    )
    controls.add_argument(
        "--resume",
        action="store_true",
        help="resume an identical pipeline and skip hash-valid completed lanes",
    )


def _add_pipeline_parser(subparsers: Any) -> None:
    parser = subparsers.add_parser(
        "pipeline",
        help="run Parts I--III from one typed YAML configuration",
        description=(
            "Run the complete Part I -> Part II -> Part III experiment pipeline from "
            "one typed YAML configuration. Each target/repetition pair is an "
            "independent, content-addressed lane."
        ),
        epilog=(
            "example: defuzz-experiment pipeline "
            "--config configs/experiments/example.yaml --show-plan"
        ),
        formatter_class=_HelpFormatter,
    )
    _configure_pipeline_parser(parser)


def build_parser() -> argparse.ArgumentParser:
    parser = _parser(
        prog="defuzz-experiment",
        description=(
            "DeFuzz unified experiment launcher\n\n"
            "Run the three evidence-linked paper stages and their fixed ablations with one "
            "reproducible, budget-aware command surface."
        ),
        epilog=(
            "examples:\n"
            "  defuzz-experiment pipeline --config "
            "configs/experiments/example.yaml --show-plan\n"
            "  defuzz-experiment invariant-generation --show-plan\n"
            "  defuzz-experiment checker-authoring --from-run RUN_DIR --show-plan\n"
            "  defuzz-experiment agent-audit --target-tree GCC_TREE --show-plan\n"
            "  defuzz-experiment ablation without-oracle --baseline-run FULL_RUN "
            "--target-tree TARGET_TREE --reference-root REFERENCE_ROOT "
            "--checker-bundle-manifest CHECKER_BUNDLE "
            "--toolchains-config TOOLCHAINS --repetitions 5 --show-plan"
        ),
    )
    subparsers = parser.add_subparsers(
        dest="experiment",
        title="experiments",
        description="complete pipeline, individual parts, and the fixed ablation suite",
        required=True,
    )
    _add_pipeline_parser(subparsers)
    _add_invariant_generation_parser(subparsers)
    _add_checker_authoring_parser(subparsers)
    _add_agent_audit_parser(subparsers)
    _add_ablation_parser(subparsers)
    return parser


def _build_pipeline_parser() -> argparse.ArgumentParser:
    parser = _parser(
        prog="defuzz-experiment pipeline",
        description=(
            "Run the complete Part I -> Part II -> Part III experiment pipeline from "
            "one typed YAML configuration. Each target/repetition pair is an "
            "independent, content-addressed lane."
        ),
        epilog=(
            "example: defuzz-experiment pipeline "
            "--config configs/experiments/example.yaml --show-plan"
        ),
    )
    _configure_pipeline_parser(parser)
    return parser


def _pipeline_main(argv: Sequence[str]) -> int:
    from defuzz_loop.experiment_engine.pipeline import (
        build_pipeline_plan,
        load_pipeline_config,
        run_pipeline_sync,
    )

    parser = _build_pipeline_parser()
    args = parser.parse_args(argv)
    config_path = args.config.expanduser().resolve(strict=False)
    try:
        config = load_pipeline_config(config_path)
        if args.show_plan:
            plan = build_pipeline_plan(config, config_path=config_path)
            print(json.dumps(plan, indent=2, sort_keys=True))
            return 0
        result = run_pipeline_sync(config, resume=bool(args.resume), config_path=config_path)
        label = "completed" if result.result_valid else "failed"
        print(f"{label}: {result.manifest_path}")
        return 0 if result.result_valid else EXIT_RUNTIME_FAILURE
    except (TypeError, ValueError) as exc:
        print(f"defuzz-experiment: configuration error: {exc}", file=sys.stderr)
        return EXIT_CONFIGURATION_ERROR
    except (OSError, RuntimeError) as exc:
        print(f"defuzz-experiment: runtime failure: {exc}", file=sys.stderr)
        return EXIT_RUNTIME_FAILURE


def _resolved_path(path: Path) -> str:
    return str(path.expanduser().resolve(strict=False))


def _resolve_agent_binary(binary: str) -> tuple[str | None, bool]:
    """Resolve a PATH name or validate an explicitly addressed executable."""

    has_path_separator = os.sep in binary or (os.altsep is not None and os.altsep in binary)
    candidate = Path(binary).expanduser()
    if candidate.is_absolute() or has_path_separator:
        try:
            resolved = candidate.resolve(strict=True)
        except (OSError, RuntimeError):
            return None, False
        return str(resolved), resolved.is_file() and os.access(resolved, os.X_OK)

    located = shutil.which(binary)
    if located is None:
        return None, False
    return str(Path(located).expanduser().resolve(strict=False)), True


def _binary_available(binary: str) -> bool:
    return _resolve_agent_binary(binary)[1]


def _is_within(path: Path, parent: Path) -> bool:
    try:
        path.resolve(strict=False).relative_to(parent.resolve(strict=False))
    except ValueError:
        return False
    return True


def _tree_metadata_manifest(root: Path, *, exclude: Path | None = None) -> dict[str, Any]:
    """Metadata manifest for untracked trees.

    It fingerprints every relative path, size, and nanosecond mtime. Formal
    campaigns should still prefer a pinned Git tree.
    """

    digest = hashlib.sha256()
    files = 0
    total_bytes = 0
    for directory, names, filenames in os.walk(root, followlinks=False):
        names.sort()
        filenames.sort()
        parent = Path(directory)
        if exclude is not None and _is_within(parent, exclude):
            names.clear()
            continue
        for filename in filenames:
            path = parent / filename
            if exclude is not None and _is_within(path, exclude):
                continue
            relative = path.relative_to(root).as_posix()
            stat_result = path.lstat()
            record = f"{relative}\0{stat_result.st_size}\0{stat_result.st_mtime_ns}\n"
            digest.update(record.encode("utf-8", errors="surrogateescape"))
            files += 1
            total_bytes += stat_result.st_size
    return {
        "kind": "tree-metadata",
        "manifest_sha256": digest.hexdigest(),
        "files": files,
        "total_bytes": total_bytes,
    }


def _git_snapshot(root: Path, *, exclude: Path | None = None) -> dict[str, Any] | None:
    def git(cwd: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
        return subprocess.run(
            ["git", "-C", os.fspath(cwd), *args],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )

    repository_result = git(root, "rev-parse", "--show-toplevel")
    if repository_result.returncode != 0:
        return None
    repository = Path(os.fsdecode(repository_result.stdout).strip()).resolve()
    try:
        scope = root.resolve().relative_to(repository).as_posix() or "."
    except ValueError:
        return None
    head_result = git(repository, "rev-parse", "HEAD")
    pathspecs = [":(top)" if scope == "." else f":(top){scope}"]
    if exclude is not None and _is_within(exclude, repository):
        excluded = exclude.resolve(strict=False).relative_to(repository).as_posix()
        pathspecs.append(f":(top,exclude){excluded}")
    status_result = git(
        repository,
        "status",
        "--porcelain=v1",
        "-z",
        "--untracked-files=normal",
        "--",
        *pathspecs,
    )
    diff_result = git(
        repository,
        "diff",
        "--binary",
        "--no-ext-diff",
        "HEAD",
        "--",
        *pathspecs,
    )
    tracked_result = git(repository, "ls-files", "--", pathspecs[0])
    if any(
        result.returncode != 0
        for result in (head_result, status_result, diff_result, tracked_result)
    ):
        return None
    # A directory represented only by one untracked parent entry is not frozen
    # by HEAD/diff. Hash its complete contents instead.
    if not tracked_result.stdout.strip():
        return None

    state = hashlib.sha256()
    head = os.fsdecode(head_result.stdout).strip()
    state.update(head.encode("ascii"))
    state.update(b"\0")
    state.update(status_result.stdout)
    state.update(b"\0")
    state.update(diff_result.stdout)
    dirty = bool(status_result.stdout)
    return {
        "kind": "git",
        "repository": str(repository),
        "head": head,
        "dirty": dirty,
        "state_sha256": state.hexdigest(),
    }


def _snapshot_path(value: Any, *, exclude: Path | None = None) -> dict[str, Any]:
    path = Path(str(value)).expanduser().resolve(strict=False)
    snapshot: dict[str, Any] = {"path": str(path), "exists": path.exists()}
    if not path.exists():
        return snapshot
    if path.is_file():
        snapshot.update(
            {
                "kind": "file",
                "sha256": _sha256_file(path),
                "size_bytes": path.stat().st_size,
            }
        )
        return snapshot
    if os.environ.get("DEFUZZ_FAST_PLAN") == "1":
        stat_result = path.stat()
        snapshot.update(
            {
                "kind": "directory-stat",
                "size_bytes": stat_result.st_size,
                "mtime_ns": stat_result.st_mtime_ns,
                "warning": "fast plan does not freeze recursive input contents",
            }
        )
        return snapshot
    git_state = _git_snapshot(path, exclude=exclude)
    if git_state is not None:
        snapshot.update(git_state)
    else:
        snapshot.update(_tree_metadata_manifest(path, exclude=exclude))
    return snapshot


def _input_snapshot(plan: ExperimentPlan) -> dict[str, Any]:
    _, stage, _ = _stage_selection(plan)
    output_root = plan.output_root
    snapshot: dict[str, Any] = {}

    direct = plan.parameters.get("inputs")
    if direct:
        values = direct if isinstance(direct, list) else [direct]
        paths: list[dict[str, Any]] = []
        for value in values:
            path = Path(str(value)).expanduser().resolve(strict=False)
            if stage == "checker-authoring" and path.is_dir():
                path /= "accepted-invariants.jsonl"
            paths.append(_snapshot_path(path))
        snapshot["inputs"] = paths

    from_run = plan.parameters.get("from_run")
    if from_run:
        _, filename = _expected_upstream(stage)
        snapshot["from_run"] = {
            "path": str(Path(str(from_run)).expanduser().resolve(strict=False)),
            "artifacts": [
                _snapshot_path(_from_run_artifact(str(from_run), repetition, filename))
                for repetition in range(1, plan.repetitions + 1)
            ],
        }
    checker_manifest = plan.parameters.get("checker_bundle_manifest")
    if checker_manifest:
        snapshot["checker_bundle_manifest"] = _snapshot_path(checker_manifest)
    toolchains_config = plan.parameters.get("toolchains_config")
    if toolchains_config:
        snapshot["toolchains_config"] = _snapshot_path(toolchains_config)

    roots: dict[str, dict[str, Any]] = {}
    if stage == "invariant-generation" and plan.parameters.get("corpus_root"):
        roots["corpus_root"] = _snapshot_path(plan.parameters["corpus_root"], exclude=output_root)
    elif plan.source_root is not None:
        roots["source_root"] = _snapshot_path(plan.source_root, exclude=output_root)
    reference_root = plan.parameters.get("reference_root")
    if reference_root:
        roots["reference_root"] = _snapshot_path(reference_root, exclude=output_root)
    if roots:
        snapshot["roots"] = roots

    baseline = plan.parameters.get("baseline_run")
    if baseline:
        root = Path(str(baseline)).expanduser().resolve(strict=False)
        snapshot["baseline_run"] = {
            "path": str(root),
            "plan": _snapshot_path(root / "plan.json"),
            "manifest": _snapshot_path(root / "manifest.json"),
        }
        if stage == "agent-audit" and plan.variant == "bare-agent":
            frozen_verifiers = []
            for repetition in range(1, plan.repetitions + 1):
                resolved = _checker_bundle_inputs(plan, repetition)
                if resolved is None:
                    continue
                manifest, toolchains = resolved
                frozen_verifiers.append(
                    {
                        "repetition": repetition,
                        "checker_bundle_manifest": _snapshot_path(manifest),
                        "toolchains_config": _snapshot_path(toolchains),
                    }
                )
            snapshot["baseline_verifiers"] = frozen_verifiers
    return snapshot


def _with_input_snapshot(plan: ExperimentPlan) -> ExperimentPlan:
    parameters = dict(plan.parameters)
    parameters["input_snapshot"] = _input_snapshot(plan)
    return plan.model_copy(update={"parameters": parameters})


def _resolved_plan(args: argparse.Namespace) -> dict[str, Any]:
    values = vars(args)
    experiment = str(values["experiment"])
    selected_variant = values.get("variant")
    variant = str(selected_variant or "full")
    default_run_id = f"{experiment}-{selected_variant}" if selected_variant else experiment
    run_id = str(values.get("run_id") or default_run_id)
    output_root = Path(values["output_root"]).expanduser().resolve(strict=False)
    repetitions = int(values["repetitions"])
    backend_kind = str(values["backend"])
    http_config: HTTPAgentConfig | None = None
    if backend_kind == "http":
        raw_config = values.get("http_config")
        if raw_config is None:
            raise ValueError("--http-config is required when --backend http")
        if values.get("agent_binary") is not None:
            raise ValueError("--agent-binary is unsupported when --backend http")
        config_path = Path(raw_config).expanduser().resolve(strict=False)
        try:
            http_config, http_config_sha256, http_config_size_bytes = (
                load_http_agent_config_snapshot(config_path)
            )
        except (OSError, ValueError) as exc:
            raise ValueError(f"HTTP agent config is invalid: {config_path}: {exc}") from exc
        requested_model = values.get("model")
        if requested_model is not None and requested_model != http_config.model:
            raise ValueError(
                "--model must match the HTTP agent config model: "
                f"cli={requested_model!r}, http_config={http_config.model!r}"
            )
        binary = None
        binary_resolved, binary_available = None, True
    else:
        if values.get("http_config") is not None:
            raise ValueError("--http-config is supported only when --backend http")
        binary = str(values.get("agent_binary") or backend_kind)
        binary_resolved, binary_available = _resolve_agent_binary(binary)

    common_keys = {
        "experiment",
        "variant",
        "run_id",
        "output_root",
        "token_budget",
        "time_budget_minutes",
        "repetitions",
        "show_plan",
        "resume",
        "source_root",
        "target_tree",
    }
    parameters = {
        key: (
            value.as_posix()
            if key == "checker_root" and isinstance(value, Path)
            else [_resolved_path(item) for item in value]
            if isinstance(value, list) and value and all(isinstance(item, Path) for item in value)
            else _resolved_path(value)
            if isinstance(value, Path)
            else value
        )
        for key, value in values.items()
        if key not in common_keys and value is not None
    }
    if http_config is not None:
        parameters["model"] = http_config.model
        parameters["http_config"] = _resolved_path(Path(values["http_config"]))
        parameters["http_config_sha256"] = http_config_sha256
    else:
        assert binary is not None
        parameters["agent_binary"] = binary
    parameters["backend"] = backend_kind
    # Standalone stage commands are production experiment entry points. Keep
    # their host-read boundary fail closed; fixture semantics belong to the
    # explicitly configured pipeline command.
    parameters["require_host_read_isolation"] = True
    if variant == "bare-agent" and (parameters.get("inputs") or parameters.get("from_run")):
        raise ValueError("bare-agent does not accept --inputs or --from-run")
    if experiment == "ablation" and variant == "without-rag":
        parameters["generation_path"] = "segmented-cot"

    source_root = values.get("source_root") or values.get("target_tree")
    run_root = output_root / run_id
    plan: dict[str, Any] = {
        "schema_version": 1,
        "status": "ready",
        # Retain the original scalar for consumers of the pre-resolution plan.
        "backend_available": binary_available,
        "backend": (
            {
                "kind": "http-responses",
                "available": True,
                "config_path": parameters["http_config"],
                "config_snapshot": {
                    "path": parameters["http_config"],
                    "sha256": http_config_sha256,
                    "size_bytes": http_config_size_bytes,
                },
                "endpoint": http_config.responses_url,
                "model": http_config.model,
                "reasoning_effort": http_config.reasoning_effort,
                "api_key_env": http_config.api_key_env,
                "api_key_available": bool(os.environ.get(http_config.api_key_env)),
                "settings": http_config.model_dump(mode="json"),
            }
            if http_config is not None
            else {
                "binary": binary,
                "resolved_path": binary_resolved,
                "available": binary_available,
            }
        ),
        "experiment": experiment,
        "variant": variant,
        "run": {
            "run_id": run_id,
            "output_root": str(output_root),
            "token_budget": values["token_budget"],
            "time_budget_minutes": values["time_budget_minutes"],
            "repetitions": repetitions,
        },
        "parameters": parameters,
        "launches": [
            {
                "repetition": repetition,
                "output_dir": str(run_root / f"rep-{repetition:03d}" / "artifacts"),
            }
            for repetition in range(1, repetitions + 1)
        ],
    }
    if source_root is not None:
        plan["source_root"] = _resolved_path(Path(source_root))
    return plan


def _stage_selection(plan: ExperimentPlan) -> tuple[str, str, _StageRunner]:
    if plan.experiment == "invariant-generation" or plan.variant == "without-rag":
        return "I", "invariant-generation", invariant_generation.run
    if plan.experiment == "checker-authoring":
        return "II", "checker-authoring", checker_authoring.run
    return "III", "agent-audit", agent_audit.run


def _from_run_artifact(run_dir: str | os.PathLike[str], repetition: int, filename: str) -> Path:
    root = Path(run_dir).expanduser().resolve(strict=False)
    candidates = (
        root / f"rep-{repetition:03d}" / "artifacts" / filename,
        root / f"rep-{repetition:03d}" / filename,
        root / "artifacts" / filename,
        root / filename,
    )
    return next((path for path in candidates if path.is_file()), candidates[0])


def _read_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ValueError(f"{label} does not exist: {path}") from exc
    except json.JSONDecodeError as exc:
        raise ValueError(f"{label} is not valid JSON: {path}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be a JSON object: {path}")
    return value


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _expected_upstream(stage: str) -> tuple[str, str]:
    if stage == "checker-authoring":
        return "invariant-generation", "accepted-invariants.jsonl"
    if stage == "agent-audit":
        return "checker-authoring", _CHECKER_BUNDLE_MANIFEST
    raise ValueError(f"stage {stage!r} does not consume --from-run")


def _validate_from_run(plan: ExperimentPlan, stage: str) -> None:
    value = plan.parameters.get("from_run")
    if not value:
        return
    root = Path(str(value)).expanduser().resolve(strict=False)
    root_manifest = _read_json_object(root / "manifest.json", "upstream manifest")
    if root_manifest.get("status") != "completed":
        raise ValueError(f"upstream run is not completed: {root_manifest.get('status')!r}")

    expected_stage, filename = _expected_upstream(stage)
    for repetition in range(1, plan.repetitions + 1):
        rep_dir = root / f"rep-{repetition:03d}"
        manifest = _read_json_object(
            rep_dir / "manifest.json", f"upstream repetition {repetition} manifest"
        )
        if manifest.get("status") != "completed":
            raise ValueError(
                f"upstream repetition {repetition} is not completed: {manifest.get('status')!r}"
            )
        if manifest.get("stage") != expected_stage:
            raise ValueError(
                f"upstream repetition {repetition} stage mismatch: expected "
                f"{expected_stage!r}, got {manifest.get('stage')!r}"
            )
        result_name = manifest.get("stage_result")
        if not isinstance(result_name, str) or not result_name:
            raise ValueError(f"upstream repetition {repetition} has no stage_result")
        result_path = (rep_dir / result_name).resolve(strict=False)
        try:
            result_path.relative_to(rep_dir.resolve())
        except ValueError as exc:
            raise ValueError(
                f"upstream repetition {repetition} stage_result escapes its directory"
            ) from exc
        try:
            result = StageResult.model_validate(
                _read_json_object(result_path, f"upstream repetition {repetition} stage result")
            )
        except (TypeError, ValueError) as exc:
            raise ValueError(
                f"upstream repetition {repetition} has an invalid stage result: {exc}"
            ) from exc
        if result.stage != expected_stage:
            raise ValueError(
                f"upstream repetition {repetition} result stage mismatch: expected "
                f"{expected_stage!r}, got {result.stage!r}"
            )
        if not result.success:
            raise ValueError(f"upstream repetition {repetition} stage result failed")

        artifact = _from_run_artifact(root, repetition, filename)
        if not artifact.is_file():
            raise ValueError(f"upstream artifact does not exist: {artifact}")
        actual_hash = _sha256_file(artifact)
        artifact_ref = next(
            (ref for ref in result.artifacts if Path(ref.path).name == filename), None
        )
        if artifact_ref is None:
            raise ValueError(
                f"upstream repetition {repetition} stage result does not declare "
                f"required artifact {filename!r}"
            )
        if artifact_ref.sha256 != actual_hash:
            raise ValueError(
                f"upstream artifact hash mismatch for {artifact}: "
                f"expected {artifact_ref.sha256}, got {actual_hash}"
            )
        if artifact_ref.size_bytes != artifact.stat().st_size:
            raise ValueError(
                f"upstream artifact size mismatch for {artifact}: "
                f"expected {artifact_ref.size_bytes}, got {artifact.stat().st_size}"
            )


def _baseline_plan(plan: ExperimentPlan) -> ExperimentPlan:
    root = Path(str(plan.parameters["baseline_run"])).expanduser().resolve(strict=False)
    try:
        return ExperimentPlan.from_mapping(_read_json_object(root / "plan.json", "baseline plan"))
    except (TypeError, ValueError) as exc:
        raise ValueError(f"baseline plan is invalid: {exc}") from exc


def _root_snapshot(plan: ExperimentPlan, key: str) -> Mapping[str, Any] | None:
    raw_snapshot = plan.parameters.get("input_snapshot")
    if raw_snapshot is None:
        return None
    if not isinstance(raw_snapshot, Mapping):
        raise ValueError("baseline input_snapshot must be an object")
    raw_roots = raw_snapshot.get("roots")
    if raw_roots is None:
        return None
    if not isinstance(raw_roots, Mapping):
        raise ValueError("baseline input_snapshot.roots must be an object")
    value = raw_roots.get(key)
    if value is None:
        return None
    if not isinstance(value, Mapping):
        raise ValueError(f"baseline input_snapshot root {key!r} must be an object")
    return value


def _snapshot_content_identity(value: Any) -> Any:
    """Discard location-only fields while retaining the frozen content identity."""

    if isinstance(value, Mapping):
        return {
            key: _snapshot_content_identity(item)
            for key, item in sorted(value.items())
            if key not in {"path", "repository", "warning"}
        }
    if isinstance(value, list):
        return [_snapshot_content_identity(item) for item in value]
    return value


def _add_root_fairness_comparison(
    comparisons: dict[str, tuple[Any, Any]],
    *,
    label: str,
    key: str,
    current_plan: ExperimentPlan,
    baseline_plan: ExperimentPlan,
    current_path: Any,
    baseline_path: Any,
) -> None:
    frozen_snapshot = _root_snapshot(baseline_plan, key)
    if frozen_snapshot is not None:
        current_snapshot = _root_snapshot(current_plan, key)
        comparisons[f"{label} content"] = (
            _snapshot_content_identity(current_snapshot),
            _snapshot_content_identity(frozen_snapshot),
        )
        return

    # Compatibility for runs produced before root snapshots were persisted.
    comparisons[label] = (
        str(Path(current_path).expanduser().resolve(strict=False))
        if current_path is not None
        else None,
        str(Path(baseline_path).expanduser().resolve(strict=False))
        if baseline_path is not None
        else None,
    )


def _checker_bundle_inputs(
    plan: ExperimentPlan, repetition: int, *, derive_baseline: bool = True
) -> tuple[Path, Path] | None:
    parameters = plan.parameters
    explicit = parameters.get("checker_bundle_manifest")
    from_run = parameters.get("from_run")
    if explicit and from_run:
        raise ValueError("--checker-bundle-manifest and --from-run are mutually exclusive")
    if explicit:
        manifest = Path(str(explicit)).expanduser().resolve(strict=False)
    elif from_run:
        manifest = _from_run_artifact(str(from_run), repetition, _CHECKER_BUNDLE_MANIFEST)
    elif plan.variant == "bare-agent" and derive_baseline:
        return _checker_bundle_inputs(_baseline_plan(plan), repetition, derive_baseline=False)
    else:
        return None

    raw_toolchains = parameters.get("toolchains_config")
    if not raw_toolchains:
        raise ValueError("checker-bundle execution requires --toolchains-config")
    toolchains = Path(str(raw_toolchains)).expanduser().resolve(strict=False)
    return manifest, toolchains


def _validated_checker_bundle_inputs(
    plan: ExperimentPlan, repetition: int
) -> tuple[Path, Path, str, str] | None:
    resolved = _checker_bundle_inputs(plan, repetition)
    if resolved is None:
        return None
    manifest, toolchains = resolved
    from defuzz_loop.checker_bundle import load_checker_bundle

    bundle = load_checker_bundle(manifest, require_ready=True)
    if not toolchains.is_file():
        raise ValueError(f"toolchains config is not an existing file: {toolchains}")
    manifest_hash = _sha256_file(bundle.manifest_path)
    configured_hash = plan.parameters.get("checker_bundle_sha256")
    if configured_hash and configured_hash != manifest_hash:
        raise ValueError(
            "checker bundle manifest SHA-256 mismatch: "
            f"expected {configured_hash}, got {manifest_hash}"
        )
    return (
        bundle.manifest_path,
        toolchains.resolve(strict=True),
        manifest_hash,
        _sha256_file(toolchains),
    )


def _validate_baseline_run(plan: ExperimentPlan, stage: str) -> None:
    value = plan.parameters.get("baseline_run")
    if not value:
        return
    root = Path(str(value)).expanduser().resolve(strict=False)
    manifest = _read_json_object(root / "manifest.json", "baseline manifest")
    if manifest.get("status") != "completed":
        raise ValueError(f"baseline run is not completed: {manifest.get('status')!r}")
    baseline = _baseline_plan(plan)
    if baseline.variant != "full":
        raise ValueError("--baseline-run must refer to a full-arm run")

    expected_experiment = (
        "invariant-generation" if stage == "invariant-generation" else "agent-audit"
    )
    if baseline.experiment != expected_experiment:
        raise ValueError(
            f"baseline stage mismatch: expected {expected_experiment!r}, "
            f"got {baseline.experiment!r}"
        )

    current_source = (
        plan.parameters.get("corpus_root") if stage == "invariant-generation" else plan.source_root
    )
    baseline_source = (
        baseline.parameters.get("corpus_root")
        if stage == "invariant-generation"
        else baseline.source_root
    )
    comparisons = {
        "backend": (
            plan.parameters.get("agent_binary"),
            baseline.parameters.get("agent_binary"),
        ),
        "model": (plan.parameters.get("model"), baseline.parameters.get("model")),
        "token budget": (plan.budget.token_budget, baseline.budget.token_budget),
        "time budget": (
            plan.budget.time_budget_minutes,
            baseline.budget.time_budget_minutes,
        ),
        "repetitions": (plan.repetitions, baseline.repetitions),
    }
    source_key = "corpus_root" if stage == "invariant-generation" else "source_root"
    _add_root_fairness_comparison(
        comparisons,
        label="source",
        key=source_key,
        current_plan=plan,
        baseline_plan=baseline,
        current_path=current_source,
        baseline_path=baseline_source,
    )
    _add_root_fairness_comparison(
        comparisons,
        label="reference root",
        key="reference_root",
        current_plan=plan,
        baseline_plan=baseline,
        current_path=plan.parameters.get("reference_root"),
        baseline_path=baseline.parameters.get("reference_root"),
    )
    if stage == "agent-audit":
        for name in ("compiler", "max_concurrency", "toolchain_versions"):
            comparisons[name.replace("_", " ")] = (
                plan.parameters.get(name),
                baseline.parameters.get(name),
            )
        for plural, singular in (("mechanisms", "mechanism"), ("isas", "isa")):
            current_scope = plan.parameters.get(
                plural, plan.parameters.get(singular, [])
            )
            baseline_scope = baseline.parameters.get(
                plural, baseline.parameters.get(singular, [])
            )
            normalize = normalize_mechanism if plural == "mechanisms" else normalize_isa
            comparisons[plural] = (
                sorted(normalize(str(item)) for item in _as_list(current_scope)),
                sorted(normalize(str(item)) for item in _as_list(baseline_scope)),
            )
        if plan.variant in {"without-oracle", "bare-agent"}:
            # Both ablations retain the full arm's frozen post-run verifier.
            # The bare worker never receives these paths or checker material.
            current_bundles = [
                _validated_checker_bundle_inputs(plan, repetition)
                for repetition in range(1, plan.repetitions + 1)
            ]
            baseline_bundles = [
                _validated_checker_bundle_inputs(baseline, repetition)
                for repetition in range(1, baseline.repetitions + 1)
            ]
            if not all(current_bundles) or not all(baseline_bundles):
                raise ValueError(
                    f"{plan.variant} baseline must freeze a checker bundle and toolchains config"
                )
            comparisons["checker bundle manifest hashes"] = (
                [cast(tuple[Path, Path, str, str], item)[2] for item in current_bundles],
                [cast(tuple[Path, Path, str, str], item)[2] for item in baseline_bundles],
            )
            comparisons["toolchains config hashes"] = (
                [cast(tuple[Path, Path, str, str], item)[3] for item in current_bundles],
                [cast(tuple[Path, Path, str, str], item)[3] for item in baseline_bundles],
            )
    mismatches = [
        f"{name}: requested={current!r}, baseline={frozen!r}"
        for name, (current, frozen) in comparisons.items()
        if current != frozen
    ]
    if mismatches:
        raise ValueError("ablation does not match its full-arm baseline: " + "; ".join(mismatches))


def _plan_for_repetition(plan: ExperimentPlan, repetition: int, stage: str) -> ExperimentPlan:
    parameters = dict(plan.parameters)
    from_run = parameters.get("from_run")
    inputs = parameters.get("inputs")
    if stage == "checker-authoring":
        if from_run:
            parameters["accepted_invariants"] = str(
                _from_run_artifact(from_run, repetition, "accepted-invariants.jsonl")
            )
        elif inputs:
            parameters["accepted_invariants"] = inputs
    elif stage == "agent-audit":
        checker_bundle = _validated_checker_bundle_inputs(plan, repetition)
        if checker_bundle is not None:
            manifest, toolchains, manifest_hash, toolchains_hash = checker_bundle
            parameters.update(
                {
                    "checker_bundle_manifest": str(manifest),
                    "checker_bundle_sha256": manifest_hash,
                    "toolchains_config": str(toolchains),
                    "toolchains_config_sha256": toolchains_hash,
                    "require_verified_candidates": True,
                }
            )
            # Bare workers remain blind to the bundle. agent_audit only uses it
            # after structural admission for the common offline verify policy.
            parameters.pop("checker_artifacts", None)
        elif inputs:
            parameters["checker_artifacts"] = inputs
    return plan.model_copy(update={"parameters": parameters})


def _assert_frozen_bundle_inputs(plan: ExperimentPlan) -> None:
    manifest = plan.parameters.get("checker_bundle_manifest")
    expected_manifest = plan.parameters.get("checker_bundle_sha256")
    toolchains = plan.parameters.get("toolchains_config")
    expected_toolchains = plan.parameters.get("toolchains_config_sha256")
    pairs = (
        (manifest, expected_manifest, "checker bundle manifest"),
        (toolchains, expected_toolchains, "toolchains config"),
    )
    for raw_path, expected, label in pairs:
        if raw_path is None:
            continue
        path = Path(str(raw_path)).expanduser().resolve(strict=False)
        if not path.is_file():
            raise ValueError(f"frozen {label} does not exist: {path}")
        actual = _sha256_file(path)
        if not expected or actual != expected:
            raise ValueError(f"frozen {label} SHA-256 mismatch: expected {expected}, got {actual}")


def _require_directory(value: Any, label: str) -> Path:
    path = Path(value).expanduser().resolve(strict=False)
    if not path.is_dir():
        raise ValueError(f"{label} is not an existing directory: {path}")
    return path


def _require_reference_documents(value: Any) -> Path:
    root = _require_directory(value, "reference root")
    missing = [path.as_posix() for path in _REQUIRED_REFERENCE_PATHS if not (root / path).exists()]
    if missing:
        raise ValueError(
            f"reference root {root} is missing required documents: {', '.join(missing)}"
        )
    return root


def _require_checker_input(plan: ExperimentPlan) -> None:
    parameters = plan.parameters
    direct = parameters.get("inputs")
    previous = parameters.get("from_run")
    if not direct and not previous:
        raise ValueError("checker-authoring requires --inputs or --from-run")
    for repetition in range(1, plan.repetitions + 1):
        if previous:
            path = _from_run_artifact(previous, repetition, "accepted-invariants.jsonl")
        else:
            path = Path(cast(str, direct)).expanduser().resolve(strict=False)
            if path.is_dir():
                path /= "accepted-invariants.jsonl"
        if not path.is_file():
            raise ValueError(f"accepted invariant input does not exist: {path}")


def _validate_execution_inputs(plan: ExperimentPlan) -> None:
    _, stage, _ = _stage_selection(plan)
    if stage == "invariant-generation":
        if not plan.parameters.get("corpus_root"):
            raise ValueError("invariant-generation requires --corpus-root")
        _require_directory(plan.parameters.get("corpus_root"), "corpus root")
    elif stage == "checker-authoring":
        _require_directory(plan.source_root, "source root")
        checker_root = Path(str(plan.parameters.get("checker_root", "")))
        if checker_root.is_absolute() or ".." in checker_root.parts:
            raise ValueError("checker root must be relative to source root")
        _require_checker_input(plan)
    else:
        if plan.source_root is None:
            raise ValueError("agent-audit requires --target-tree")
        _require_directory(plan.source_root, "target tree")
        _require_reference_documents(plan.parameters.get("reference_root"))
        for value in plan.parameters.get("inputs", []):
            path = Path(value).expanduser().resolve(strict=False)
            if not path.is_file():
                raise ValueError(f"audit input does not exist: {path}")
        threshold = plan.parameters.get("parity_threshold")
        if threshold is not None and not 0.0 <= float(threshold) <= 1.0:
            raise ValueError("parity threshold must be between 0 and 1")
        if plan.parameters.get("toolchains_config") and not (
            plan.parameters.get("checker_bundle_manifest") or plan.parameters.get("from_run")
        ):
            raise ValueError("--toolchains-config requires --checker-bundle-manifest or --from-run")
        bundle_inputs = [
            _validated_checker_bundle_inputs(plan, repetition)
            for repetition in range(1, plan.repetitions + 1)
        ]
        if plan.variant in {"without-oracle", "bare-agent"} and plan.parameters.get(
            "online_oracle_command"
        ):
            raise ValueError(f"{plan.variant} forbids online oracle commands")
        if (
            plan.variant == "full"
            and not all(bundle_inputs)
            and not plan.parameters.get("online_oracle_command")
        ):
            raise ValueError(
                "full agent-audit requires --checker-bundle-manifest/--from-run "
                "or the legacy --online-oracle-command"
            )
        if plan.variant in {"without-oracle", "bare-agent"} and not all(bundle_inputs):
            raise ValueError(
                f"{plan.variant} requires a frozen checker bundle for offline verification"
            )

    _validate_from_run(plan, stage)
    _validate_baseline_run(plan, stage)

    if plan.parameters.get("backend") == "http":
        config = _http_config_for_plan(plan)
        if not os.environ.get(config.api_key_env):
            raise ValueError(
                "HTTP agent API key environment variable is unavailable: "
                f"{config.api_key_env}"
            )
    else:
        binary = str(plan.parameters.get("agent_binary", "traex"))
        if not _binary_available(binary):
            raise ValueError(f"agent binary is unavailable: {binary}")


def _completed_repetition(store: RunStore, repetition: int, stage: str) -> bool:
    manifest = store.read_manifest(repetition=repetition)
    if manifest.get("status") != "completed":
        return False
    if manifest.get("stage") != stage:
        raise ValueError(
            f"completed repetition {repetition} stage mismatch: "
            f"expected {stage!r}, got {manifest.get('stage')!r}"
        )
    result_name = manifest.get("stage_result")
    if not isinstance(result_name, str) or not result_name:
        raise ValueError(f"completed repetition {repetition} has no stage_result")
    result_path = store.rep_dir(repetition) / result_name
    result = StageResult.model_validate(
        _read_json_object(result_path, f"completed repetition {repetition} stage result")
    )
    if result.stage != stage or not result.success:
        raise ValueError(f"completed repetition {repetition} has an inconsistent stage result")
    return True


def _http_config_for_plan(plan: ExperimentPlan) -> HTTPAgentConfig:
    value = plan.parameters.get("http_config")
    if not value:
        raise ValueError("--http-config is required when --backend http")
    path = Path(str(value)).expanduser().resolve(strict=False)
    expected_hash = plan.parameters.get("http_config_sha256")
    if not isinstance(expected_hash, str) or not expected_hash:
        raise ValueError("HTTP agent plan is missing the frozen config SHA-256")
    if not path.is_file() or _sha256_file(path) != expected_hash:
        raise ValueError(
            "HTTP agent config no longer matches the frozen plan; create a new run"
        )
    try:
        config, actual_hash, _ = load_http_agent_config_snapshot(path)
    except (OSError, ValueError) as exc:
        raise ValueError(f"HTTP agent config is invalid: {path}: {exc}") from exc
    if actual_hash != expected_hash:
        raise ValueError(
            "HTTP agent config changed while it was being frozen; create a new run"
        )
    return config


def _standalone_backend(plan: ExperimentPlan) -> AgentBackend:
    """Build and capability-check the backend before entering asyncio."""

    if plan.parameters.get("backend") == "http":
        backend: AgentBackend = HTTPResponsesAgentBackend(_http_config_for_plan(plan))
    else:
        backend = ExecAgentBackend(
            binary=str(plan.parameters.get("agent_binary", "traex")),
            model=cast(str | None, plan.parameters.get("model")),
            provider=cast(
                Literal["traex", "codex"],
                plan.parameters.get("backend", "traex"),
            ),
        )
    if plan.parameters.get("require_host_read_isolation") and not bool(
        getattr(backend, "supports_host_read_isolation", False)
    ):
        raise ValueError(
            "standalone experiments require host read isolation through an enforced "
            "boundary; "
            "use workspace-scoped tools or an equivalent OS sandbox"
        )
    return backend


async def _execute(
    plan: ExperimentPlan,
    *,
    backend_impl: AgentBackend,
    resume: bool = False,
) -> int:
    if plan.output_root is None:
        raise ValueError("output_root is required")
    run_root = plan.output_root / plan.run_id
    if resume:
        if not run_root.is_dir() or not all(
            (run_root / name).is_file() for name in ("plan.json", "manifest.json")
        ):
            raise ValueError(f"cannot resume missing or incomplete run: {run_root}")
    elif run_root.exists():
        raise ValueError(f"run already exists: {run_root}; pass --resume to continue it")
    store = RunStore(run_root, plan)
    part, stage_name, runner = _stage_selection(plan)
    successes: list[int] = []
    failures: list[int] = []

    for repetition in range(1, plan.repetitions + 1):
        if resume and _completed_repetition(store, repetition, stage_name):
            successes.append(repetition)
            continue
        rep_dir = store.prepare_rep(repetition)
        output_dir = rep_dir / "artifacts"
        sink = TokenUsageSink(
            rep_dir / "token_usage.jsonl",
            context=TokenUsageContext(
                run_id=plan.run_id,
                experiment=plan.experiment,
                variant=plan.variant,
                part=part,
                stage=stage_name,
                provider=str(plan.parameters.get("backend", "traex")),
                model=cast(str | None, plan.parameters.get("model")),
            ),
            token_budget=plan.budget.token_budget,
        )
        backend = _TokenSinkBackend(
            backend_impl,
            sink,
        )
        stage_plan = _plan_for_repetition(plan, repetition, stage_name)
        try:
            if stage_name == "agent-audit":
                _assert_frozen_bundle_inputs(stage_plan)
            with use_token_usage(sink):
                async with asyncio.timeout(plan.budget.timeout_seconds):
                    result = await runner(stage_plan, repetition, output_dir, backend)
            if stage_name == "agent-audit":
                _assert_frozen_bundle_inputs(stage_plan)
        except TimeoutError:
            result = StageResult(
                stage=stage_name,
                status="failed",
                error=f"wall-clock budget exceeded after {plan.budget.timeout_seconds:g}s",
            )
        except Exception as exc:  # A failed repetition must not hide later repetitions.
            result = StageResult(
                stage=stage_name,
                status="failed",
                error=f"{type(exc).__name__}: {exc}",
            )

        result_path = store.write_stage_result(repetition, result)
        summary_json_path = rep_dir / "token_usage_summary.json"
        summary_csv_path = rep_dir / "token_usage_summary.csv"
        summary_rows = sink.finalize(json_path=summary_json_path, csv_path=summary_csv_path)
        usage_missing = sum(int(row["usage_missing_count"]) for row in summary_rows)
        consumed_tokens = sink.consumed_total_tokens
        budget_overshot = consumed_tokens is not None and consumed_tokens > plan.budget.token_budget
        token_comparable = bool(summary_rows) and usage_missing == 0 and not budget_overshot
        if result.success and not token_comparable:
            reasons = []
            if not summary_rows:
                reasons.append("no model usage was recorded")
            if usage_missing:
                reasons.append(f"{usage_missing} calls have missing provider usage")
            if budget_overshot:
                reasons.append(
                    f"provider total {consumed_tokens} exceeded budget {plan.budget.token_budget}"
                )
            result = result.model_copy(
                update={
                    "status": "failed",
                    "errors": [*result.errors, "token comparison invalid: " + "; ".join(reasons)],
                    "metadata": {
                        **result.metadata,
                        "token_comparable": False,
                    },
                }
            )
            result_path = store.write_stage_result(repetition, result)
        succeeded = result.success
        (successes if succeeded else failures).append(repetition)
        store.write_manifest(
            {
                "status": "completed" if succeeded else "failed",
                "stage": stage_name,
                "stage_result": result_path.relative_to(rep_dir).as_posix(),
                "token_usage_summary": {
                    "json": summary_json_path.relative_to(rep_dir).as_posix(),
                    "csv": summary_csv_path.relative_to(rep_dir).as_posix(),
                },
                "token_comparable": token_comparable,
                "usage_missing_count": usage_missing,
                "consumed_total_tokens": consumed_tokens,
                "token_budget_overshot": budget_overshot,
            },
            repetition=repetition,
        )

    complete = not failures
    store.write_manifest(
        {
            "status": "completed" if complete else "failed",
            "successful_repetitions": successes,
            "failed_repetitions": failures,
            "backend_available": (
                bool(os.environ.get(_http_config_for_plan(plan).api_key_env))
                if plan.parameters.get("backend") == "http"
                else _binary_available(str(plan.parameters.get("agent_binary", "traex")))
            ),
        }
    )
    print(f"{('completed' if complete else 'failed')}: {store.manifest_path}")
    return 0 if complete else EXIT_RUNTIME_FAILURE


def main(argv: Sequence[str] | None = None) -> int:
    """Parse ``argv``, show a pure plan, or execute every repetition."""

    selected_argv = list(sys.argv[1:] if argv is None else argv)
    if selected_argv[:1] == ["pipeline"]:
        return _pipeline_main(selected_argv[1:])
    parser = build_parser()
    args = parser.parse_args(selected_argv)
    try:
        mapping = _resolved_plan(args)
        plan = _with_input_snapshot(ExperimentPlan.from_mapping(mapping))
        if args.show_plan:
            _, stage, _ = _stage_selection(plan)
            _validate_baseline_run(plan, stage)
            mapping["parameters"]["input_snapshot"] = plan.parameters["input_snapshot"]
            print(json.dumps(mapping, indent=2, sort_keys=True))
            return 0
        _validate_execution_inputs(plan)
        backend = _standalone_backend(plan)
        return asyncio.run(_execute(plan, backend_impl=backend, resume=bool(args.resume)))
    except (TypeError, ValueError) as exc:
        print(f"defuzz-experiment: configuration error: {exc}", file=sys.stderr)
        return EXIT_CONFIGURATION_ERROR
    except (OSError, RuntimeError) as exc:
        print(f"defuzz-experiment: runtime failure: {exc}", file=sys.stderr)
        return EXIT_RUNTIME_FAILURE


if __name__ == "__main__":
    raise SystemExit(main())
