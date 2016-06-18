package main

import (
	"fmt"
	"github.com/cmcoffee/go-cfg"
	"github.com/cmcoffee/go-kvlite"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	DEFAULT_CONF  = "kitebroker.cfg"
	NAME          = "kitebroker"
	VERSION       = "0.0.2a"
	KWAPI_VERSION = "4.1"
	NONE          = ""
)

var (
	Config *cfg.Store
	DB     *kvlite.Store
	wg     *limited_wait
)

// Fatal error handler.
func errChk(err error) {
	if err != nil {
		fmt.Println("fatal: ", err.Error())
		os.Exit(1)
	}
}

// Provides a clean path, for Windows ... and everyone else.
func cleanPath(input string) string {
	return filepath.Clean(input)
}

// Empty struct, used simply for filling and depleating the channel.
type s_sig struct{}

// Wait group with a limit on threads used.
type limited_wait struct {
	limit chan (s_sig)
	wg    *sync.WaitGroup
}

// Similar to sync.WaitGroup in every way, just with a limit. :)
func NewWaitGroup(limit int) (l *limited_wait) {
	if limit < 0 {
		limit = 1
	}
	l = &limited_wait{
		make(chan s_sig, limit),
		&sync.WaitGroup{},
	}
	for i := 0; i < limit; i++ {
		l.limit <- s_sig{}
	}
	return
}

func (l *limited_wait) Add() {
	<-l.limit
	l.wg.Add(1)
}
func (l *limited_wait) Done() {
	l.wg.Done()
	l.limit <- s_sig{}
}
func (l *limited_wait) Wait() { l.wg.Wait() }

func main() {
	var err error

	// Read configuration file.
	Config, err = cfg.ReadOnly(DEFAULT_CONF)
	errChk(err)

	// Open kitebroker sqlite database.
	DB, err = kvlite.Open("kitebroker.dat")
	errChk(err)

	path := Config.Get("kiteworks", "path")[0]

	max_threads, err := strconv.Atoi(Config.Get("kitebroker", "max_threads")[0])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	wg = NewWaitGroup(max_threads)

	finfo, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		errChk(os.Mkdir(path, 0755))
	} else if err != nil {
		errChk(err)
	}

	if finfo != nil && finfo.IsDir() == false {
		errChk(os.Remove(path))
		errChk(os.Mkdir(path, 0755))
	}

	ival, err := time.ParseDuration(Config.Get("kitebroker", "polling_rate")[0])
	errChk(err)

	fmt.Printf("[Accellion %s(v%s)]\n\n", NAME, VERSION)

	// Scan cycle.
	for {
		start := time.Now().Round(time.Second)
		for _, user := range Config.Get("kiteworks", "users") {
			fmt.Printf("--- Scanning files for %s ---\n", user)
			s := NewSession("kiteworks", user)
			folder_id, _ := s.MyFolderID()
			err := s.DownloadFolder(folder_id, path+"/"+user, nil)
			if err != nil {
				fmt.Printf("! Error: %s\n", err.Error())
			}
		}
		for time.Now().Sub(start) < ival {
			fmt.Printf("\r* Rescan will occur in %s", time.Duration(ival-time.Now().Round(time.Second).Sub(start)).String())
			time.Sleep(time.Duration(2 * time.Second))
		}
		fmt.Printf("\r* Restarting Scan Cycle ... (%s has elapsed since last run.)\n", time.Now().Round(time.Second).Sub(start).String())
	}

}
