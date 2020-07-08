package options

import (
	"bytes"
	"fmt"
	. "github.com/cmcoffee/go-snuglib/nfo"
	"github.com/cmcoffee/go-snuglib/xsync"
	"strconv"
	"strings"
	"text/tabwriter"
)

type Options struct {
	header    string
	footer    string
	exit_char rune
	flags     xsync.BitFlag
	config    []Value
}

// Options Value
type Value interface {
	Set() bool
	Get() interface{}
	String() string
}

// Creates new Options Menu
func NewOptions(header, footer string, exit_char rune) *Options {
	return &Options{
		header:    header,
		footer:    footer,
		exit_char: exit_char,
		flags:     0,
		config:    make([]Value, 0),
	}
}

// Registers an Value with Options Menu
func (T *Options) Register(input Value) {
	T.config = append(T.config, input)
}

// Show Options Menu, if seperate_last = true, the last menu item will be dropped one line, and it's select number will be 0, seperating it from the rest.
func (T *Options) Select(seperate_last bool) (changed bool) {
	var text_buffer bytes.Buffer
	txt := tabwriter.NewWriter(&text_buffer, 1, 8, 1, ' ', 0)

	show_banner := func() {
		if len(T.header) > 0 {
			text_buffer.Reset()
			fmt.Fprintf(txt, T.header)
			fmt.Fprintf(txt, "\n\n")
			txt.Flush()

			Stdout(text_buffer.String())
		}
	}

	show_banner()

	for {
		text_buffer.Reset()
		config_map := make(map[int]Value)
		config_len := len(T.config) - 1

		for i := 0; i <= config_len; i++ {
			if i == config_len && config_len > 0 && seperate_last {
				config_map[0] = T.config[config_len]
				fmt.Fprintf(txt, "\t\n")
				fmt.Fprintf(txt, " [0] %s\n", T.config[config_len].String())
				break
			}
			config_map[i+1] = T.config[i]
			fmt.Fprintf(txt, " [%d] %s\n", i+1, T.config[i].String())
		}

		fmt.Fprintf(txt, "\n%s: ", T.footer)
		txt.Flush()

		input := GetInput(text_buffer.String())
		if strings.ToLower(input) == strings.ToLower(string(T.exit_char)) {
			return
		} else {
			sel, err := strconv.Atoi(input)
			if err != nil {
				Stdout("\n[ERROR] Unrecognized Selection: '%s'\n\n", input)
				continue
			} else {
				if v, ok := config_map[sel]; ok {
					changed = v.Set()
					switch v.(type) {
					case *funcValue:
						Stdout("\n")
						show_banner()
					case *optionsValue:
						Stdout("\n")
						show_banner()
					default:
						Stdout("\n")
					}
					continue
				}
				Stdout("\n[ERROR] Unrecognized Selection: '%s'\n\n", input)
			}
		}
	}
}

// Presents string, uses astricks if private.
func showVar(input string, mask bool) string {
	hide_value := func(input string) string {
		var str []rune
		for _ = range input {
			str = append(str, '*')
		}
		return string(str)
	}

	if len(input) == 0 {
		return "*** UNCONFIGURED ***"
	} else {
		if !mask {
			return input
		} else {
			return hide_value(input)
		}
	}
}

// String defines an string menu option displaying with specified desc in menu, default value, and help string. The return value is the address of an string variable that stores the value of the option.
func (O *Options) String(desc string, value string, help string, mask_value bool) *string {
	new_var := &stringValue{
		desc:  desc,
		value: &value,
		help:  help,
		mask:  mask_value,
	}
	O.Register(new_var)
	return &value
}

// StringVar defines a string flag with specified name, default value, and usage string. The argument p points to a string variable in which to store the value of the flag.
func (O *Options) StringVar(p *string, desc string, value string, help string, mask_value bool) {
	*p = value
	O.Register(&stringValue{
		desc:  desc,
		value: p,
		help:  help,
		mask:  mask_value,
	})
	return
}

// Bool defines an int menu option displaying with specified desc in menu, default value, and help string. The return value is the address of an bool variable that stores the value of the option.
func (O *Options) Bool(desc string, value bool) *bool {
	new_var := &boolValue{
		desc:  desc,
		value: &value,
	}
	O.Register(new_var)
	return &value
}

