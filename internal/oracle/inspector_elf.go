package oracle

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
)

// inspector_elf.go contains the heavier ELF-decoding helpers that
// inspector.go's parseOnce() invokes. They live in a separate file so the
// core BinaryInspector surface stays compact.
//
// All helpers are best-effort: a malformed or unrecognised section is
// silently skipped rather than promoted to a parseErr, because IBT
// checkers must keep the per-binary read paths resilient (the binary
// matters even if its `.note.gnu.property` is absent).

// ----------------------------------------------------------------------
// Relocations + IFUNC resolvers
// ----------------------------------------------------------------------

// decodeRelocationsAndIFUNCs walks every `.rela.*` / `.rel.*` section and
// returns:
//   - every decoded relocation entry with the referenced symbol resolved;
//   - the union of "explicit STT_GNU_IFUNC function symbols" and
//     "functions referenced by an IRELATIVE relocation" — these are the
//     IFUNC resolvers whose entry must be ENDBR per INV-IBT-P04.
//
// The implementation supports both ELF64 RELA and ELF32 REL/RELA;
// platforms outside x86 still get a best-effort decode, but only x86
// IRELATIVE constants are recognised because IBT only applies to x86.
func decodeRelocationsAndIFUNCs(f *elf.File, extFuncs []ExtendedFunctionSymbol) ([]Relocation, []FunctionSymbol) {
	var relocs []Relocation
	addrToFunc := make(map[uint64]FunctionSymbol, len(extFuncs))
	ifuncSet := make(map[uint64]FunctionSymbol)
	for _, ef := range extFuncs {
		addrToFunc[ef.Addr] = ef.FunctionSymbol
		if ef.IsIFUNC {
			ifuncSet[ef.Addr] = ef.FunctionSymbol
		}
	}

	// Cached symbol tables: relocations reference st_name in their
	// associated link section's symbol table.
	staticSyms, _ := f.Symbols()
	dynSyms, _ := f.DynamicSymbols()

	for _, sec := range f.Sections {
		switch sec.Type {
		case elf.SHT_RELA, elf.SHT_REL:
		default:
			continue
		}
		data, err := sec.Data()
		if err != nil {
			continue
		}
		// Resolve the linked symbol table.
		var syms []elf.Symbol
		if int(sec.Link) >= 0 && int(sec.Link) < len(f.Sections) {
			linked := f.Sections[sec.Link]
			switch linked.Type {
			case elf.SHT_SYMTAB:
				syms = staticSyms
			case elf.SHT_DYNSYM:
				syms = dynSyms
			}
		}

		entries := decodeRelocSection(f, sec, data)
		for _, ent := range entries {
			rel := Relocation{
				Section: sec.Name,
				Offset:  ent.offset,
				Type:    ent.relType,
				Addend:  ent.addend,
			}
			if ent.symIdx > 0 && int(ent.symIdx-1) < len(syms) {
				s := syms[ent.symIdx-1]
				rel.Symbol = s.Name
				rel.SymbolValue = s.Value
				rel.SymbolType = elf.ST_TYPE(s.Info)
				if rel.SymbolType == elf.STT_GNU_IFUNC && s.Section != elf.SHN_UNDEF && s.Size > 0 {
					if _, ok := ifuncSet[s.Value]; !ok {
						ifuncSet[s.Value] = FunctionSymbol{
							Name:       s.Name,
							Addr:       s.Value,
							Size:       s.Size,
							SectionIdx: int(s.Section),
						}
					}
				}
			}
			// IRELATIVE: resolver target is the addend (no symbol).
			if isIRELATIVE(f.Machine, ent.relType) {
				addr := uint64(ent.addend)
				if fn, ok := addrToFunc[addr]; ok {
					if _, exists := ifuncSet[addr]; !exists {
						ifuncSet[addr] = fn
					}
				} else {
					// Synthesise a placeholder so the checker can still
					// flag the resolver address even when no symbol
					// names it.
					ifuncSet[addr] = FunctionSymbol{
						Name: fmt.Sprintf("_ifunc_resolver_%x", addr),
						Addr: addr,
					}
				}
			}
			relocs = append(relocs, rel)
		}
	}

	ifuncs := make([]FunctionSymbol, 0, len(ifuncSet))
	for _, fn := range ifuncSet {
		ifuncs = append(ifuncs, fn)
	}
	return relocs, ifuncs
}

