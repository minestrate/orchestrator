package allocator

import "testing"

func TestPortAllocator_FreePorts(t *testing.T) {
	a := NewPortAllocator(25565, 25570)

	free := a.FreePorts()
	if free != 6 {
		t.Fatalf("expected 6 free ports, got %d", free)
	}

	p, err := a.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != 25565 {
		t.Fatalf("expected port 25565, got %d", p)
	}

	free = a.FreePorts()
	if free != 5 {
		t.Fatalf("expected 5 free ports, got %d", free)
	}

	a.Release(p)
	free = a.FreePorts()
	if free != 6 {
		t.Fatalf("expected 6 free ports after release, got %d", free)
	}
}
