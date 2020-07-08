// Package 'eflag' is a wrapper around Go's standard flag, it provides enhancments for:
// Adding Header's and Footer's to Usage.
// Adding Aliases to flags. (ie.. -d, --debug)
// Enhances formatting for flag usage.
// Aside from that everything else is standard from the flag library.
package eflag

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// Duplicate flag's ErrorHandling.
type ErrorHandling int

const (
	ContinueOnError ErrorHandling = iota
	ExitOnError
	PanicOnError
	ReturnErrorOnly
)

var ErrHelp = flag.ErrHelp

// Write to nothing, to remove standard output of flag.
type _voidText struct{}

var voidText _voidText

func (s _voidText) Write(p []byte) (n int, err error) {
	return len(p), nil
}

type splitValue struct {
	example string
	value   *[]string
}

func (A *splitValue) String() string {
	if len(*A.value) > 0 {
		return strings.Join(*A.value, ",")
	} else {
		return fmt.Sprintf("\"%s\"", A.example)
	}
}

func (A *splitValue) Set(value string) error {
	A.example = ""
	*A.value = append(*A.value, strings.Split(value, ",")[0:]...)
	return nil
}

func (A *splitValue) Get() interface{} { return []string(*A.value) }

// Array variable, ie.. multiple --string=values
func (E *EFlagSet) Split(name string, example string, usage string) *[]string {
	output := new([]string)
	E.SplitVar(output, name, example, usage)
	return output
}

// Array variable, ie.. multiple --string=values
func (E *EFlagSet) SplitVar(p *[]string, name string, example string, usage string) {
	if strings.HasPrefix(example, "<") && strings.HasSuffix(example, ">") {
		example = example[1 : len(example)-1]
	}
	v := splitValue{
		example: example,
		value:   p,
	}
	E.Var(&v, name, usage)
}

// Array value, allows multiple --string=.
type arrayValue struct {
	example string
	value   *[]string
}

func (A *arrayValue) String() string {
	if len(*A.value) > 0 {
		return strings.Join(*A.value, ",")
	} else {
		return fmt.Sprintf("\"%s\"", A.example)
	}
}

func (A *arrayValue) Set(value string) error {
	A.example = ""
	*A.value = append(*A.value, value)
	return nil
}

func (A *arrayValue) Get() interface{} { return []string(*A.value) }

// Array variable, ie.. multiple --string=values
func (E *EFlagSet) Array(name string, example string, usage string) *[]string {
	output := new([]string)
	E.ArrayVar(output, name, example, usage)
	return output
}

// Array variable, ie.. multiple --string=values
func (E *EFlagSet) ArrayVar(p *[]string, name string, example string, usage string) {
	if strings.HasPrefix(example, "<") && strings.HasSuffix(example, ">") {
		example = example[1 : len(example)-1]
	}
	v := arrayValue{
		example: example,
		value:   p,
	}
	E.Var(&v, name, usage)
}

type EFlagSet struct {
	name          string
	Header        string
	Footer        string
	alias         map[string]string
	stringVars    map[string]bool
	out           io.Writer
	errorHandling ErrorHandling
	setFlags      []string
	order         []string
	AllowEmpty    bool
	*flag.FlagSet
}

var cmd = EFlagSet{
	os.Args[0],
	"",
	"",
	make(map[string]string),
	make(map[string]bool),
	os.Stderr,
	ExitOnError,
	make([]string, 0),
	make([]string, 0),
	false,
	flag.NewFlagSet(os.Args[0], flag.ContinueOnError),
}

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

func Header(input string) {
	cmd.Header = input
}

func Footer(input string) {
	cmd.Footer = input
}

func Parse() (err error) {
	if len(os.Args) > 1 {
		return cmd.Parse(os.Args[1:])
	} else {
		return cmd.Parse([]string{})
	}
}

func Usage() {
	if !cmd.Parsed() {
		cmd.Parse([]string{"--help"})
	}
}

func (s *EFlagSet) Order(name ...string) {
	if name != nil {
		s.order = name[0:]
	}
}

// Change where output will be directed.
func (s *EFlagSet) SetOutput(output io.Writer) {
	s.out = output
}

// Load a flag created with flag package.
func NewFlagSet(name string, errorHandling ErrorHandling) (output *EFlagSet) {
	output = &EFlagSet{
		name,
		"",
		"",
		make(map[string]string),
		make(map[string]bool),
		os.Stderr,
		errorHandling,
		make([]string, 0),
		make([]string, 0),
		false,
		flag.NewFlagSet(name, flag.ContinueOnError),
	}
	output.Usage = func() {
		output.Parse([]string{"--help"})
	}
	return output
}

