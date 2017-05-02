// Package 'eflag' is a wrapper around Go's standard flag, it provides enhancments for:
// Adding Header's and Footer's to Usage.
// Adding Aliases to flags. (ie.. -d, --debug)
// Enhances formatting for flag usage.
// Aside from that everything else is standard from the flag library.
package eflag

import (
	"flag"
	"fmt"
	"os"
	"io"
	"time"
	"text/tabwriter"
	"strings"
)

// Duplicate flag's ErrorHandling.
type ErrorHandling int

const (
   		ContinueOnError ErrorHandling = iota
		ExitOnError
		PanicOnError
)

// Write to nothing, to remove standard output of flag.
type _voidText struct{}
var voidText _voidText 
func (s _voidText) Write(p []byte) (n int, err error) {
		return len(p), nil
}

type EFlagSet struct {
	name string
	Header string
	Footer string
	alias map[string]string
	stringVars map[string]bool
	*flag.FlagSet
	out io.Writer
	errorHandling ErrorHandling
}

// Allows for quick command line argument parsing, flag's default usage.
func CommandLine() *EFlagSet {
	return NewFlagSet(os.Args[0], ExitOnError)
}

// Change where output will be directed.
func (s *EFlagSet) SetOutput(output io.Writer) {
	s.out = output
}

// Load a flag created with flag package.
func NewFlagSet(name string, errorHandling ErrorHandling) *EFlagSet {
	return &EFlagSet{
		name,
		"",
		"",
		make(map[string]string),
		make(map[string]bool),
		flag.NewFlagSet(name, flag.ContinueOnError),
		os.Stderr,
		errorHandling,
	}
}

// Reads through all flags available and outputs with better formatting.
func (s *EFlagSet) PrintDefaults() {
	
	output := tabwriter.NewWriter(s.out, 34, 8, 1, ' ', 0)
	
	s.VisitAll(func(flag *flag.Flag) {
		if flag.Usage == "" { return }
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
			text = append(text, fmt.Sprintf("=%q", flag.DefValue))
		} else {
			if flag.DefValue != "true" && flag.DefValue != "false" {
				text = append(text, fmt.Sprintf("=%s", flag.DefValue))
			}
		}
		text = append(text, fmt.Sprintf("\t%s\n", flag.Usage))
		
		fmt.Fprintf(output, strings.Join(text[0:], ""))
	})
		fmt.Fprintf(output, "  --help\tDisplays usage information.\n")
		output.Flush()
}

// Adds an alias to an existing flag, requires a pointer to the variable, the current name and the new alias name.
func (s *EFlagSet) Alias(val interface{}, name string, alias string) {
	flag := s.Lookup(name)
	if flag == nil { return }
	switch v := val.(type) {
		case *bool:
			s.BoolVar(v, alias, *v, "")
		case *time.Duration:
			s.DurationVar(v, alias, *v, "")
		case *float64:
			s.Float64Var(v, alias, *v, "")
		case *int:
			s.IntVar(v, alias, *v, "")
		case *int64:
			s.Int64Var(v, alias, *v, "")
		case *string:
			s.StringVar(v, alias, *v, "")
		case *uint:
			s.UintVar(v, alias, *v, "")
		case *uint64:
			s.Uint64Var(v, alias, *v, "")
		default:
			s.Var(flag.Value, alias, "")
			
	}
	s.alias[name] = alias
}

// Wraps around the standard flag Parse, adds header and footer.
func (s *EFlagSet) Parse(args []string) (err error) {
	// set usage to empty to prevent unessisary work as we dump the output of flag.
	s.Usage = func() {}

	// Split bool flags so that '-abc' becomes '-a -b -c' before being parsed.
	for i, a := range args {
		if !strings.Contains(a, "-") { continue }
		if strings.Contains(a, "=") { continue }
		if strings.Contains(a, "--") { continue }
		a = strings.TrimPrefix(a, "-")
		if len(a) == 0 { continue }
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

	// Implement new Usage function.
	s.Usage = func() {
		if s.Header != "" {
			fmt.Fprintf(s.out, "%s\n\n", s.Header)
		} else {
			if s.name == "" {
				fmt.Fprintf(s.out, "Available modifiers:\n")
			} else {
				if s.name == os.Args[0] {
					fmt.Fprintf(s.out, "Available %s modifiers:\n", os.Args[0])
				} else {
					fmt.Fprintf(s.out, "Available %s modifiers:\n", s.name)
				}
			}
		}
		s.PrintDefaults()
		if s.Footer != "" { fmt.Fprintf(s.out, "%s\n", s.Footer) }
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
						fmt.Fprintf(s.out, "%s\n\n", err.Error())
						break
					}
				}
			} else {
				fmt.Fprintf(s.out, "%s\n\n", errStr)
			}
		}
		
		// Errorflag handling.
		switch s.errorHandling {
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