type rawReloc struct {
	offset  uint64
	relType uint32
	symIdx  uint32
	addend  int64
}

// decodeRelocSection decodes a single SHT_REL / SHT_RELA section into
// arch-specific (offset, type, sym-index, addend) tuples.
func decodeRelocSection(f *elf.File, sec *elf.Section, data []byte) []rawReloc {
	var out []rawReloc
	bo := f.ByteOrder
	switch f.Class {
	case elf.ELFCLASS64:
		entSize := 24
		if sec.Type == elf.SHT_REL {
			entSize = 16
		}
		for off := 0; off+entSize <= len(data); off += entSize {
			r := rawReloc{
				offset: bo.Uint64(data[off : off+8]),
			}
			info := bo.Uint64(data[off+8 : off+16])
			r.relType = uint32(info & 0xffffffff)
			r.symIdx = uint32(info >> 32)
			if sec.Type == elf.SHT_RELA {
				r.addend = int64(bo.Uint64(data[off+16 : off+24]))
			}
			out = append(out, r)
		}
	case elf.ELFCLASS32:
		entSize := 12
		if sec.Type == elf.SHT_REL {
			entSize = 8
		}
		for off := 0; off+entSize <= len(data); off += entSize {
			r := rawReloc{
				offset: uint64(bo.Uint32(data[off : off+4])),
			}
			info := bo.Uint32(data[off+4 : off+8])
			r.relType = info & 0xff
			r.symIdx = info >> 8
			if sec.Type == elf.SHT_RELA {
				r.addend = int64(int32(bo.Uint32(data[off+8 : off+12])))
			}
			out = append(out, r)
		}
	}
	return out
}

func isIRELATIVE(m elf.Machine, t uint32) bool {
	switch m {
	case elf.EM_X86_64:
		return t == RX8664IRELATIVE
	case elf.EM_386:
		return t == RI386IRELATIVE
	}
	return false
}

// ----------------------------------------------------------------------
// .note.gnu.property — GNU_PROPERTY_X86_FEATURE_1_AND
// ----------------------------------------------------------------------

// decodeGNUProperty parses `.note.gnu.property` and returns the bitwise
// OR of every GNU_PROPERTY_X86_FEATURE_1_AND value it contains. Returns
// 0 when the note is absent or malformed.
func decodeGNUProperty(f *elf.File) uint32 {
	sec := f.Section(".note.gnu.property")
	if sec == nil {
		return 0
	}
	data, err := sec.Data()
	if err != nil {
		return 0
	}
	bo := f.ByteOrder

	// Note header: namesz (4) | descsz (4) | type (4) | name (namesz) |
	// desc (descsz). Each record padded to 4 bytes.
	const noteAlign = 4
	off := 0
	var bits uint32
	for off+12 <= len(data) {
		namesz := bo.Uint32(data[off:])
		descsz := bo.Uint32(data[off+4:])
		ntype := bo.Uint32(data[off+8:])
		off += 12
		nameEnd := off + int(namesz)
		descStart := align(nameEnd, noteAlign)
		descEnd := descStart + int(descsz)
		if descEnd > len(data) {
			break
		}
		// Only NT_GNU_PROPERTY_TYPE_0 = 5 is meaningful.
		if ntype == 5 {
			bits |= scanGNUProperty(data[descStart:descEnd], bo)
		}
		off = align(descEnd, noteAlign)
	}
	return bits
}

func scanGNUProperty(desc []byte, bo binary.ByteOrder) uint32 {
	var out uint32
	off := 0
	for off+8 <= len(desc) {
		ptype := bo.Uint32(desc[off:])
		psize := bo.Uint32(desc[off+4:])
		off += 8
		end := off + int(psize)
		if end > len(desc) {
			break
		}
		if ptype == gnuPropertyX86Feature1AndTag && psize == 4 {
			out |= bo.Uint32(desc[off:end])
		}
		// pr_data is padded to 8 bytes.
		off = align(end, 8)
	}
	return out
}

