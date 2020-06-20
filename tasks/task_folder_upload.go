package tasks

import (
	. "github.com/cmcoffee/kitebroker/core"
	"path/filepath"
	"strings"
	"fmt"
)


type FolderUploadTask struct {
	input struct {
		src string
		dst string
	}
}

func (T *FolderUploadTask) New() Task {
	return new(FolderUploadTask)
}


func (T *FolderUploadTask) Init(flag *FlagSet) (err error) {
	flag.StringVar(&T.input.dst, "kw_folder", "<kw folder>", "Specify folders you wish to download.")
	flag.StringVar(&T.input.src, "local_path", "<local destination folder>", "Specify local path to store downloaded folders.")
	if err = flag.Parse(); err != nil {
		return err
	}

	return nil
}

func (T *FolderUploadTask) Main(pass Passport) (err error) {
	xo = pass

	T.input.src, err = filepath.Abs(T.input.src)
	if err != nil {
		return err
	}

	folders, _ := ScanPath(T.input.src)
	var tmp []string
	remove_path := fmt.Sprintf("%s%s", T.input.src, SLASH)
	for _, v := range folders {
		if v == T.input.src {
			continue
		}
		tmp = append(tmp, strings.TrimPrefix(v, remove_path))
	}
	folders = folders[0:0]
	folders = tmp
	Log(tmp)
	return
}