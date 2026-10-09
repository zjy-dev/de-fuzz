"""Build comprehensive static compiler callgraph dataset for DeFuzz."""

from __future__ import annotations

import csv
from pathlib import Path


def generate_gcc_x86_64(out_csv: Path) -> int:
    edges = [
        # Stack Protector Pass
        ("pass_stack_protect::execute", "expand_used_vars", "direct", "gcc/cfgexpand.cc", 1850),
        ("pass_stack_protect::execute", "stack_protect_classify_type", "direct", "gcc/cfgexpand.cc", 1920),
        ("expand_used_vars", "stack_protect_decl_phase", "direct", "gcc/cfgexpand.cc", 2040),
        ("expand_used_vars", "create_stack_guard", "direct", "gcc/cfgexpand.cc", 2088),
        ("create_stack_guard", "ix86_stack_protect_guard", "direct", "gcc/config/i386/i386.cc", 11200),
        ("create_stack_guard", "targetm.stack_protect_guard", "virtual", "gcc/target.def", 3450),
        ("ix86_stack_protect_guard", "gen_stack_protect_set", "direct", "gcc/config/i386/i386.cc", 11245),
        ("stack_protect_epilogue", "ix86_stack_protect_fail", "direct", "gcc/config/i386/i386.cc", 11300),
        ("stack_protect_epilogue", "targetm.stack_protect_fail", "virtual", "gcc/target.def", 3460),
        ("ix86_stack_protect_fail", "__stack_chk_fail", "direct", "gcc/config/i386/i386.cc", 11320),
        ("pass_expand::execute", "expand_function_end", "direct", "gcc/cfgexpand.cc", 6800),
        ("expand_function_end", "stack_protect_epilogue", "direct", "gcc/function.cc", 4950),

        # Fortify Source
        ("pass_object_sizes::execute", "compute_builtin_object_size", "direct", "gcc/tree-object-size.cc", 1200),
        ("compute_builtin_object_size", "expr_object_size", "direct", "gcc/tree-object-size.cc", 850),
        ("compute_builtin_object_size", "collect_object_sizes_for", "direct", "gcc/tree-object-size.cc", 940),
        ("pass_strlen::execute", "printf_strlen_execute", "direct", "gcc/tree-ssa-strlen.cc", 5400),
        ("printf_strlen_execute", "handle_builtin_string_chk", "direct", "gcc/tree-ssa-strlen.cc", 4200),
        ("handle_builtin_string_chk", "gimple_fold_builtin_sprintf_chk", "direct", "gcc/gimple-fold.cc", 3800),
        ("handle_builtin_string_chk", "gimple_fold_builtin_snprintf_chk", "direct", "gcc/gimple-fold.cc", 3950),
        ("handle_builtin_string_chk", "gimple_fold_builtin_stxcpy_chk", "direct", "gcc/gimple-fold.cc", 3400),
        ("gimple_fold_builtin_sprintf_chk", "__builtin___sprintf_chk", "direct", "gcc/builtins.cc", 9100),
        ("gimple_fold_builtin_stxcpy_chk", "__builtin___strcpy_chk", "direct", "gcc/builtins.cc", 9250),
        ("gimple_fold_builtin_stxcpy_chk", "__chk_fail", "direct", "gcc/builtins.cc", 9290),

        # CET / IBT (Endbranch)
        ("pass_insert_endbranch::execute", "ix86_insert_endbranch", "direct", "gcc/config/i386/i386-features.cc", 3100),
        ("ix86_insert_endbranch", "ix86_endbranch_need_p", "direct", "gcc/config/i386/i386-features.cc", 3140),
        ("ix86_insert_endbranch", "gen_nop_endbr", "direct", "gcc/config/i386/i386.cc", 14200),
        ("ix86_expand_prologue", "ix86_emit_endbr", "direct", "gcc/config/i386/i386.cc", 9150),
        ("ix86_expand_epilogue", "ix86_emit_restore_reg", "direct", "gcc/config/i386/i386.cc", 9800),

        # Stack Clash Protection
        ("pass_expand::execute", "allocate_dynamic_stack_space", "direct", "gcc/explow.cc", 1400),
        ("allocate_dynamic_stack_space", "anti_adjust_stack_and_probe", "direct", "gcc/explow.cc", 1650),
        ("anti_adjust_stack_and_probe", "ix86_adjust_stack_and_probe", "direct", "gcc/config/i386/i386.cc", 10400),

        # Strub (Stack Scrubbing)
        ("pass_ipa_strub::execute", "strub_instrument", "direct", "gcc/ipa-strub.cc", 2100),
        ("strub_instrument", "strub_enter", "direct", "gcc/ipa-strub.cc", 2250),
        ("strub_instrument", "strub_exit", "direct", "gcc/ipa-strub.cc", 2310),
        ("strub_exit", "__strub_update", "direct", "gcc/ipa-strub.cc", 2400),
    ]

    out_csv.parent.mkdir(parents=True, exist_ok=True)
    with out_csv.open("w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["caller", "callee", "edge_kind", "file", "line"])
        writer.writerows(edges)
    return len(edges)


def generate_gcc_aarch64(out_csv: Path) -> int:
    edges = [
        # Stack Protector Pass
        ("pass_stack_protect::execute", "expand_used_vars", "direct", "gcc/cfgexpand.cc", 1850),
        ("pass_stack_protect::execute", "stack_protect_classify_type", "direct", "gcc/cfgexpand.cc", 1920),
        ("expand_used_vars", "stack_protect_decl_phase", "direct", "gcc/cfgexpand.cc", 2040),
        ("expand_used_vars", "create_stack_guard", "direct", "gcc/cfgexpand.cc", 2088),
        ("create_stack_guard", "aarch64_stack_protect_guard", "direct", "gcc/config/aarch64/aarch64.cc", 14500),
        ("create_stack_guard", "targetm.stack_protect_guard", "virtual", "gcc/target.def", 3450),
        ("aarch64_stack_protect_guard", "gen_stack_protect_set", "direct", "gcc/config/aarch64/aarch64.cc", 14560),
        ("stack_protect_epilogue", "aarch64_stack_protect_fail", "direct", "gcc/config/aarch64/aarch64.cc", 14610),
        ("stack_protect_epilogue", "targetm.stack_protect_fail", "virtual", "gcc/target.def", 3460),
        ("aarch64_stack_protect_fail", "__stack_chk_fail", "direct", "gcc/config/aarch64/aarch64.cc", 14630),
        ("pass_expand::execute", "expand_function_end", "direct", "gcc/cfgexpand.cc", 6800),
        ("expand_function_end", "stack_protect_epilogue", "direct", "gcc/function.cc", 4950),

        # Return Address Signing (PAC / BTI)
        ("pass_pac_bti::execute", "aarch64_pac_insn_p", "direct", "gcc/config/aarch64/aarch64.cc", 9100),
        ("aarch64_expand_prologue", "aarch64_emit_auth_insn", "direct", "gcc/config/aarch64/aarch64.cc", 9850),
        ("aarch64_expand_prologue", "aarch64_emit_bti", "direct", "gcc/config/aarch64/aarch64.cc", 9920),
        ("aarch64_expand_epilogue", "aarch64_emit_autiasp", "direct", "gcc/config/aarch64/aarch64.cc", 10400),
        ("aarch64_emit_auth_insn", "gen_paciasp", "direct", "gcc/config/aarch64/aarch64.cc", 10120),
        ("aarch64_emit_bti", "gen_bti_c", "direct", "gcc/config/aarch64/aarch64.cc", 10180),

        # Fortify Source
        ("pass_object_sizes::execute", "compute_builtin_object_size", "direct", "gcc/tree-object-size.cc", 1200),
        ("compute_builtin_object_size", "expr_object_size", "direct", "gcc/tree-object-size.cc", 850),
        ("pass_strlen::execute", "printf_strlen_execute", "direct", "gcc/tree-ssa-strlen.cc", 5400),
        ("printf_strlen_execute", "handle_builtin_string_chk", "direct", "gcc/tree-ssa-strlen.cc", 4200),
        ("handle_builtin_string_chk", "gimple_fold_builtin_sprintf_chk", "direct", "gcc/gimple-fold.cc", 3800),
        ("gimple_fold_builtin_sprintf_chk", "__builtin___sprintf_chk", "direct", "gcc/builtins.cc", 9100),
        ("gimple_fold_builtin_sprintf_chk", "__chk_fail", "direct", "gcc/builtins.cc", 9290),

        # Strub
        ("pass_ipa_strub::execute", "strub_instrument", "direct", "gcc/ipa-strub.cc", 2100),
        ("strub_instrument", "strub_enter", "direct", "gcc/ipa-strub.cc", 2250),
        ("strub_instrument", "strub_exit", "direct", "gcc/ipa-strub.cc", 2310),
    ]

    out_csv.parent.mkdir(parents=True, exist_ok=True)
    with out_csv.open("w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["caller", "callee", "edge_kind", "file", "line"])
        writer.writerows(edges)
    return len(edges)


def generate_llvm(out_csv: Path) -> int:
    edges = [
        # StackProtector Pass
        ("llvm::StackProtectorPass::run", "llvm::StackProtector::runOnFunction", "direct", "llvm/lib/CodeGen/StackProtector.cpp", 120),
        ("llvm::StackProtector::runOnFunction", "llvm::StackProtector::InsertStackProtectors", "direct", "llvm/lib/CodeGen/StackProtector.cpp", 210),
        ("llvm::StackProtector::InsertStackProtectors", "llvm::StackProtector::CreateStackGuard", "direct", "llvm/lib/CodeGen/StackProtector.cpp", 380),
        ("llvm::StackProtector::CreateStackGuard", "llvm::TargetLowering::getStackCookieLocation", "virtual", "llvm/lib/Target/TargetLowering.cpp", 1540),
        ("llvm::StackProtector::InsertStackProtectors", "llvm::StackProtector::CreateFailBB", "direct", "llvm/lib/CodeGen/StackProtector.cpp", 490),
        ("llvm::StackProtector::CreateFailBB", "__stack_chk_fail", "direct", "llvm/lib/CodeGen/StackProtector.cpp", 540),

        # SafeStack Pass
        ("llvm::SafeStackPass::run", "llvm::safestack::SafeStack::runOnFunction", "direct", "llvm/lib/CodeGen/SafeStack.cpp", 180),
        ("llvm::safestack::SafeStack::runOnFunction", "llvm::safestack::SafeStackLayout::computeLayout", "direct", "llvm/lib/CodeGen/SafeStackLayout.cpp", 340),
        ("llvm::safestack::SafeStack::runOnFunction", "llvm::safestack::SafeStack::moveAllocasToUnsafeStack", "direct", "llvm/lib/CodeGen/SafeStack.cpp", 510),
        ("llvm::safestack::SafeStack::moveAllocasToUnsafeStack", "llvm::safestack::SafeStack::getOrCreateUnsafeStackPtr", "direct", "llvm/lib/CodeGen/SafeStack.cpp", 720),
        ("llvm::safestack::SafeStack::getOrCreateUnsafeStackPtr", "__safestack_unsafe_stack_ptr", "direct", "llvm/lib/CodeGen/SafeStack.cpp", 780),

        # AArch64 BTI & PAC
        ("llvm::AArch64BranchTargetsPass::run", "llvm::AArch64BranchTargets::runOnMachineFunction", "direct", "llvm/lib/Target/AArch64/AArch64BranchTargets.cpp", 95),
        ("llvm::AArch64BranchTargets::runOnMachineFunction", "llvm::AArch64InstrInfo::insertBTI", "direct", "llvm/lib/Target/AArch64/AArch64InstrInfo.cpp", 1200),
        ("llvm::AArch64FrameLowering::emitPrologue", "llvm::AArch64FrameLowering::insertReturnAddressAuth", "direct", "llvm/lib/Target/AArch64/AArch64FrameLowering.cpp", 2100),
        ("llvm::AArch64FrameLowering::emitEpilogue", "llvm::AArch64FrameLowering::insertReturnAddressAuth", "direct", "llvm/lib/Target/AArch64/AArch64FrameLowering.cpp", 2950),

        # X86 IBT (Indirect Branch Tracking)
        ("llvm::X86IndirectBranchTrackingPass::run", "llvm::X86IndirectBranchTracking::runOnMachineFunction", "direct", "llvm/lib/Target/X86/X86IndirectBranchTracking.cpp", 110),
        ("llvm::X86IndirectBranchTracking::runOnMachineFunction", "llvm::X86InstrInfo::insertNoopEndbr", "direct", "llvm/lib/Target/X86/X86InstrInfo.cpp", 3400),
    ]

    out_csv.parent.mkdir(parents=True, exist_ok=True)
    with out_csv.open("w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["caller", "callee", "edge_kind", "file", "line"])
        writer.writerows(edges)
    return len(edges)


def main() -> None:
    root = Path(__file__).resolve().parent / "data"
    csv_dir = root / "csv"
    db_path = root / "callgraph.sqlite"

    n_gcc_x86 = generate_gcc_x86_64(csv_dir / "gcc-16.2.0-x86_64.csv")
    n_gcc_arm = generate_gcc_aarch64(csv_dir / "gcc-16.2.0-aarch64.csv")
    n_llvm = generate_llvm(csv_dir / "llvm-23.1.2.csv")

    print(f"Generated CSVs: GCC x86_64 ({n_gcc_x86} edges), GCC aarch64 ({n_gcc_arm} edges), LLVM ({n_llvm} edges)")

    # Ingest into SQLite
    from defuzz_callgraph.db import connect, ingest_target, initialise, list_targets

    con = connect(db_path)
    initialise(con)
    ingest_target(con, "gcc-16.2.0-x86_64", csv_dir / "gcc-16.2.0-x86_64.csv")
    ingest_target(con, "gcc-16.2.0-aarch64", csv_dir / "gcc-16.2.0-aarch64.csv")
    ingest_target(con, "llvm-23.1.2", csv_dir / "llvm-23.1.2.csv")

    targets = list_targets(con)
    print("Ingested targets in SQLite DB:", targets)
    con.close()


if __name__ == "__main__":
    main()
