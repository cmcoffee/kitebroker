package core

import (
	"bytes"
	"fmt"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

type TaskReport struct {
	name       string
	file       string
	start_time time.Time
	tallys     []*Tally
}

func (t *TaskReport) Summary(errors uint32) {
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
	if t.tallys != nil {
		for i := 0; i < len(t.tallys); i++ {
			fmt.Fprintf(text, "\t%s: \t%s\n", t.tallys[i].name, t.tallys[i].Format(*t.tallys[i].count))
		}
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
	name   string
	count  *int64
	Format func(val int64) string
}

func (r *TaskReport) Tally(name string, format ...func(val int64) string) (new_tally Tally) {
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

func (c *TaskReport) FindTally(name string) int64 {
	for _, v := range c.tallys {
		if v.name == name {
			return *v.count
		}
	}
	return 0
}

func (c Tally) Add(num int64) {
	atomic.StoreInt64(c.count, atomic.LoadInt64(c.count)+num)
}

func (c Tally) Value() int64 {
	return atomic.LoadInt64(c.count)
}