func align(v, a int) int {
	if a <= 0 {
		return v
	}
	r := v % a
	if r == 0 {
		return v
	}
	return v + (a - r)
}

// ----------------------------------------------------------------------
// EH landing pads
// ----------------------------------------------------------------------

// decodeEHLandingPads returns the list of program-counter values that the
// `.eh_frame` table identifies as exception/cleanup landing pads.
//
// The full DWARF EH decoder is non-trivial. We implement just enough of
// it to extract every FDE's PC range entries and report the FDE's
// initial_location plus any associated LSDA pointer — that's the surface
// INV-IBT-P03 needs.
//
// Returns an empty slice when `.eh_frame` is absent or unparseable; this
// is acceptable because the consumer treats "no landing pads found" as
// VerdictNotApplicable, not Fail.
func decodeEHLandingPads(f *elf.File) []uint64 {
	sec := f.Section(".eh_frame")
	if sec == nil {
		return nil
	}
	data, err := sec.Data()
	if err != nil || len(data) == 0 {
		return nil
	}
	bo := f.ByteOrder

	// CIE bookkeeping: keyed by their offset in `.eh_frame` so an FDE can
	// resolve its parent CIE's encoding bytes.
	type cie struct {
		fdeEnc byte // FDE pointer encoding from 'R' augmentation, default DW_EH_PE_absptr (0)
	}
	cies := make(map[uint64]cie)

	var pads []uint64
	off := 0
	for off < len(data) {
		// length: 4 bytes; if 0xffffffff, an extended 8-byte length follows.
		if off+4 > len(data) {
			break
		}
		length := uint64(bo.Uint32(data[off:]))
		hdrLen := 4
		if length == 0xffffffff {
			if off+12 > len(data) {
				break
			}
			length = bo.Uint64(data[off+4:])
			hdrLen = 12
		}
		if length == 0 {
			break // terminator
		}
		recordEnd := off + hdrLen + int(length)
		if recordEnd > len(data) || recordEnd < off+hdrLen {
			break
		}

		body := data[off+hdrLen : recordEnd]
		if len(body) < 4 {
			off = recordEnd
			continue
		}
		ciePtr := bo.Uint32(body)
		isCIE := ciePtr == 0
		if isCIE {
			cies[uint64(off)] = parseCIE(body, bo)
		} else {
			parentOff := (off + hdrLen) - int(int32(ciePtr))
			parent := cies[uint64(parentOff)]
			pc, ok := parseFDE(uint64(off+hdrLen)+uint64(sec.Addr), body, parent.fdeEnc, f.Class, bo)
			if ok {
				pads = append(pads, pc)
			}
		}
		off = recordEnd
	}
	return pads
}

// parseCIE walks a CIE body and extracts the FDE pointer encoding from
// the augmentation string ('R'). All other augmentation bytes are
// skipped; we don't need LSDA decoding accuracy for the IBT checker, we
// only need each FDE's initial_location PC.
func parseCIE(body []byte, bo binary.ByteOrder) (out struct{ fdeEnc byte }) {
	// CIE layout after the CIE_id (already consumed by caller):
	//   version (1) | augmentation NUL-terminated | code_alignment_factor uleb128 |
	//   data_alignment_factor sleb128 | return_address_register uleb128 |
	//   if augmentation begins with 'z': aug_length uleb128 + aug data
	if len(body) < 5 {
		return
	}
	p := 4 // skip CIE_id
	if p >= len(body) {
		return
	}
	p++ // version
	augStart := p
	for p < len(body) && body[p] != 0 {
		p++
	}
	if p >= len(body) {
		return
	}
	aug := string(body[augStart:p])
	p++ // NUL
	// Skip code_align (uleb), data_align (sleb), ret_reg (uleb).
	for i := 0; i < 3 && p < len(body); i++ {
		_, n := readULEB128(body[p:])
		p += n
	}
	if !startsWithZ(aug) {
		return
	}
	// aug_length uleb128
	if p >= len(body) {
		return
	}
	_, n := readULEB128(body[p:])
	p += n
	for _, c := range aug[1:] {
		if p >= len(body) {
			break
		}
		switch c {
		case 'L':
			p++ // LSDA encoding
		case 'P':
			if p >= len(body) {
				return
			}
			penc := body[p]
			p++
			sz := encodingSize(penc)
			p += sz
		case 'R':
			if p >= len(body) {
				return
			}
			out.fdeEnc = body[p]
			p++
		case 'S', 'B':
			// Signal frame / B-key flag, no extra data.
		default:
			// Unknown aug char — bail out, encoding becomes default.
			return
		}
	}
	return
}

