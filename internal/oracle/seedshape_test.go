package oracle

import "testing"

func TestSeedShape_FixedBufferOnly(t *testing.T) {
	src := `void seed(int n) { char buf[64]; (void)buf; }`
	s := classifySeedShapeText(src)
	if !s.HasFixedBuffer {
		t.Error("expected HasFixedBuffer=true")
	}
	if s.HasVLA {
		t.Error("expected HasVLA=false")
	}
	if s.HasAlloca {
		t.Error("expected HasAlloca=false")
	}
	if s.HasDynamicAlloc() {
		t.Error("expected HasDynamicAlloc=false")
	}
	if s.IsMixed() {
		t.Error("expected IsMixed=false")
	}
}

func TestSeedShape_VLA(t *testing.T) {
	src := `void seed(int n) { char buf[n]; (void)buf; }`
	s := classifySeedShapeText(src)
	if !s.HasVLA {
		t.Error("expected HasVLA=true for char buf[n]")
	}
	if !s.HasDynamicAlloc() {
		t.Error("expected HasDynamicAlloc=true")
	}
}

func TestSeedShape_VLA_Expression(t *testing.T) {
	src := `void seed(int n) { int xs[n + 1]; (void)xs; }`
	s := classifySeedShapeText(src)
	if !s.HasVLA {
		t.Error("expected HasVLA=true for int xs[n+1]")
	}
}

func TestSeedShape_Alloca(t *testing.T) {
	src := `#include <alloca.h>
void seed(int n) { char *p = alloca(n); (void)p; }`
	s := classifySeedShapeText(src)
	if !s.HasAlloca {
		t.Error("expected HasAlloca=true")
	}
	if !s.HasDynamicAlloc() {
		t.Error("expected HasDynamicAlloc=true")
	}
}

func TestSeedShape_BuiltinAlloca(t *testing.T) {
	src := `void seed(int n) { void *p = __builtin_alloca(n); (void)p; }`
	s := classifySeedShapeText(src)
	if !s.HasAlloca {
		t.Error("expected HasAlloca=true for __builtin_alloca")
	}
}

func TestSeedShape_Mixed(t *testing.T) {
	src := `void seed(int n) {
		char fixed[16];
		char vla[n];
		(void)fixed; (void)vla;
	}`
	s := classifySeedShapeText(src)
	if !s.HasFixedBuffer {
		t.Error("expected HasFixedBuffer=true")
	}
	if !s.HasVLA {
		t.Error("expected HasVLA=true")
	}
	if !s.IsMixed() {
		t.Error("expected IsMixed=true")
	}
}

func TestSeedShape_Empty(t *testing.T) {
	s := classifySeedShape(nil)
	if s.HasFixedBuffer || s.HasVLA || s.HasAlloca {
		t.Error("nil seed should yield zero shape")
	}
	s = classifySeedShapeText("")
	if s.HasFixedBuffer || s.HasVLA || s.HasAlloca {
		t.Error("empty source should yield zero shape")
	}
}

func TestSeedShape_NoFalseVLAFromFixed(t *testing.T) {
	// `char buf[64]` must not look like a VLA — the size token is all digits.
	src := `void seed(void) { char buf[64]; (void)buf; }`
	s := classifySeedShapeText(src)
	if s.HasVLA {
		t.Error("char buf[64] should not register as VLA")
	}
}
