package common

import (
	"bytes"
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

type TaskReport struct {
	name        string
	file        string
	start_time  time.Time
	index       int
	elem_names  []string
	elem_values []*int64
}

func (t *TaskReport) Summary(errors uint32) {
	if t.elem_names == nil {
		return
	}

	var buffer bytes.Buffer
	text := tabwriter.NewWriter(&buffer, 0, 0, 1, ' ', tabwriter.AlignRight)
	end_time := time.Now()

	Log("\n")
	//fmt.Fprintf(text, "################### Task Summary ###################\n")
	if t.file != "cli" {
		fmt.Fprintf(text, "\tFile: \t%s\n", t.file)
	}
	fmt.Fprintf(text, "\tTask: \t%s\n", t.name)
	fmt.Fprintf(text, "\tStarted: \t%v\n", t.start_time.Round(time.Millisecond))
	fmt.Fprintf(text, "\tFinished: \t%v\n", end_time.Round(time.Millisecond))
	fmt.Fprintf(text, "\tRuntime: \t%v\n", end_time.Sub(t.start_time).Round(time.Second))
	for i, n := range t.elem_names {
		fmt.Fprintf(text, "\t%s: \t%d\n", n, *t.elem_values[i])
	}
	fmt.Fprintf(text, "\tErrors: \t%d\n", errors)
	//fmt.Fprintf(text, "####################################################")
	text.Flush()
	for _, t := range strings.Split(buffer.String(), "\n") {
		Log(t)
	}
	return
}

type Tally struct {
	count *int64
}

func (r *TaskReport) Tally(name string) Tally {
	var num int64
	r.elem_names = append(r.elem_names, name)
	r.elem_values = append(r.elem_values, &num)
	tally := Tally{&num}
	r.index++
	return tally
}

func (c *Tally) Add(num int64) {
	atomic.StoreInt64(c.count, atomic.LoadInt64(c.count)+num)
}
