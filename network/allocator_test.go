package network

import (
	"testing"
)

func TestPortAllocator_FreePorts(t *testing.T) {
	// Range 25565-25570 (size 6)
	// count=6, numUint64=1
	// remainingBits=6, mask = ^((1 << 6) - 1) = ^63 = 11...11000000
	// This masks out all bits from 6 onwards.
	a := NewPortAllocator(25565, 25570)
	
	// Expect 6 free ports
	free := a.FreePorts()
	if free != 6 {
		t.Fatalf("expected 6 free ports, got %d", free)
	}

	_, _ = a.Acquire() // Acquire 1 port
	
	free = a.FreePorts()
	if free != 5 {
		t.Fatalf("expected 5 free ports, got %d", free)
	}
}