// BoolVar defines a bool menu option displaying with specified desc in menu, default value, and help string. The argument p points to a bool variable in which to store the value of the option.
func (O *Options) BoolVar(p *bool, desc string, value bool) {
	*p = value
	O.Register(&boolValue{
		desc:  desc,
		value: p,
	})
}

// Int defines an int menu option displaying with specified desc in menu, default value, and help string. The return value is the address of an int variable that stores the value of the option.
func (O *Options) Int(desc string, value int, help string, min, max int) *int {
	new_var := &intValue{
		desc:  desc,
		value: &value,
		help:  help,
		min:   min,
		max:   max,
	}
	O.Register(new_var)
	return &value
}

func (O *Options) IntVar(p *int, desc string, value int, help string, min, max int) {
	*p = value
	O.Register(&intValue{
		desc:  desc,
		value: p,
		min:   min,
		max:   max,
		help:  help,
	})
}

// Option defines an nested Options menu option displaying with specified desc in menu, seperate_last will seperate the last menu option within the sub Options when selected.
func (O *Options) Options(desc string, value *Options, seperate_last bool) {
	O.Register(&optionsValue{
		desc:          desc,
		value:         value,
		seperate_last: seperate_last,
	})
}

// Func defined a function within the option menu, the function should return a bool variable telling the Options menu if a change has occured.
func (O *Options) Func(desc string, value func() bool) {
	O.Register(&funcValue{
		desc:  desc,
		value: value,
	})
}

// String value
type stringValue struct {
	desc  string
	value *string
	help  string
	mask  bool
}

func (S *stringValue) Set() bool {
	var input string
	if len(S.help) > 0 {
		input = GetInput(fmt.Sprintf("\n# %s\n--> %s: ", S.help, S.desc))
	} else {
		input = GetInput(fmt.Sprintf("\n--> %s: ", S.desc))
	}
	if len(input) > 0 {
		*S.value = input
		return true
	}
	return false
}

func (S *stringValue) String() string {
	return fmt.Sprintf("%s: \t%s", S.desc, showVar(*S.value, S.mask))
}

func (S *stringValue) Get() interface{} {
	return S.value
}

// Boolean Value.
type boolValue struct {
	desc  string
	value *bool
}

func (B *boolValue) IsSet() bool {
	return true
}

func (B *boolValue) Set() bool {
	*B.value = *B.value == false
	return true
}

func (B *boolValue) Get() interface{} {
	return B.value
}

func (B *boolValue) String() string {
	var value_str string
	if *B.value {
		value_str = "yes"
	} else {
		value_str = "no"
	}
	return fmt.Sprintf("%s:\t%s", B.desc, value_str)
}

type intValue struct {
	desc    string
	help    string
	value   *int
	min     int
	max     int
	changed int
}

// Integer Value
func (I *intValue) Set() bool {
	for {
		var input string
		if len(I.help) > 0 {
			input = GetInput(fmt.Sprintf("\n# %s\n--> %s (%d-%d): ", I.help, I.desc, I.min, I.max))
		} else {
			input = GetInput(fmt.Sprintf("--> %s (%d-%d): ", I.desc, I.min, I.max))
		}
		if len(input) > 0 {
			val, err := strconv.Atoi(input)
			if err != nil {
				Stdout("\n[ERROR] Value must be an integer between %d and %d.", I.min, I.max)
				continue
			}
			if val > I.max || val < I.min {
				Stdout("\n[ERROR] Value is outside of acceptable range of %d and %d.", I.min, I.max)
				continue
			}
			*I.value = val
			return true
		}
		return false
	}
}

func (I *intValue) Get() interface{} {
	return I.value
}

func (I intValue) IsSet() bool {
	return true
}

func (I *intValue) String() string {
	return fmt.Sprintf("%s:\t%d", I.desc, *I.value)
}

// Nested Options.
type optionsValue struct {
	desc          string
	seperate_last bool
	value         *Options
}

func (O *optionsValue) Set() bool {
	Stdout("\n")
	return O.value.Select(O.seperate_last)
}

func (O *optionsValue) Get() interface{} {
	return nil
}

func (O *optionsValue) String() string {
	return O.desc
}

// Function as an option, should take no arguments and return a bool value.
type funcValue struct {
	desc  string
	value func() bool
}

func (f *funcValue) String() string {
	return f.desc
}

func (f *funcValue) Get() interface{} {
	return nil
}

func (f *funcValue) Set() bool {
	Stdout("\n")
	return f.value()
}
