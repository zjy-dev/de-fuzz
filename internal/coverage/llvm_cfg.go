package coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LLVM IR parsing regexes.
var (
	// define ... @name(...) ... {   (capture the function symbol after '@')
	reLLVMDefine = regexp.MustCompile(`^\s*define\b.*@([A-Za-z0-9_$.]+)\s*\(`)
	// A basic block label line: "label:" possibly with trailing "; preds = ..."
	reLLVMLabel = regexp.MustCompile(`^([A-Za-z0-9_.$-]+):`)
	// !dbg !N  -> reference id
	reLLVMDbgRef = regexp.MustCompile(`!dbg\s+!(\d+)`)
	// !N = !DILocation(line: X, ...)
	reLLVMDILocation = regexp.MustCompile(`^!(\d+)\s*=\s*!DILocation\(line:\s*(\d+)`)
	// !N = ... !DIFile(filename: "F", directory: "D")
	reLLVMDIFile = regexp.MustCompile(`!DIFile\(filename:\s*"([^"]*)",\s*directory:\s*"([^"]*)"`)
	// br label %X
	reLLVMBrUncond = regexp.MustCompile(`^\s*br\s+label\s+%([A-Za-z0-9_.$-]+)`)
	// any "label %X" occurrence (for conditional br / switch / invoke targets)
	reLLVMLabelRef = regexp.MustCompile(`label\s+%([A-Za-z0-9_.$-]+)`)
)

// ParseLLVMIRFiles parses one or more LLVM .ll files and returns the CFG
// functions for the requested target functions (matched by demangled or mangled
// name). If targetFunctions is empty, all functions are returned.
func ParseLLVMIRFiles(irPaths []string, targetFunctions []string, demanglerCommand string) (map[string]*CFGFunction, error) {
	merged := make(map[string]*CFGFunction)
	for _, p := range irPaths {
		funcs, err := parseLLVMIRFile(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse IR file %s: %w", filepath.Base(p), err)
		}
		for name, fn := range funcs {
			merged[name] = fn
		}
	}

	if len(targetFunctions) == 0 {
		return merged, nil
	}

	// Build a matcher for the requested target functions (exact + simplified).
	matcher := newTargetFunctionMatcher()
	for _, fn := range targetFunctions {
		matcher.add(fn)
	}

	// Optionally demangle the parsed (mangled) names for matching.
	demangled := demangleNames(keysOf(merged), demanglerCommand)

	result := make(map[string]*CFGFunction)
	for mangled, fn := range merged {
		human := demangled[mangled]
		if matcher.matches(human) || matcher.matches(mangled) {
			// Key the result by the demangled (human) name so it matches the
			// target functions passed to the analyzer.
			fn.Name = human
			fn.MangledName = mangled
			result[human] = fn
		}
	}
	return result, nil
}

