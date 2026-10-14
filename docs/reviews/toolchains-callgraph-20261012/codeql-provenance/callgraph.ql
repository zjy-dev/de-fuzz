/**
 * @name Global Call Graph for GCC/LLVM
 * @description Emits (caller, callee, edge_kind, file, line) tuples.
 *              Direct calls: fc.getTarget().
 *              Virtual dispatch: expanded via overriding-function walk.
 *              Indirect calls via function pointers/struct fields are
 *              intentionally omitted in this v1; they will be added later.
 * @kind table
 * @id defuzz/callgraph
 */

import cpp

predicate isVirtual(FunctionCall fc) { fc.getTarget() instanceof VirtualFunction }

Function virtualCallees(FunctionCall fc) {
  isVirtual(fc) and
  (
    result = fc.getTarget()
    or
    result = fc.getTarget().(VirtualFunction).getAnOverridingFunction+()
  )
}

from FunctionCall fc, Function caller, Function callee, string kind
where
  caller = fc.getEnclosingFunction() and
  (
    (not isVirtual(fc) and callee = fc.getTarget() and kind = "direct")
    or
    (isVirtual(fc) and callee = virtualCallees(fc) and kind = "virtual")
  )
select
  caller.getQualifiedName() as caller_name,
  callee.getQualifiedName() as callee_name,
  kind as edge_kind,
  fc.getLocation().getFile().getRelativePath() as file,
  fc.getLocation().getStartLine() as line
