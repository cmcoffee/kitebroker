# eflag
--
    import "github.com/cmcoffee/go-eflag"

Package 'eflag' is a wrapper around Go's standard flag, it provides
enhancments for: Adding Header's and Footer's to Usage. Adding Aliases to flags.
(ie.. -d, --debug) Enhances formatting for flag usage. Aside from that
everything else is standard from the flag library.

## Usage

#### type EFlagSet

```go
type EFlagSet struct {
	Header string
	Footer string

	*flag.FlagSet
}
```


#### func  CommandLine

```go
func CommandLine() *EFlagSet
```
Allows for quick command line argument parsing, flag's default usage.

#### func  NewFlagSet

```go
func NewFlagSet(name string, errorHandling ErrorHandling) *EFlagSet
```
Load a flag created with flag package.

#### func (*EFlagSet) Alias

```go
func (self *EFlagSet) Alias(val interface{}, name string, alias string)
```
Adds an alias to an existing flag, requires a pointer to the variable, the
current name and the new alias name.

#### func (*EFlagSet) Parse

```go
func (self *EFlagSet) Parse(args ...string) (err error)
```
Wraps around the standard flag Parse, adds header and footer.

#### func (*EFlagSet) PrintDefaults

```go
func (self *EFlagSet) PrintDefaults()
```
Reads through all flags available and outputs with better formatting.

#### func (*EFlagSet) SetOutput

```go
func (self *EFlagSet) SetOutput(output io.Writer)
```
Change where output will be directed.

#### func (*EFlagSet) String

```go
func (self *EFlagSet) String(name string, value string, usage string) *string
```

#### func (*EFlagSet) StringVar

```go
func (self *EFlagSet) StringVar(p *string, name string, value string, usage string)
```

#### type ErrorHandling

```go
type ErrorHandling int
```

Duplicate flag's ErrorHandling.

```go
const (
	ContinueOnError ErrorHandling = iota
	ExitOnError
	PanicOnError
)
```
