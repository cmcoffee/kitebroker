# wrotate
--
    import "github.com/cmcoffee/go-snuglib/wrotate"


## Usage

#### func  OpenFile

```go
func OpenFile(name string, max_bytes int64, max_rotations uint) (io.WriteCloser, error)
```
Creates a new log file (or opens an existing one) for writing. max_bytes is
threshold for rotation, max_rotation is number of previous logs to hold on to.
