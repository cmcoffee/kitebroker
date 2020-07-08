package xsync

import "sync/atomic"

// Atomic BitFlag
type BitFlag uint32

func (B *BitFlag) Has(flag uint32) bool {
	if atomic.LoadUint32((*uint32)(B))&uint32(flag) != 0 {
		return true
	}
	return false
}

// Set BitFlag
func (B *BitFlag) Set(flag uint32) {
	atomic.StoreUint32((*uint32)(B), atomic.LoadUint32((*uint32)(B))|uint32(flag))
}

// Unset BitFlag
func (B *BitFlag) Unset(flag uint32) {
	atomic.StoreUint32((*uint32)(B), atomic.LoadUint32((*uint32)(B))&^uint32(flag))
}

// Atomic BitFlag
type BitFlag64 uint64

func (B *BitFlag64) Has(flag uint64) bool {
	if atomic.LoadUint64((*uint64)(B))&uint64(flag) != 0 {
		return true
	}
	return false
}

// Set BitFlag
func (B *BitFlag64) Set(flag uint64) {
	atomic.StoreUint64((*uint64)(B), atomic.LoadUint64((*uint64)(B))|uint64(flag))
}

// Unset BitFlag
func (B *BitFlag64) Unset(flag uint64) {
	atomic.StoreUint64((*uint64)(B), atomic.LoadUint64((*uint64)(B))&^uint64(flag))
}
