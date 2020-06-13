package main

import (
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	//"time"
	//"os"
	//"io"
	//"sync/atomic"
	//"sync"
)

func init() {
	glo.menu.Register("folder_download", "Download files & folders from kiteworks.", new(FolderDownload))
}

type FolderDownload struct {
	dst string
	src []string
	delete bool
}

func (T *FolderDownload) New() task {
	return new(FolderDownload)
}

func (T *FolderDownload) Init(flag *FlagSet) (err error) {
	flag.StringVar(&T.dst, "save_to", "<destination path>", "Local path to store files and folders from kiteworks.")
	flag.ArrayVar(&T.src, "kiteworks", "<kiteworks folder>", "kiteworks folders to download folders and files from.\n\t(multiple folders require multiple '--kiteworks' args.)")
	flag.BoolVar(&T.delete, "delete", false, "Delete files that don't exist on source.")
	if err := flag.Parse(); err != nil {
		return err
	}
	if T.dst == NONE || T.src == nil {
		return fmt.Errorf("Need to specfify both at least one '--kiteworks' folder and one '--save_to' path.")
	}
	return nil
}

func (T *FolderDownload) Main() (err error) {

	var f_list []KiteObject

	Log("Folders: %v", T.src)

	for _, src := range T.src {
		var f []KiteObject

		if src == "/" || src == "." || src == NONE {
			f, err = glo.user.TopFolders()
			if err != nil {
				Err(err)
				continue
			}
		} else {
			folder, err := glo.user.Find(0, src)
			if err != nil {
				Err("%s: %v", src, err)
				continue
			}
			Log(folder)
			f, err = glo.user.FolderContents(folder.ID)
			if err != nil {
				Err("%s: %v", src, err)
				continue
			}
		}
		if f != nil {
			f_list = append(f_list, f[0:]...)
		}
	}

	for _, f := range f_list {
		Log("%s: %s", f.Name, f.Type)
	}

	return nil
}

func (T *FolderDownload) CrawlFolder(folder_id int) (results *[]KiteObject, err error) {
	return nil, nil
}