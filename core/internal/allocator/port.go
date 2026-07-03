package allocator

import (
	"errors"
	"math/bits"
	"sync/atomic"
)

var ErrNoPortsAvailable = errors.New("no ports available")

type PortAllocator struct {
	rangeStart int
	rangeEnd   int
	bits       []uint64
}

func NewPortAllocator(start, end int) *PortAllocator {
	if start > end {
		panic("invalid port range: start > end")
	}
	count := end - start + 1
	numUint64 := (count + 63) / 64
	bits := make([]uint64, numUint64)

	if count%64 != 0 {
		lastIdx := numUint64 - 1
		remainingBits := count % 64
		var mask uint64 = ^((1 << remainingBits) - 1)
		bits[lastIdx] = mask
	}

	return &PortAllocator{
		rangeStart: start,
		rangeEnd:   end,
		bits:       bits,
	}
}

func (a *PortAllocator) Acquire() (int, error) {
	for i := 0; i < len(a.bits); i++ {
		for {
			val := atomic.LoadUint64(&a.bits[i])
			if val == ^uint64(0) {
				break
			}

			bitIdx := -1
			for j := 0; j < 64; j++ {
				if (val & (1 << j)) == 0 {
					bitIdx = j
					break
				}
			}

			if bitIdx == -1 {
				break
			}

			newVal := val | (1 << bitIdx)
			if atomic.CompareAndSwapUint64(&a.bits[i], val, newVal) {
				port := a.rangeStart + i*64 + bitIdx
				return port, nil
			}
		}
	}
	return 0, ErrNoPortsAvailable
}

func (a *PortAllocator) Release(port int) {
	if port < a.rangeStart || port > a.rangeEnd {
		return
	}

	offset := port - a.rangeStart
	idx := offset / 64
	bitIdx := offset % 64
	mask := uint64(1) << bitIdx

	for {
		val := atomic.LoadUint64(&a.bits[idx])
		if (val & mask) == 0 {
			return
		}
		newVal := val & ^mask
		if atomic.CompareAndSwapUint64(&a.bits[idx], val, newVal) {
			return
		}
	}
}

func (a *PortAllocator) FreePorts() int {
	count := 0
	for i := 0; i < len(a.bits); i++ {
		val := atomic.LoadUint64(&a.bits[i])
		count += bits.OnesCount64(val)
	}

	// Calculate the number of masked padding bits
	paddingBits := 0
	countRange := a.rangeEnd - a.rangeStart + 1
	if countRange%64 != 0 {
		paddingBits = 64 - (countRange % 64)
	}

	total := countRange
	return total - (count - paddingBits)
}
