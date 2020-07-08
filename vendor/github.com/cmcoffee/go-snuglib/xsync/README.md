# xsync
--
    import "github.com/cmcoffee/go-snuglib/xsync"

LimitGroup is a sync.WaitGroup combined with a limiter, to limit how many
threads are created.

## Usage

#### type BitFlag

```go
type BitFlag uint32
```

Atomic BitFlag

#### func (*BitFlag) Has

```go
func (B *BitFlag) Has(flag uint32) bool
```

#### func (*BitFlag) Set

```go
func (B *BitFlag) Set(flag uint32)
```
Set BitFlag

#### func (*BitFlag) Unset

```go
func (B *BitFlag) Unset(flag uint32)
```
Unset BitFlag

#### type BitFlag64

```go
type BitFlag64 uint64
```

Atomic BitFlag

#### func (*BitFlag64) Has

```go
func (B *BitFlag64) Has(flag uint64) bool
```

#### func (*BitFlag64) Set

```go
func (B *BitFlag64) Set(flag uint64)
```
Set BitFlag

#### func (*BitFlag64) Unset

```go
func (B *BitFlag64) Unset(flag uint64)
```
Unset BitFlag

#### type LimitGroup

```go
type LimitGroup interface {
	Add(n int)
	Try() bool
	Done()
	Wait()
}
```


#### func  NewLimitGroup

```go
func NewLimitGroup(max int) LimitGroup
```
