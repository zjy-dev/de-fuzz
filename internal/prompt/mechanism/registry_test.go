package mechanism_test

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/prompt/mechanism"
)

func TestGet_Registered(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("expected canary to be registered")
	}
	if c == nil {
		t.Fatal("Get returned nil contract for registered key")
	}
}

func TestGet_Missing(t *testing.T) {
	_, ok := mechanism.Get("no_such_mechanism")
	if ok {
		t.Error("Get should return false for unregistered key")
	}
}

func TestMustGet_KnownKey(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustGet panicked unexpectedly: %v", r)
		}
	}()
	c := mechanism.MustGet("canary")
	if c == nil {
		t.Error("MustGet returned nil")
	}
}

func TestMustGet_UnknownKey_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet should panic for unknown key")
		}
	}()
	mechanism.MustGet("no_such_mechanism")
}
