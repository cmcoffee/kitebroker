/*
	LimitGroup is a sync.WaitGroup combined with a limiter, to limit how many threads are created.
*/
package xsync

import "sync"

type limitGroup struct {
	wg      sync.WaitGroup
	limiter chan struct{}
}

type LimitGroup interface {
	Add(n int)
	Try() bool
	Done()
	Wait()
}

func NewLimitGroup(max int) LimitGroup {
	x := new(limitGroup)
	x.limiter = make(chan struct{}, max)
	return x
}

// Add adds on to sync.WaitGroup, expanding to have a limiter on the counter.
// If delta is larger than the limiter, Add panics.
func (L *limitGroup) Add(n int) {
	if n > cap(L.limiter) {
		panic("Attempted to add more goroutines than max LimitGroup allowed!")
	}
	L.wg.Add(n)
	if L.limiter == nil {
		return
	}
	if n > 0 {
		for i := 0; i < n; i++ {
			L.limiter <- struct{}{}
		}
	} else {
		for i := n; i < 0; i++ {
			<-L.limiter
		}
	}

}

// Attempts to get a waitgroup thread, if true one is available and taken, if not, returns false.
func (L *limitGroup) Try() bool {
	select {
	case L.limiter <- struct{}{}:
		L.wg.Add(1)
		return true
	default:
		return false
	}
}

// Done decrements the LimitGroup counter by one.
func (L *limitGroup) Done() {
	if L.limiter != nil {
		<-L.limiter
	}
	L.wg.Done()
}

// Wait blocks until the LimitGroup is zero.
func (L *limitGroup) Wait() {
	L.wg.Wait()
}