// parseLLVMIRFile parses a single .ll file into CFGFunctions keyed by mangled name.
func parseLLVMIRFile(path string) (map[string]*CFGFunction, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	type rawBlock struct {
		id      int
		label   string
		dbgRefs []int
		succs   []string // successor labels
	}

	functions := make(map[string]*CFGFunction)
	// metadata maps collected in a first pass would require two passes; we read
	// all lines first so DILocation/DIFile (which appear after function bodies)
	// are available.
	var lines []string
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading IR file: %w", err)
	}

	// Pass 1: metadata tables.
	dbgLine := make(map[int]int)    // !N (DILocation) -> source line
	dbgFile := make(map[int]string) // !N (DILocation) -> filename (from scope chain, best-effort)
	// Collect DIFile by node id and DISubprogram/DILocation file references.
	diFileByID := make(map[int]string)
	for _, line := range lines {
		if m := reLLVMDILocation.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			ln, _ := strconv.Atoi(m[2])
			dbgLine[id] = ln
		}
		if strings.HasPrefix(line, "!") {
			if m := reLLVMDIFile.FindStringSubmatch(line); m != nil {
				if idm := regexp.MustCompile(`^!(\d+)`).FindStringSubmatch(line); idm != nil {
					id, _ := strconv.Atoi(idm[1])
					dir := m[2]
					fn := m[1]
					if dir != "" {
						diFileByID[id] = filepath.ToSlash(filepath.Join(dir, fn))
					} else {
						diFileByID[id] = filepath.ToSlash(fn)
					}
				}
			}
		}
	}
	// Best-effort: pick the single DIFile as the file for all blocks if exactly
	// one exists (typical for one source file per .ll). Otherwise leave empty and
	// rely on per-scope resolution (not implemented for the simplified version).
	var defaultFile string
	if len(diFileByID) == 1 {
		for _, f := range diFileByID {
			defaultFile = f
		}
	}
	_ = dbgFile

	// Pass 2: functions and basic blocks.
	var (
		curName   string
		inFunc    bool
		nextBBID  int
		labelToID map[string]int
		blocks    []*rawBlock
		curBlock  *rawBlock
		inSwitch  bool
	)

	flush := func() {
		if curName == "" {
			return
		}
		fn := &CFGFunction{
			Name:        curName,
			MangledName: curName,
			Blocks:      make(map[int]*BasicBlock),
			SuccsMap:    make(map[int][]int),
		}
		for _, rb := range blocks {
			bb := &BasicBlock{
				ID:       rb.id,
				Function: curName,
				File:     defaultFile,
				Lines:    []int{},
			}
			lineSet := make(map[int]bool)
			for _, ref := range rb.dbgRefs {
				if ln, ok := dbgLine[ref]; ok && !lineSet[ln] {
					lineSet[ln] = true
					bb.Lines = append(bb.Lines, ln)
				}
			}
			sort.Ints(bb.Lines)
			// Resolve successor labels to BB IDs.
			for _, succLabel := range rb.succs {
				if sid, ok := labelToID[succLabel]; ok {
					bb.Successors = append(bb.Successors, sid)
				}
			}
			fn.Blocks[rb.id] = bb
			fn.SuccsMap[rb.id] = bb.Successors
		}
		functions[curName] = fn
	}

	startFunc := func(name string) {
		curName = name
		inFunc = true
		nextBBID = 2 // start at 2 to align with Analyzer's bbID>1 convention
		labelToID = make(map[string]int)
		blocks = nil
		curBlock = &rawBlock{id: nextBBID, label: "entry"}
		nextBBID++
		labelToID["entry"] = curBlock.id
		blocks = append(blocks, curBlock)
		inSwitch = false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inFunc {
			if m := reLLVMDefine.FindStringSubmatch(line); m != nil {
				startFunc(m[1])
			}
			continue
		}

		// End of function body.
		if trimmed == "}" {
			flush()
			curName = ""
			inFunc = false
			curBlock = nil
			continue
		}

		// New basic block label.
		if m := reLLVMLabel.FindStringSubmatch(line); m != nil && !strings.HasPrefix(trimmed, ";") {
			label := m[1]
			id, ok := labelToID[label]
			if !ok {
				id = nextBBID
				nextBBID++
				labelToID[label] = id
			}
			curBlock = &rawBlock{id: id, label: label}
			blocks = append(blocks, curBlock)
			continue
		}

		if curBlock == nil {
			continue
		}

		// Collect !dbg references.
		if m := reLLVMDbgRef.FindStringSubmatch(line); m != nil {
			ref, _ := strconv.Atoi(m[1])
			curBlock.dbgRefs = append(curBlock.dbgRefs, ref)
		}

		// A switch spans multiple lines:
		//   switch i32 %x, label %default [
		//     i32 1, label %a
		//     i32 2, label %b
		//   ]
		// Collect labels across the whole construct until the closing ']'.
		if inSwitch {
			for _, lm := range reLLVMLabelRef.FindAllStringSubmatch(line, -1) {
				curBlock.succs = appendUnique(curBlock.succs, lm[1])
			}
			if strings.Contains(trimmed, "]") {
				inSwitch = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "switch") {
			for _, lm := range reLLVMLabelRef.FindAllStringSubmatch(line, -1) {
				curBlock.succs = appendUnique(curBlock.succs, lm[1])
			}
			// If the switch table is not closed on this line, keep consuming.
			if !strings.Contains(trimmed, "]") {
				inSwitch = true
			}
			continue
		}

		// Collect successors from single-line terminator instructions.
		if reLLVMBrUncond.MatchString(line) || strings.Contains(trimmed, "br i1") ||
			strings.HasPrefix(trimmed, "invoke") {
			for _, lm := range reLLVMLabelRef.FindAllStringSubmatch(line, -1) {
				curBlock.succs = appendUnique(curBlock.succs, lm[1])
			}
		}
	}
	// Flush a function missing a closing brace (defensive).
	if inFunc {
		flush()
	}

	return functions, nil
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func keysOf(m map[string]*CFGFunction) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// demangleNames demangles a list of mangled names using the configured command.
// On any failure (or empty command), names map to themselves.
func demangleNames(names []string, demanglerCommand string) map[string]string {
	result := make(map[string]string, len(names))
	for _, n := range names {
		result[n] = n
	}
	if demanglerCommand == "" || len(names) == 0 {
		return result
	}
	// Use the shared executor-free path: shell out via the command directly.
	// We avoid importing exec here to keep the parser dependency-light; callers
	// that need demangling at parse time can pass the command and we run it with
	// a minimal os/exec call.
	out, err := runDemangler(demanglerCommand, names)
	if err != nil {
		return result
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(names) {
		return result
	}
	for i, n := range names {
		if s := strings.TrimSpace(lines[i]); s != "" {
			result[n] = s
		}
	}
	return result
}

// runDemangler runs the demangler command with the names as positional args,
// returning its stdout. llvm-cxxfilt and c++filt both accept names as args and
// print one demangled name per line.
func runDemangler(command string, names []string) (string, error) {
	cmd := osexec.Command(command, names...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
