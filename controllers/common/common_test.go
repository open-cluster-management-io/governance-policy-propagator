package common

import "testing"

func TestParseRootPolicyLabel(t *testing.T) {
	tests := map[string]struct {
		name      string
		namespace string
		shouldErr bool
	}{
		"foobar":   {"", "", true},
		"foo.bar":  {"bar", "foo", false},
		"fo.ob.ar": {"", "", true},
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			name, namespace, err := ParseRootPolicyLabel(input)
			if (err != nil) != expected.shouldErr {
				t.Fatal("expected error, got nil")
			}
			if name != expected.name {
				t.Fatalf("expected name '%v', got '%v'", expected.name, name)
			}
			if namespace != expected.namespace {
				t.Fatalf("expected namespace '%v', got '%v'", expected.namespace, namespace)
			}
		})
	}
}
