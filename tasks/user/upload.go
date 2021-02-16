package user

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"io/ioutil"
	"os"
	"path/filepath"
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
	crawl_wg     LimitGroup
	upload_wg    LimitGroup
	upload_chan  chan *upload
	file_count   Tally
	folder_count Tally
	transfered   Tally
	uploads      Table
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
	T.Flags.BoolVar(&T.input.move, "move", "Remove source files upon succesful upload.")
	T.Flags.BoolVar(&T.input.dont_overwrite, "dont_version", "Do not upload file if file exists on server already.")
	T.Flags.Order("overwrite_newer", "move")
	T.Flags.CLIArgs("src", "remote_kw_folder")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if len(T.input.src) == 0 {
		return fmt.Errorf("please provide a local folder/file for upload.")
	}

	return nil
}

func (T *FolderUploadTask) Main() (err error) {
	T.crawl_wg = NewLimitGroup(20)
	T.upload_wg = NewLimitGroup(6)
	T.uploads = T.DB.Table("uploads")

	var base_folder KiteObject

	if IsBlank(T.input.dst) {
		base_folder, err = T.KW.Folder(0).Info()
		if err != nil {
			return err
		}
	} else {
		base_folder, err = T.KW.Folder(0).Find(T.input.dst)
		if err != nil {
			if err == ErrNotFound {
				base_folder, err = T.KW.Folder(0).NewFolder(T.input.dst)
				if err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%s: %v", T.input.dst, err)
			}
		}
	}

	T.file_count = T.Report.Tally("Files")
	T.folder_count = T.Report.Tally("Folders")
	T.transfered = T.Report.Tally("Transfered", HumanSize)

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
			T.upload_wg.Add(1)
			go func(up *upload) {
				defer T.upload_wg.Done()
				var err error
				for i := 0; i < int(T.KW.Retries); i++ {
					err = T.UploadFile(up.path, up.finfo, up.dest)
					if err == ErrUploadNoResp {
						time.Sleep(time.Second*time.Duration(i) + 1)
						continue
					}
					break
				}
				if err != nil {
					Err("%s[%d]: %v", up.path, up.dest.ID, err)
					return
				}
			}(u)
		}
	}()

	for i, src := range T.input.src {
		src, err = filepath.Abs(src)
		if err != nil {
			Err("%s: %v", T.input.src[i], err)
			continue
		}
		T.crawl_wg.Add(1)
		go func(src string, folder KiteObject) {
			defer T.crawl_wg.Done()
			if err := T.ProcessFolder(src, &folder); err != nil {
				Err(err)
			}
		}(src, base_folder)
	}
	T.crawl_wg.Wait()

	// Shutdown upload
	T.upload_chan <- nil
	T.upload_wg.Wait()

	return
}

func (T *FolderUploadTask) UploadFile(local_path string, finfo os.FileInfo, folder *KiteObject) (err error) {
	defer T.file_count.Add(1)
	
	f, err := os.Open(local_path)
	if err != nil {
		return err
	}
	x := TransferCounter(f, T.transfered.Add)
	defer f.Close()

	_, err = T.KW.Upload(finfo.Name(), finfo.Size(), finfo.ModTime(), T.input.overwrite_newer, !T.input.dont_overwrite, true, *folder, x)
	return
}

/*
func (T *FolderUploadTask) UploadFile(local_path string, finfo os.FileInfo, folder *KiteObject) (err error) {
	defer T.file_count.Add(1)
	if folder.ID == 0 {
		Notice("%s: Uploading files to base path is not permitted, ignoring file.", local_path)
		return nil
	}

	var UploadRecord struct {
		Name string
		ID int
		ClientModified time.Time
		Size int64
	}

	transfer_file := func(local_path string, uid int) (err error) {
		upload_counter := func(num int) {
			T.transfered.Add(int64(num))
		}

		f, err := os.Open(local_path)
		if err != nil {
			return err
		}
		defer f.Close()

		x := TransferCounter(f, upload_counter)
		_, err = T.KW.Upload(finfo.Name(), uid, x)
		if err == nil {
			if T.input.move {
				return os.Remove(local_path)
			}
		}
		return
	}

	target := fmt.Sprintf("%d:%s", folder.ID, finfo.Name())

	if T.uploads.Get(target, &UploadRecord) {
		if UploadRecord.Name == finfo.Name() && UploadRecord.Size == finfo.Size() && UploadRecord.ClientModified == finfo.ModTime() {
			if err := transfer_file(local_path, UploadRecord.ID); err != nil {
				Debug("Error attempting to resume file: %s", err.Error())
			} else {
				T.uploads.Unset(target)
				return nil
			}
		}
	} 

	kw_file_info, err := T.KW.Folder(folder.ID).Find(finfo.Name())
	if err != nil && err != ErrNotFound {
		return err
	}
	var uid int
	//Log(kw_file_info)
	if kw_file_info.ID > 0 {
		if T.input.dont_overwrite {
			if T.input.move {
					return os.Remove(local_path)
			}
			return
		}
		modified, _ := ReadKWTime(kw_file_info.ClientModified)

		// File on kiteworks is newer than local file.
		if modified.UTC().Unix() > finfo.ModTime().UTC().Unix() {
			if T.input.overwrite_newer {
				uid, err = T.KW.File(kw_file_info.ID).NewVersion(finfo.Name(), finfo.Size(), finfo.ModTime())
				if err != nil {
					return err
				}
			} else {
				T.uploads.Unset(target)
				return nil
			}
			// Local file is newer than kiteworks file.
		} else if modified.UTC().Unix() < finfo.ModTime().UTC().Unix() {
			uid, err = T.KW.File(kw_file_info.ID).NewVersion(finfo.Name(), finfo.Size(), finfo.ModTime())
			if err != nil {
				return err
			}
			// Local file gas same timestamp as kiteworks file.
		} else {
			if kw_file_info.Size == finfo.Size() {
				T.uploads.Unset(target)
				if T.input.move {
					return os.Remove(local_path)
				} else {
					return nil
				}
			}
		}
	} else {
		uid, err = T.KW.Folder(folder.ID).NewUpload(finfo.Name(), finfo.Size(), finfo.ModTime())
		if err != nil {
			return err
		}
	}
	UploadRecord.Name = finfo.Name()
	UploadRecord.ID = uid
	UploadRecord.ClientModified = finfo.ModTime()
	UploadRecord.Size = finfo.Size()

	T.uploads.Set(target, &UploadRecord)

	for i := uint(0); i <= T.KW.Retries; i++ {
		err = transfer_file(local_path, uid)
		if err == nil || IsAPIError(err) {
			if err != nil && IsAPIError(err, "ERR_INTERNAL_SERVER_ERROR") {
				Debug("%s/%s: %s (%d/%d)", folder.Path, UploadRecord.Name, err.Error(), i+1, T.KW.Retries+1)
				T.KW.BackoffTimer(i)
				continue
			}
			T.uploads.Unset(target)
		}
		break
	}
	return
}
*/

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
						Err("%s[%d]: %v", target.Path, target.ID, err)
						continue
					} else {
						kw_folder, err = T.KW.Folder(target.ID).NewFolder(v.Name())
						if err != nil {
							if IsAPIError(err, "ERR_INPUT_FOLDER_NAME_INVALID_START_END_FORMAT") {
								Notice("%s: %v", v.Name(), err)
							} else {
								Err("%s[%d]: %v", target.Path, target.ID, err)
							}
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
