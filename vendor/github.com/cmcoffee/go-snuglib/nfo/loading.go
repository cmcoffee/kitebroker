package nfo

import (
	"fmt"
	"github.com/cmcoffee/go-snuglib/xsync"
	"sync"
	"sync/atomic"
	"time"
)

func init() {
	PleaseWait = new(loading)
	ProgressBar = new(progressBar)
	PleaseWait.Set(func() string { return "Please wait ..." }, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
}

// PleaseWait is a wait prompt to display between requests.
var PleaseWait *loading

type loading struct {
	flag    xsync.BitFlag
	message func() string
	anim_1  []string
	anim_2  []string
	mutex   sync.Mutex
	counter int32
}

type loading_backup struct {
	message func() string
	anim_1  []string
	anim_2  []string
}

const (
	loading_show = 1 << iota
)

func (B *loading_backup) Restore() {
	PleaseWait.Set(B.message, B.anim_1, B.anim_2)
}

func (L *loading) Backup() *loading_backup {
	L.mutex.Lock()
	defer L.mutex.Unlock()
	return &loading_backup{L.message, L.anim_1, L.anim_2}
}

// Specify a "Please wait" animated PleaseWait line.
func (L *loading) Set(message func() string, loader ...[]string) {
	L.mutex.Lock()
	defer L.mutex.Unlock()

	if len(loader) == 0 {
		return
	}

	var anim_1, anim_2 []string

	anim_1 = loader[0]
	if len(loader) > 1 {
		anim_2 = loader[1]
	}

	if anim_2 == nil || len(anim_2) < len(anim_1) {
		anim_2 = make([]string, len(anim_1))
	}

	L.message = message
	L.anim_1 = anim_1
	L.anim_2 = anim_2
	count := atomic.AddInt32(&L.counter, 1)

	go func(message func() string, anim_1 []string, anim_2 []string, count int32) {
		for count == atomic.LoadInt32(&L.counter) {
			for i, str := range anim_1 {
				if L.flag.Has(loading_show) && count == atomic.LoadInt32(&L.counter) {
					Flash("%s %s %s", str, message(), anim_2[i])
				}
				time.Sleep(125 * time.Millisecond)
			}
		}
	}(message, anim_1, anim_2, count)
}

// Displays loader. "[>>>] Working, Please wait."
func (L *loading) Show() {
	L.flag.Set(loading_show)
}

// Hides display loader.
func (L *loading) Hide() {
	L.flag.Unset(loading_show)
}

type progressBar struct {
	mutex    sync.Mutex
	cur      int64
	max      int64
	working  bool
	name     string
	anim_len int
	backup   *loading_backup
}

var ProgressBar *progressBar

// Produces progress bar for information on update.
func (p *progressBar) draw() string {
	cur := atomic.LoadInt64(&p.cur)
	max := atomic.LoadInt64(&p.max)

	return DrawProgressBar(27-p.anim_len, cur, max, fmt.Sprintf("%d/%d %s.", cur, max, p.name))
}

func (p *progressBar) updateMessage() string {
	return p.draw()
}

// Updates loading to be a progress bar.
func (p *progressBar) New(name string, max int) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.working {
		return
	}

	p.cur = 0
	p.max = int64(max)
	p.name = name
	p.backup = PleaseWait.Backup()
	PleaseWait.Set(p.updateMessage, PleaseWait.anim_1)
	p.anim_len = len(PleaseWait.anim_1)
	p.working = true
}

// Adds to progress bar.
func (p *progressBar) Add(num int) {
	atomic.StoreInt64(&p.cur, atomic.LoadInt64(&p.cur)+int64(num))
}

// Complete progress bar, return to loading.
func (p *progressBar) Done() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.working {
		return
	}

	if p.backup != nil {
		p.backup.Restore()
	}
	p.working = false
}
