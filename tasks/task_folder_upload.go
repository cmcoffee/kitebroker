package tasks

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"io/ioutil"
	"os"
	"path/filepath"
)

type FolderUploadTask struct {
	input struct {
		src             string
		dst             string
		overwrite_newer bool
	}
	crawl_wg     LimitGroup
	upload_wg    LimitGroup
	upload_chan  chan *upload
	file_count   Tally
	folder_count Tally
	transfered   Tally
	ppt          Passport
}

type upload struct {
	path  string
	finfo os.FileInfo
	dest  *KiteObject
}

func (T *FolderUploadTask) New() Task {
	return new(FolderUploadTask)
}

func (T *FolderUploadTask) Init(flag *FlagSet) (err error) {
	flag.StringVar(&T.input.dst, "dst", "<kw folder>", "Specify folders you wish to download.")
	flag.StringVar(&T.input.src, "src", "<local destination folder>", "Specify local path to store downloaded folders.")
	flag.BoolVar(&T.input.overwrite_newer, "overwrite-newer", false, "Overwrite newer files on server.")
	if err = flag.Parse(); err != nil {
		return err
	}

	if IsBlank(T.input.src) {
		return fmt.Errorf("please provide a --src for upload.")
	}

	return nil
}

func (T *FolderUploadTask) Main(ppt Passport) (err error) {
	T.ppt = ppt
	T.crawl_wg = NewLimitGroup(20)
	T.upload_wg = NewLimitGroup(5)

	T.input.src, err = filepath.Abs(T.input.src)
	if err != nil {
		return err
	}

	var base_folder KiteObject

	if IsBlank(T.input.dst) {
		base_folder, err = ppt.Folder(0).Info()
		if err != nil {
			return err
		}
	} else {
		base_folder, err = ppt.Folder(0).Find(T.input.dst)
		if err != nil {
			if err == ErrNotFound {
				base_folder, err = ppt.CreateFolder(0, T.input.dst)
				if err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%s: %v", T.input.dst, err)
			}
		}
	}

	T.file_count = T.ppt.Tally("Files")
	T.folder_count = T.ppt.Tally("Folders")
	T.transfered = T.ppt.Tally("Transfered", HumanSize)

	message := func() string {
		return fmt.Sprintf("Please wait ... [files: %d/folders: %d]", T.file_count.Value(), T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()
	defer DefaultPleaseWait()

	T.upload_chan = make(chan *upload, 100)
	// Spin up go thread for uploading.
	T.upload_wg.Add(1)
	go func() {
		defer T.upload_wg.Done()
		for {
			u := <-T.upload_chan
			if u == nil {
				return
			}
			T.upload_wg.Add(1)
			go func(up *upload) {
				defer T.upload_wg.Done()
				err := T.UploadFile(up.path, up.finfo, up.dest)
				if err != nil {
					Err("%s: %v", up.path, err)
					return
				}
			}(u)
		}
	}()

	T.ProcessFolder(T.input.src, &base_folder)
	T.crawl_wg.Wait()

	// Shutdown upload
	T.upload_chan <- nil
	T.upload_wg.Wait()

	return
}

func (T *FolderUploadTask) UploadFile(local_path string, finfo os.FileInfo, folder *KiteObject) (err error) {
	defer T.file_count.Add(1)
	if folder.ID == 0 {
		Notice("%s: Uploading files to base path is not permitted, ignoring file.", local_path)
		return nil
	}
	kw_file_info, err := T.ppt.Folder(folder.ID).Find(finfo.Name())
	if err != nil && err != ErrNotFound {
		return err
	}
	var uid int
	//Log(kw_file_info)
	if kw_file_info.ID > 0 {
		modified, _ := ReadKWTime(kw_file_info.Modified)

		if modified.UTC().Unix() > finfo.ModTime().UTC().Unix() {
			if T.input.overwrite_newer {
				uid, err = T.ppt.NewVersion(kw_file_info.ID, finfo)
				if err != nil {
					return err
				}
			} else {
				return nil
			}

		}
	} else {
		uid, err = T.ppt.NewUpload(folder.ID, finfo)
		if err != nil {
			return err
		}
	}
	f, err := os.Open(local_path)
	if err != nil {
		return err
	}
	_, err = T.ppt.Upload(finfo.Name(), uid, f)
	if err == nil {
		T.transfered.Add(finfo.Size())
	}
	return
}

func (T *FolderUploadTask) ProcessFolder(local_path string, folder *KiteObject) (err error) {

	type child struct {
		path string
		os.FileInfo
		*KiteObject
	}

	finfo, err := os.Stat(local_path)
	if err != nil {
		return err
	}

	if !finfo.IsDir() {
		T.upload_chan <- &upload{local_path, finfo, folder}
		return
	}

	var (
		current []child
		next    []child
		n       int
	)

	current = append(current, child{local_path, finfo, folder})

	for {
		if n > len(current)-1 {
			current = current[0:0]
			if len(next) > 0 {
				for _, o := range next {
					if T.crawl_wg.Try() {
						go func(local_path string, folder *KiteObject) {
							defer T.crawl_wg.Done()
							T.ProcessFolder(local_path, folder)
						}(o.path, o.KiteObject)
					} else {
						current = append(current, o)
					}
				}
			} else {
				break
			}
			next = next[0:0]
			n = 0
			if len(current) == 0 {
				return
			}
		}
		target := current[n]
		if target.FileInfo.Name() == ".." || target.FileInfo.Name() == "." {
			n++
			continue
		}
		if !target.IsDir() {
			T.upload_chan <- &upload{target.path, target.FileInfo, target.KiteObject}
			n++
			continue
		}
		nested, err := ioutil.ReadDir(target.path)
		if err != nil {
			Err("%s: %v", target.path, err)
			n++
			continue
		}
		T.folder_count.Add(1)

		for _, v := range nested {
			if v.IsDir() {
				kw_folder, err := T.ppt.Folder(target.ID).Find(v.Name())
				if err != nil {
					if err != ErrNotFound {
						Err("%s: %v", target.Path, err)
						n++
						continue
					} else {
						kw_folder, err = T.ppt.CreateFolder(target.ID, v.Name())
						if err != nil {
							Err("%s: %v", target.Path, err)
							n++
							continue
						}
					}
				}
				next = append(next, child{CombinePath(target.path, v.Name()), v, &kw_folder})
			} else {
				next = append(next, child{CombinePath(target.path, v.Name()), v, target.KiteObject})
			}
		}
		n++
	}

	return nil
}
