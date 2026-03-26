package seed

import "testing"

func TestAnalyzeCanarySource(t *testing.T) {
	src := `
__attribute__((no_stack_protector))
void seed(int buf_size, int fill_size) {
    char vla[buf_size];
    char *p = alloca(buf_size);
    (void)p;
}
`

	analysis := AnalyzeCanarySource(src)
	if !analysis.SeedDisablesStackProtector {
		t.Fatal("expected seed no_stack_protector to be detected")
	}
	if analysis.SeedRequestsStackProtect {
		t.Fatal("did not expect stack_protect request")
	}
	if !analysis.UsesVLA {
		t.Fatal("expected VLA usage to be detected")
	}
	if !analysis.UsesAlloca {
		t.Fatal("expected alloca usage to be detected")
	}
}

func TestAnalyzeCanarySource_StackProtect(t *testing.T) {
	src := `
__attribute__((stack_protect))
void seed(int buf_size, int fill_size) {
    char fixed[32];
    (void)buf_size;
    (void)fill_size;
}
`

	analysis := AnalyzeCanarySource(src)
	if analysis.SeedDisablesStackProtector {
		t.Fatal("did not expect source disable")
	}
	if !analysis.SeedRequestsStackProtect {
		t.Fatal("expected stack_protect request to be detected")
	}
	if analysis.UsesVLA || analysis.UsesAlloca {
		t.Fatal("did not expect VLA/alloca usage")
	}
}