// Reads through all flags available and outputs with better formatting.
func (s *EFlagSet) PrintDefaults() {

	output := tabwriter.NewWriter(s.out, 34, 8, 3, ' ', 0)

	flag_text := make(map[string]string)
	var flag_order []string
	var alias_order []string

	s.VisitAll(func(flag *flag.Flag) {
		if flag.Usage == "" {
			return
		}
		var text []string
		name := flag.Name
		alias := s.alias[flag.Name]
		is_string := s.stringVars[flag.Name]
		if alias != "" {
			if len(alias) > 1 {
				text = append(text, fmt.Sprintf("  --%s,", alias))
			} else {
				text = append(text, fmt.Sprintf("  -%s,", alias))
			}
		}
		space := " "
		if alias == "" {
			space = "  "
		}
		if len(name) > 1 {
			text = append(text, fmt.Sprintf("%s--%s", space, name))
		} else {
			text = append(text, fmt.Sprintf("%s-%s", space, name))
		}
		if is_string == true {
			if strings.HasPrefix(flag.DefValue, "<") && strings.HasSuffix(flag.DefValue, ">") {
				text = append(text, fmt.Sprintf("=%q", flag.DefValue[1:len(flag.DefValue)-1]))
			} else {
				text = append(text, fmt.Sprintf("=%q", flag.DefValue))
			}
		} else {
			if flag.DefValue != "true" && flag.DefValue != "false" {
				text = append(text, fmt.Sprintf("=%s", flag.DefValue))
			}
		}
		text = append(text, fmt.Sprintf("\t%s\n", flag.Usage))

		if alias == "" {
			flag_text[name] = strings.Join(text[0:], "")
			flag_order = append(flag_order, name)
		} else {
			flag_text[name] = strings.Join(text[0:], "")
			alias_order = append(alias_order, name)
		}
	})

	// Place Aliases first.
	flag_order = append(alias_order, flag_order[0:]...)

	//OutterLoop:
	for _, v := range flag_order {
		for _, o := range s.order {
			if o == v {
				for _, name := range s.order {
					txt, ok := flag_text[name]
					if ok {
						fmt.Fprintf(output, txt)
						delete(flag_text, name)
					}
				}
			}
		}
		if txt, ok := flag_text[v]; ok {
			fmt.Fprintf(output, txt)
		}
	}
	fmt.Fprintf(output, "  --help\tDisplays this usage information.\n")
	output.Flush()
}

// Adds an alias to an existing flag, requires a pointer to the variable, the current name and the new alias name.
func (s *EFlagSet) Alias(name string, alias string) {
	flag := s.Lookup(name)
	if flag == nil {
		return
	}
	s.Var(flag.Value, alias, "")
	s.alias[name] = alias
}

func (s *EFlagSet) IsSet(name string) bool {
	for _, k := range s.setFlags {
		if k == name {
			return true
		}
	}
	return false
}

// Wraps around the standard flag Parse, adds header and footer.
func (s *EFlagSet) Parse(args []string) (err error) {
	// set usage to empty to prevent unessisary work as we dump the output of flag.
	s.Usage = func() {}

	// Split bool flags so that '-abc' becomes '-a -b -c' before being parsed.
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		if strings.Contains(a, "=") {
			continue
		}
		a = strings.TrimPrefix(a, "-")
		if len(a) == 0 {
			continue
		}
		args[i] = fmt.Sprintf("-%c", a[0])
		for _, ch := range a[1:] {
			args = append(args[0:], "")
			copy(args[1:], args[0:])
			args[0] = fmt.Sprintf("-%c", ch)
		}
	}

	// Remove normal error message printing.
	s.FlagSet.SetOutput(voidText)

	// Harvest error message, conceal flag.Parse() output, then reconstruct error message.
	stdOut := s.out
	s.out = voidText

	err = s.FlagSet.Parse(args)
	s.out = stdOut

	// Remove example text from strings, ie.. <server to connect with>
	clear_examples := func(f *flag.Flag) {
		val := f.Value.String()
		if strings.HasPrefix(val, "<") && strings.HasSuffix(val, ">") {
			f.Value.Set("")
		}
	}

	s.FlagSet.VisitAll(clear_examples)

	mark_set_flags := func(f *flag.Flag) {
		s.setFlags = append(s.setFlags, f.Name)
	}

	s.FlagSet.Visit(mark_set_flags)

	// Implement new Usage function.
	s.Usage = func() {
		if s.Header != "" {
			fmt.Fprintf(s.out, "%s\n", s.Header)
		}
		if s.name == "" {
			fmt.Fprintf(s.out, "Available options:\n")
		} else {
			if s.name == os.Args[0] {
				fmt.Fprintf(s.out, "Available '%s' options:\n", os.Args[0])
			} else {
				fmt.Fprintf(s.out, "Available '%s' options:\n", s.name)
			}
		}
		s.PrintDefaults()
		if s.Footer != "" {
			fmt.Fprintf(s.out, "%s\n", s.Footer)
		}
	}

	// Implement a new error message.
	if err != nil {
		if err != flag.ErrHelp {
			errStr := err.Error()
			cmd := strings.Split(errStr, "-")
			if len(cmd) > 1 {
				for _, arg := range args {
					if strings.Contains(arg, cmd[1]) {
						err = fmt.Errorf("%s%s", cmd[0], arg)
						if s.errorHandling != ReturnErrorOnly {
							fmt.Fprintf(s.out, "%s\n\n", errStr)
						}
						break
					}
				}
			} else {
				if s.errorHandling != ReturnErrorOnly {
					fmt.Fprintf(s.out, "%s\n\n", errStr)
				}
			}
		}

		// Errorflag handling.
		switch s.errorHandling {
		case ReturnErrorOnly:
		case ContinueOnError:
			s.Usage()
		case ExitOnError:
			s.Usage()
			os.Exit(2)
		case PanicOnError:
			panic(err)
		}
	}
	return
}

func (s *EFlagSet) String(name string, value string, usage string) *string {
	s.stringVars[name] = true
	return s.FlagSet.String(name, value, usage)
}

func (s *EFlagSet) StringVar(p *string, name string, value string, usage string) {
	s.stringVars[name] = true
	s.FlagSet.StringVar(p, name, value, usage)
}
