/* Trigger for the x86 IBT ENDBR-immediate bypass (DREV-2026-004).
 *
 * Build with:
 *   gcc -O2 -fcf-protection=branch -c source.c -o source.o
 * then:
 *   objdump -d -Mintel source.o
 *
 * Expected observation: the function `gadget` emits
 *   movabs rax, 0x1fa1e0ff3ab
 * whose encoded bytes place a valid endbr64 at `<gadget>+0x7`.
 *
 * If the predicate were correct, the constant should be loaded from
 * .rodata via RIP-relative addressing, not a direct immediate.
 *
 * Root cause (verbatim from DREV-2026-004):
 *   `gcc/config/i386/predicates.md` defines `ix86_endbr_immediate_operand`
 *   with a shift-scan loop that tests whole-word equality at every byte
 *   position. The test only matches when ALL upper bytes are zero, so a
 *   64-bit immediate with the ENDBR pattern at byte offset k AND any
 *   non-zero byte at offset >= k+4 is wrongly accepted as legal.
 *
 * Additional shapes that should all exhibit the same bug:
 *   0x7f00fa1e0ff3abULL
 *   0x01fa1e0ff3abULL
 *   0xffbafa1e0ff3abULL
 *   0xffffbafa1e0ff3abULL
 *   0xffbafa1e0ff3abdeULL
 * Each has the 4-byte ENDBR64 pattern at some non-lowest position
 * with non-zero bytes above it, bypassing the shift-scan.
 *
 * Source-of-truth: findings/DREV-2026-004/ (gitignored).
 */

unsigned long long gadget(void) {
    return 0x1fa1e0ff3abULL;
}
