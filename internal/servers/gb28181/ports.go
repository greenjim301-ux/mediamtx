package gb28181

import (
	"fmt"
	"sync"
)

// portAllocator allocates the ports used to receive media streams.
type portAllocator struct {
	min int
	max int

	mutex sync.Mutex
	used  map[int]struct{}
	next  int
}

func (a *portAllocator) initialize() {
	a.used = make(map[int]struct{})
	a.next = a.min
}

func (a *portAllocator) allocate() (int, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	count := a.max - a.min + 1

	for range count {
		port := a.next

		a.next++
		if a.next > a.max {
			a.next = a.min
		}

		if _, ok := a.used[port]; !ok {
			a.used[port] = struct{}{}
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available media ports in range %d-%d", a.min, a.max)
}

func (a *portAllocator) release(port int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.used, port)
}