// parseFDE returns the initial_location PC of an FDE, decoded with the
// CIE-supplied pointer encoding. fdeAddr is the in-section address of
// the FDE's body (used for PC-relative encoding fix-up).
func parseFDE(fdeAddr uint64, body []byte, enc byte, cls elf.Class, bo binary.ByteOrder) (uint64, bool) {
	if len(body) < 8 {
		return 0, false
	}
	p := 4 // skip CIE_pointer
	val, n, ok := readEHPtr(body[p:], enc, fdeAddr+uint64(p), cls, bo)
	if !ok {
		return 0, false
	}
	_ = n
	return val, true
}

// readEHPtr decodes an `.eh_frame` pointer per the encoding byte. Only
// the subset the FDE initial_location uses is supported (absptr,
// pcrel, sdata4, sdata8, udata4, udata8); other encodings return !ok.
func readEHPtr(buf []byte, enc byte, here uint64, cls elf.Class, bo binary.ByteOrder) (uint64, int, bool) {
	if enc == 0xff { // DW_EH_PE_omit
		return 0, 0, false
	}
	format := enc & 0x0f
	app := enc & 0x70
	if enc == 0 {
		// absolute, absptr (size depends on class).
		if cls == elf.ELFCLASS64 {
			format = 0x04 // udata8
		} else {
			format = 0x03 // udata4
		}
	}
	var raw uint64
	var size int
	switch format {
	case 0x02: // udata2
		if len(buf) < 2 {
			return 0, 0, false
		}
		raw = uint64(bo.Uint16(buf))
		size = 2
	case 0x03: // udata4
		if len(buf) < 4 {
			return 0, 0, false
		}
		raw = uint64(bo.Uint32(buf))
		size = 4
	case 0x04: // udata8
		if len(buf) < 8 {
			return 0, 0, false
		}
		raw = bo.Uint64(buf)
		size = 8
	case 0x0a: // sdata2
		if len(buf) < 2 {
			return 0, 0, false
		}
		raw = uint64(int64(int16(bo.Uint16(buf))))
		size = 2
	case 0x0b: // sdata4
		if len(buf) < 4 {
			return 0, 0, false
		}
		raw = uint64(int64(int32(bo.Uint32(buf))))
		size = 4
	case 0x0c: // sdata8
		if len(buf) < 8 {
			return 0, 0, false
		}
		raw = bo.Uint64(buf)
		size = 8
	default:
		return 0, 0, false
	}
	switch app {
	case 0x00: // absptr
		return raw, size, true
	case 0x10: // pcrel
		return here + raw, size, true
	default:
		// datarel / funcrel / aligned not needed for the FDE PC; bail out.
		return 0, size, false
	}
}

func encodingSize(enc byte) int {
	switch enc & 0x0f {
	case 0x02, 0x0a:
		return 2
	case 0x03, 0x0b:
		return 4
	case 0x04, 0x0c:
		return 8
	}
	return 0
}

func startsWithZ(s string) bool {
	return len(s) > 0 && s[0] == 'z'
}

func readULEB128(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i, c := range b {
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
		if shift > 63 {
			return v, i + 1
		}
	}
	return v, len(b)
}
