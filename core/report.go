package core

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-snuglib/eflag"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

// Create New Task Report.
func NewTaskReport(name string, file string, flags *FlagSet) *TaskReport {
	return &TaskReport{
		name:       name,
		file:       file,
		flags:      flags,
		start_time: time.Now().Round(time.Millisecond),
		tallys:     make([]*Tally, 0),
	}
}

// TraskReport
type TaskReport struct {
	lock       sync.Mutex
	name       string
	file       string
	flags      *FlagSet
	start_time time.Time
	tallys     []*Tally
}

// Generates Summary Report
func (t *TaskReport) Summary(errors uint32) {
	t.lock.Lock()
	defer t.lock.Unlock()

	var buffer bytes.Buffer
	text := tabwriter.NewWriter(&buffer, 0, 0, 1, ' ', tabwriter.AlignRight)
	end_time := time.Now()

	Info("\n")

	if t.file != "cli" {
		fmt.Fprintf(text, "\tFile: \t%s\n", t.file)
	}
	fmt.Fprintf(text, "\tTask: \t%s\n", t.name)
	if t.flags != nil {
		first := true
		log_flag := func(input *eflag.Flag) {
			switch input.Name {
			case "repeat":
				return
			case "snoop":
				return
			case "debug":
				return
			case "pause":
				return
			case "quiet":
				return
			}
			if first {
				fmt.Fprintf(text, "\tOptions: ")
				fmt.Fprintf(text, "\t%s = %v\n", t.flags.ResolveAlias(input.Name), input.Value)
				first = false
			} else {
				fmt.Fprintf(text, "\t\t%s = %v\n", t.flags.ResolveAlias(input.Name), input.Value)
			}
		}
		t.flags.Visit(log_flag)
	}
	fmt.Fprintf(text, "\tStarted: \t%v\n", t.start_time.Round(time.Millisecond))
	fmt.Fprintf(text, "\tFinished: \t%v\n", end_time.Round(time.Millisecond))

	rt := end_time.Sub(t.start_time)
	if x := rt.Round(time.Second); x == 0 {
		rt = rt.Round(time.Millisecond)
	} else {
		rt = x
	}
	fmt.Fprintf(text, "\tRuntime: \t%v\n", rt)
	if t.tallys != nil {
		for i := 0; i < len(t.tallys); i++ {
			fmt.Fprintf(text, "\t%s: \t%s\n", t.tallys[i].name, t.tallys[i].Format(*t.tallys[i].count))
		}
	}
	fmt.Fprintf(text, "\tErrors: \t%d\n", errors)
	atomic.StoreUint32(&error_counter, 0)

	text.Flush()
	for _, t := range strings.Split(buffer.String(), "\n") {
		Info(t)
	}
	return
}

// Tally for TaskReport.
type Tally struct {
	name   string
	count  *int64
	Format func(val int64) string
}

// Generates a new Tally for the TaskReport
func (r *TaskReport) Tally(name string, format ...func(val int64) string) (new_tally Tally) {
	r.lock.Lock()
	defer r.lock.Unlock()

	for i := 0; i < len(r.tallys); i++ {
		if name == r.tallys[i].name {
			return *r.tallys[i]
		}
	}
	new_tally.name = name
	new_tally.count = new(int64)
	if format == nil || format[0] == nil {
		new_tally.Format = func(val int64) string {
			return fmt.Sprintf("%d", val)
		}
	} else {
		new_tally.Format = format[0]
	}
	r.tallys = append(r.tallys, &new_tally)
	return
}

// Gets name from Tally
func (c Tally) Name() string {
	return c.name
}

// Add to Tally
func (c Tally) Add(num int64) {
	atomic.StoreInt64(c.count, atomic.LoadInt64(c.count)+num)
}

// Get the value of the Tally
func (c Tally) Value() int64 {
	return atomic.LoadInt64(c.count)
}
