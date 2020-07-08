# iotimeout
--
    import "github.com/cmcoffee/go-snuglib/iotimeout"


## Usage

```go
var ErrTimeout = errors.New("Timeout reached while waiting for bytes.")
```

#### func  NewReadCloser

```go
func NewReadCloser(source io.ReadCloser, timeout time.Duration) io.ReadCloser
```
Timeout ReadCloser: Adds a timer to io.ReadCloser

#### func  NewReader

```go
func NewReader(source io.Reader, timeout time.Duration) io.Reader
```
Timeout Reader: Adds a time to io.Reader
