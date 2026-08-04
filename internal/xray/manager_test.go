package xray

import "testing"

// TestManagerDoesNotOverwriteANewerResult pins why StoreResult takes the process
// it belongs to. A dying process reports asynchronously, so its result can land
// after a replacement is already installed and would be read as that one's.
func TestManagerDoesNotOverwriteANewerResult(t *testing.T) {
	m := &Manager{}
	first := NewProcess(&Config{})
	second := NewProcess(&Config{})

	m.Replace(first)
	m.StoreResult(first, "first result")
	process, result := m.Snapshot()
	if process != first || result != "first result" {
		t.Fatalf("snapshot = (%p, %q), want (%p, %q)", process, result, first, "first result")
	}

	m.Replace(second)
	m.StoreResult(first, "old result")
	process, result = m.Snapshot()
	if process != second {
		t.Fatal("snapshot returned the replaced process")
	}
	if result != "" {
		t.Fatalf("snapshot result = %q, want empty", result)
	}
}
