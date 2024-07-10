package user

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FolderUploadTask struct {
	input struct {
		src             []string
		dst             string
		overwrite_newer bool
		move            bool
		dont_overwrite  bool
	}
	db struct {
		uploads Table
		files   Table
	}
	crawl_wg     LimitGroup
	upload_wg    LimitGroup
	upload_chan  chan *upload
	file_count   Tally
	folder_count Tally
	transferred  Tally
	uploads      Table
	cache        FileCache
	KiteBrokerTask
}

type upload struct {
	path  string
	finfo os.FileInfo
	dest  *KiteObject
}

func (T *FolderUploadTask) New() Task {
	return new(FolderUploadTask)
}

func (T FolderUploadTask) Name() string {
	return "upload"
}

func (T FolderUploadTask) Desc() string {
	return "Upload folders and/or files to kiteworks."
}

func (T *FolderUploadTask) Init() (err error) {
	T.Flags.StringVar(&T.input.dst, "remote_kw_folder", "<remote folder>", "Specify kiteworks folder you wish to upload to.")
	T.Flags.MultiVar(&T.input.src, "src", "<local file/folder>", "Specify local path to folder or file you wish to upload.")
	T.Flags.BoolVar(&T.input.overwrite_newer, "overwrite_newer", "Overwrite newer files on server.")
	T.Flags.BoolVar(&T.input.move, "move", "Remove source files upon successful upload.")
	T.Flags.BoolVar(&T.input.dont_overwrite, "dont_version", "Do not upload file if file exists on server already.")
	T.Flags.Order("remote_kw_folder", "overwrite_newer", "move")
	T.Flags.InlineArgs("src", "remote_kw_folder")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if len(T.input.src) == 0 {
		return fmt.Errorf("must provide a local folder/file for upload.")
	}

	return nil
}

func (T *FolderUploadTask) Main() (err error) {
	T.crawl_wg = NewLimitGroup(50)
	T.upload_wg = NewLimitGroup(50)
	T.uploads = T.DB.Table("uploads")

	var base_folder KiteObject

	user_info, err := T.KW.MyUser()
	if err != nil {
		return err
	}

	T.file_count = T.Report.Tally("Files")
	T.folder_count = T.Report.Tally("Folders")
	T.transferred = T.Report.Tally("Transferred", HumanSize)

	message := func() string {
		return fmt.Sprintf("Please wait ... [files: %d/folders: %d]", T.file_count.Value(), T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})

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
			T.file_count.Add(1)
			T.upload_wg.Add(1)
			go func(up *upload) {
				retry := T.KW.InitRetry(T.KW.Username, fmt.Sprintf("%s/%s", up.dest.Name, up.finfo.Name()))
				defer T.upload_wg.Done()
				for {
					if err := T.UploadFile(up.path, up.finfo, up.dest); err != nil {
						if retry.CheckForRetry(err) {
							continue
						}
						Err("(%s) Unexpected error while uploading %s: %s", T.KW.Username, up.path, err.Error())
						T.file_count.Del(1)
					}
					return
				}
			}(u)
		}
	}()

	for i, src := range T.input.src {
		src_path := src
		if len(src_path) >= 2 {
			if src_path[1] == ':' {
				src_split := strings.Split(src_path, ":")
				src_path = src_split[1]
			}
		}
		s := strings.Split(NormalizePath(src_path), "/")
		src_path = s[len(s)-1]
		if IsBlank(T.input.dst) {
			switch src[len(src)-1] {
			case '/':
				fallthrough
			case '*':
				base_folder, err = T.KW.Folder(user_info.BaseDirID).Info()
				if err != nil {
					return err
				}
			default:
				base_folder, err = T.KW.Folder(user_info.BaseDirID).ResolvePath(src_path)
				if err != nil {
					return err
				}
			}
		} else {
			destination := T.input.dst
			if !strings.HasSuffix(src, SLASH) && src[len(src)-1] != '*' {
				destination = fmt.Sprintf("%s/%s", destination, src_path)
			}
			base_folder, err = T.KW.Folder(user_info.BaseDirID).ResolvePath(destination)
			if err != nil {
				return err
			}
		}
		src, err = filepath.Abs(src)
		if err != nil {
			Err("%s: %v", T.input.src[i], err)
			continue
		}
		src = strings.TrimSuffix(src, "*")

		T.crawl_wg.Add(1)
		go func(src string, folder KiteObject) {
			defer T.crawl_wg.Done()
			if err := T.ProcessFolder(src, &folder); err != nil {
				Err(err)
			}
		}(src, base_folder)
	}
	T.crawl_wg.Wait()

	for len(T.upload_chan) > 0 {
		time.Sleep(time.Second)
	}

	// Shutdown upload
	T.upload_chan <- nil
	T.upload_wg.Wait()

	return
}

func (T *FolderUploadTask) UploadFile(local_path string, finfo os.FileInfo, folder *KiteObject) (err error) {
	if T.cache.Check(finfo, folder) == true {
		return nil
	}

	if finfo.Mode().Type() == fs.ModeSymlink {
		return nil
	}

	f, err := os.Open(local_path)
	if err != nil {
		return err
	}

	x := TransferCounter(f, T.transferred.Add)
	defer f.Close()

	_, err = T.KW.Upload(finfo.Name(), finfo.Size(), finfo.ModTime(), T.input.overwrite_newer, !T.input.dont_overwrite, true, *folder, x)

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

	T.cache.CacheFolder(T.KW, folder)

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
			if len(next) > 0 {
				current = append(current[:0], next[0:]...)
				next = next[0:0]
				n = 0
			} else {
				break
			}
		}

		target := current[n]

		if n+1 < len(current)-1 {
			if T.crawl_wg.Try() {
				go func(local_path string, folder *KiteObject) {
					defer T.crawl_wg.Done()
					T.ProcessFolder(local_path, folder)
				}(target.path, target.KiteObject)
				n++
				continue
			}
		}

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

		for _, v := range nested {
			if v.IsDir() {
				T.folder_count.Add(1)
				kw_folder, err := T.KW.Folder(target.ID).Find(v.Name())
				if err != nil {
					if err != ErrNotFound {
						Err("%s[%s]: %v", target.Path, target.ID, err)
						continue
					} else {
						kw_folder, err = T.KW.Folder(target.ID).NewFolder(v.Name())
						if err != nil {
							if IsAPIError(err, "ERR_INPUT_FOLDER_NAME_INVALID_START_END_FORMAT") {
								Notice("%s: %v", v.Name(), err)
							} else {
								Err("%s[%s]: %v", target.Path, target.ID, err)
							}
							continue
						}
					}
					T.cache.CacheFolder(T.KW, &kw_folder)
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
