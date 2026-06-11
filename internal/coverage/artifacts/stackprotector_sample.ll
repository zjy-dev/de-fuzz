; ModuleID = 'stackprotector_sample.cpp'
source_filename = "StackProtector.cpp"

; A hand-written sample resembling clang -S -emit-llvm -g output, used as a
; fixture for parsing basic blocks, successors, and !DILocation line numbers.

define i32 @_ZN13StackProtector13runOnFunctionEi(i32 %0) !dbg !4 {
entry:
  %retval = alloca i32, align 4, !dbg !7
  %cmp = icmp sgt i32 %0, 0, !dbg !8
  br i1 %cmp, label %if.then, label %if.else, !dbg !8

if.then:                                          ; preds = %entry
  store i32 1, ptr %retval, align 4, !dbg !9
  br label %sw.epilog, !dbg !10

if.else:                                          ; preds = %entry
  switch i32 %0, label %sw.default [
    i32 -1, label %sw.bb
    i32 -2, label %sw.bb2
  ], !dbg !11

sw.bb:                                             ; preds = %if.else
  store i32 2, ptr %retval, align 4, !dbg !12
  br label %sw.epilog, !dbg !12

sw.bb2:                                            ; preds = %if.else
  store i32 3, ptr %retval, align 4, !dbg !13
  br label %sw.epilog, !dbg !13

sw.default:                                        ; preds = %if.else
  store i32 0, ptr %retval, align 4, !dbg !14
  br label %sw.epilog, !dbg !14

sw.epilog:                                         ; preds = %sw.default, %sw.bb2, %sw.bb, %if.then
  %r = load i32, ptr %retval, align 4, !dbg !15
  ret i32 %r, !dbg !15
}

!llvm.dbg.cu = !{!0}
!llvm.module.flags = !{!3}

!0 = distinct !DICompileUnit(language: DW_LANG_C_plus_plus_14, file: !1, producer: "clang", isOptimized: false, runtimeVersion: 0, emissionKind: FullDebug)
!1 = !DIFile(filename: "StackProtector.cpp", directory: "/src/llvm/lib/CodeGen")
!3 = !{i32 2, !"Debug Info Version", i32 3}
!4 = distinct !DISubprogram(name: "runOnFunction", scope: !1, file: !1, line: 100, unit: !0)
!5 = !DIBasicType(name: "int", size: 32, encoding: DW_ATE_signed)
!7 = !DILocation(line: 100, column: 7, scope: !4)
!8 = !DILocation(line: 101, column: 9, scope: !4)
!9 = !DILocation(line: 102, column: 5, scope: !4)
!10 = !DILocation(line: 103, column: 5, scope: !4)
!11 = !DILocation(line: 105, column: 3, scope: !4)
!12 = !DILocation(line: 107, column: 5, scope: !4)
!13 = !DILocation(line: 109, column: 5, scope: !4)
!14 = !DILocation(line: 111, column: 5, scope: !4)
!15 = !DILocation(line: 114, column: 3, scope: !4)
