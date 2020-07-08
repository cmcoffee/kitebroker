# eflag
--
    import "github.com/cmcoffee/go-snuglib/eflag"

Package 'eflag' is a wrapper around Go's standard flag, it provides enhancments
for: Adding Header's and Footer's to Usage. Adding Aliases to flags. (ie.. -d,
--debug) Enhances formatting for flag usage. Aside from that everything else is
standard from the flag library.

## Usage

```go
var (
	ArrayVar      = cmd.ArrayVar
	SetOutput     = cmd.SetOutput
	PrintDefaults = cmd.PrintDefaults
	Alias         = cmd.Alias
	String        = cmd.String
	StringVar     = cmd.StringVar
	Arg           = cmd.Arg
	Args          = cmd.Args
	Bool          = cmd.Bool
	BoolVar       = cmd.BoolVar
	Duration      = cmd.Duration
	DurationVar   = cmd.DurationVar
	Float64       = cmd.Float64
	Float64Var    = cmd.Float64Var
	Int           = cmd.Int
	IntVar        = cmd.IntVar
	Int64         = cmd.Int64
	Int64Var      = cmd.Int64Var
	Lookup        = cmd.Lookup
	NArg          = cmd.NArg
	NFlag         = cmd.NFlag
	Name          = cmd.Name
	Output        = cmd.Output
	Parsed        = cmd.Parsed
	Uint          = cmd.Uint
	UintVar       = cmd.UintVar
	Uint64        = cmd.Uint64
	Uint64Var     = cmd.Uint64Var
	Var           = cmd.Var
	Visit         = cmd.Visit
	VisitAll      = cmd.VisitAll
)
```

```go
var ErrHelp = flag.ErrHelp
```

#### func  Footer

```go
func Footer(input string)
```

#### func  Header

```go
func Header(input string)
```

#### func  Parse

```go
func Parse() (err error)
```

#### func  Usage

```go
func Usage()
```

#### type EFlagSet

```go
type EFlagSet struct {
	Header string
	Footer string

	AllowEmpty bool
	*flag.FlagSet
}
```


#### func  NewFlagSet

```go
func NewFlagSet(name string, errorHandling ErrorHandling) (output *EFlagSet)
```
Load a flag created with flag package.

#### func (*EFlagSet) Alias

```go
func (s *EFlagSet) Alias(name string, alias string)
```
Adds an alias to an existing flag, requires a pointer to the variable, the
current name and the new alias name.

#### func (*EFlagSet) Array

```go
func (E *EFlagSet) Array(name string, example string, usage string) *[]string
```
Array variable, ie.. multiple --string=values

#### func (*EFlagSet) ArrayVar

```go
func (E *EFlagSet) ArrayVar(p *[]string, name string, example string, usage string)
```
Array variable, ie.. multiple --string=values

#### func (*EFlagSet) IsSet

```go
func (s *EFlagSet) IsSet(name string) bool
```

#### func (*EFlagSet) Order

```go
func (s *EFlagSet) Order(name ...string)
```

#### func (*EFlagSet) Parse

```go
func (s *EFlagSet) Parse(args []string) (err error)
```
Wraps around the standard flag Parse, adds header and footer.

#### func (*EFlagSet) PrintDefaults

```go
func (s *EFlagSet) PrintDefaults()
```
Reads through all flags available and outputs with better formatting.

#### func (*EFlagSet) SetOutput

```go
func (s *EFlagSet) SetOutput(output io.Writer)
```
Change where output will be directed.

#### func (*EFlagSet) Split

```go
func (E *EFlagSet) Split(name string, example string, usage string) *[]string
```
Array variable, ie.. multiple --string=values

#### func (*EFlagSet) SplitVar

```go
func (E *EFlagSet) SplitVar(p *[]string, name string, example string, usage string)
```
Array variable, ie.. multiple --string=values

#### func (*EFlagSet) String

```go
func (s *EFlagSet) String(name string, value string, usage string) *string
```

#### func (*EFlagSet) StringVar

```go
func (s *EFlagSet) StringVar(p *string, name string, value string, usage string)
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
	ReturnErrorOnly
)
```
