package oracle

import "strings"

// fortifyProtectedFamilies lists the libc functions that glibc's
// `bits/*_fortified.h` headers wrap with `__<name>_chk` when
// `_FORTIFY_SOURCE >= 1` is in effect.
//
// The list is intentionally narrowed to the shapes we exercise from the
// FORTIFY seed template's argv modes — adding more is cheap (just append)
// but every entry implicitly requires a corresponding seed call in the
// template, otherwise W01 will produce false NA cases.
//
// Source: glibc 2.34+ `string/bits/string_fortified.h`,
// `wcsmbs/bits/wchar2.h`, `libio/bits/stdio2.h`, `posix/bits/unistd.h`.
var fortifyProtectedFamilies = []string{
	"memcpy",
	"memmove",
	"memset",
	"mempcpy",
	"strcpy",
	"stpcpy",
	"strncpy",
	"strcat",
	"strncat",
	"snprintf",
	"vsnprintf",
	"sprintf",
	"vsprintf",
	"fprintf",
	"vfprintf",
	"printf",
	"vprintf",
	"dprintf",
	"asprintf",
	"read",
	"pread",
	"readlink",
	"gets",
	"fgets",
}

// chkSymbolFor returns the glibc `__<name>_chk` symbol corresponding to
// `family`. The mapping is the trivial `__<family>_chk` shape; glibc
// occasionally adds `_chk_warn` / `_chk_pre` suffixes for variants but
// the base `_chk` is what every wrapper emits.
func chkSymbolFor(family string) string {
	return "__" + family + "_chk"
}

// errWarnFamilies lists the BSD/GNU diagnostic functions whose internal
// `vfprintf` is reachable from user input via `%n`. INV-FORT-C02 asserts
// these must have `_chk` wrappers; sourceware 24987 reports glibc has
// never shipped such wrappers.
var errWarnFamilies = []string{
	"err", "errx", "warn", "warnx",
	"verr", "verrx", "vwarn", "vwarnx",
	"error", "error_at_line",
}

// errWarnChkFamilies lists the *expected* `_chk` wrapper names. They do
// not exist in mainline glibc today; if any one of them ever shows up in
// a binary that also calls the bare variant, INV-FORT-C02 should flip to
// Pass. We keep both lists so the Detail map can show which side was
// observed.
var errWarnChkFamilies = []string{
	"__err_chk", "__errx_chk", "__warn_chk", "__warnx_chk",
	"__verr_chk", "__verrx_chk", "__vwarn_chk", "__vwarnx_chk",
	"__error_chk", "__error_at_line_chk",
}

// printfEntries enumerates the FORTIFY-instrumented `printf`-family
// entry points exercised by INV-FORT-C01 in the dynamic template. Each
// entry has a corresponding `printf:<entry>` argv mode; the dynamic
// checker reads `oracle.options.fortify.printf_entries` to narrow the
// set when desired.
var printfEntries = []string{
	"printf",
	"sprintf",
	"snprintf",
	"vsnprintf",
	"syslog",
}

// stringSetContainsAny reports whether any element of `needles` appears
// in `haystack`. Helper kept package-private so it stays narrowly scoped
// to fortify-checker code that does many small membership tests.
func stringSetContainsAny(haystack []string, needles ...string) bool {
	if len(haystack) == 0 || len(needles) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(haystack))
	for _, h := range haystack {
		set[h] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; ok {
			return true
		}
	}
	return false
}

// chkSymbolFamilyName extracts the `family` part of a `__family_chk`
// symbol name, returning "" if the name does not look like a fortify
// chk wrapper. Used by reports and by tests; keeping it next to the
// table avoids a separate parsing helper.
func chkSymbolFamilyName(symbol string) string {
	if !strings.HasPrefix(symbol, "__") || !strings.HasSuffix(symbol, "_chk") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(symbol, "__"), "_chk")
}
