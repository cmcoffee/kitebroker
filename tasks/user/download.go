package user

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"path/filepath"
	"strings"
)

type FolderDownloadTask struct {
	input struct {
		src        []string
		dst        string
		track      bool
		owned_only bool
		move       bool
	}
	db struct {
		downloads Table
		files     Table
	}
	crawl_limiter    LimitGroup
	dwnld_limiter    LimitGroup
	folders          map[int]string
	folder_count     Tally
	file_count       Tally
	transferred      Tally
	files_downloaded Tally
	dwnld_chan       chan *download
	KiteBrokerTask
}

type download struct {
	path string
	file *KiteObject
}

func (T FolderDownloadTask) New() Task {
	return new(FolderDownloadTask)
}

func (T FolderDownloadTask) Name() string {
	return "download"
}

func (T FolderDownloadTask) Desc() string {
	return "Download folders and/or files from kiteworks."
}

// Init function.
func (T *FolderDownloadTask) Init() (err error) {
	T.Flags.BoolVar(&T.input.owned_only, "owner", "Download folders and files from owned folders only.")
	T.Flags.MultiVar(&T.input.src, "src", "<remote file/folder>", "Specify kiteworks folder or file you wish to download.")
	T.Flags.StringVar(&T.input.dst, "dst", "<local folder>", "Specify local path to store downloaded folders/files.")
	T.Flags.BoolVar(&T.input.track, "track", "Track downloads. (Prevents files from being redownloaded)")
	T.Flags.BoolVar(&T.input.move, "move", "Remove sources files from kiteworks upon successful download.")
	T.Flags.Order("src", "dst", "track", "owner", "move")
	T.Flags.CLIArgs("src", "dst")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if IsBlank(T.input.dst) && len(T.input.src) == 0 {
		return fmt.Errorf("--dst is a required paramented if --src is not provided.")
	}

	return nil
}

// Main function
func (T *FolderDownloadTask) Main() (err error) {
	T.crawl_limiter = NewLimitGroup(50)
	T.dwnld_limiter = NewLimitGroup(50)

	T.db.downloads = T.DB.Table("downloads")
	T.db.files = T.DB.Bucket(fmt.Sprintf("sync:%s:%s", T.KW.Username, T.input.dst)).Table("downloads")
	T.db.files.Drop()

	T.input.dst, err = filepath.Abs(T.input.dst)
	if err != nil {
		return err
	}

	T.folder_count = T.Report.Tally("Folders Analyzed")
	T.file_count = T.Report.Tally("Files Analyzed")
	T.files_downloaded = T.Report.Tally("Files Downloaded")
	T.transferred = T.Report.Tally("Transferred", HumanSize)

	message := func() string {
		return fmt.Sprintf("Please wait ... [files: %d/folders: %d]", T.file_count.Value(), T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})

	var folders []KiteObject

	for _, f := range T.input.src {
		folder, err := T.KW.Folder("0").Find(f)
		if err != nil {
			Err("%s: %v", f, err)
			continue
		}
		folders = append(folders, folder)
	}

	if T.input.src == nil {
		folders, err = T.KW.TopFolders()
		if err != nil {
			return err
		}
	}

	err = MkDir(T.input.dst)
	if err != nil {
		return err
	}

	T.dwnld_chan = make(chan *download, 100)

	// Downloader Go Thread
	T.dwnld_limiter.Add(1)
	go func() {
		defer T.dwnld_limiter.Done()
		for {
			m := <-T.dwnld_chan
			if m == nil {
				return
			}
			T.dwnld_limiter.Add(1)
			go func(m *download) {
				defer T.dwnld_limiter.Done()
				retry := T.KW.InitRetry(T.KW.Username, m.file.Name)
				for {
					err := T.ProcessFile(m.file, m.path)
					if retry.CheckForRetry(err) {
						continue
					}
					if err != nil {
						Err("%s/%s: %v", strings.TrimPrefix(strings.TrimPrefix(m.path, T.input.dst), "/"), m.file.Name, err)
					}
					break
				}
			}(m)
		}
	}()

	for i := range folders {
		T.folder_count.Add(1)
		T.crawl_limiter.Add(1)

		go func(path string, folder *KiteObject) {
			T.ProcessFolder(folder, path)
			T.crawl_limiter.Done()
		}(T.input.dst, &folders[i])
	}

	T.crawl_limiter.Wait()

	// Shutdown downloader.
	T.dwnld_chan <- nil
	T.dwnld_limiter.Wait()

	return nil
}

func (T *FolderDownloadTask) ProcessFolder(folder *KiteObject, local_path string) {
	type child struct {
		local_path string
		*KiteObject
	}

	var folders []child
	folders = append(folders, child{local_path, folder})

	var n int
	var next []child

	// Do iterative loop if no threads are available, do recursion if there are.
	for {
		if len(folders) < n+1 {
			if len(next) > 0 {
				folders = append(folders[:0], next[0:]...)
				next = next[0:0]
			} else {
				break
			}
			n = 0
		}

		obj := folders[n]

		if n+1 < len(folders)-1 {
			if T.crawl_limiter.Try() {
				go func(path string, obj *KiteObject) {
					T.ProcessFolder(obj, path)
					T.crawl_limiter.Done()
				}(obj.local_path, obj.KiteObject)
				n++
				continue
			}
		}

		switch obj.Type {
		case "d":
			if obj.CurrentUserRole.ID < 5 && T.input.owned_only {
				n++
				continue
			}
			if obj.CurrentUserRole.ID < 2 {
				n++
				continue
			}
			if obj.Secure {
				folder, err := T.KW.Folder(obj.ID).Info()
				if err != nil {
					Err("%s: %v", obj.Name, err)
					n++
					continue
				}
				if !(folder.CurrentUserRole.ID >= 4) {
					Notice("%s is 'RESTRICTED': Current permissions do not allow downloads.", obj.Path)
					n++
					continue
				}
			}

			T.folder_count.Add(1)
			err := MkDir(CombinePath(obj.local_path, obj.Name))
			if err != nil {
				Err("%s: %v", obj.Path, err)
				n++
				continue
			}
			obj.local_path = CombinePath(obj.local_path, obj.Name)

			objs, err := T.KW.Folder(obj.ID).Contents()
			if err != nil {
				Err(err)
				n++
				continue
			}

			for i := 0; i < len(objs); i++ {
				switch objs[i].Type {
				case "d":
					next = append(next, child{obj.local_path, &objs[i]})
				case "f":
					T.dwnld_chan <- &download{obj.local_path, &objs[i]}
				}
			}
		case "f":
			T.dwnld_chan <- &download{obj.local_path, obj.KiteObject}
		}
		n++
	}
}

const (
	incomplete = 1 << iota
	complete
)

func (T *FolderDownloadTask) ProcessFile(file *KiteObject, local_path string) (err error) {
	var dl_record uint

	T.file_count.Add(1)
	download_record_name := fmt.Sprintf("%d:%s:%s:%d", file.ID, file.Name, file.Created, file.Size)

	clear_from_db := func(file_id string) {
		for _, k := range T.db.downloads.Keys() {
			if strings.Split(k, ":")[0] == file_id {
				T.db.downloads.Unset(k)
			}
		}
	}

	found := T.db.downloads.Get(download_record_name, &dl_record)

	if T.input.track && found && dl_record == 1 {
		if T.input.move {
			clear_from_db(file.ID)
		} else {
			return nil
		}
	}

	err = T.KW.LocalDownload(file, local_path, T.transferred.Add)
	if err != nil {
		return err
	}

	mark_complete := func() (err error) {
		if T.input.move {
			err = T.KW.File(file.ID).Delete()
			if err == nil {
				clear_from_db(file.ID)
			}
		}
		if T.input.track {
			T.db.downloads.Set(download_record_name, 1)
			T.db.files.Set(CombinePath(file.Path, file.Name), 1)
		}
		return nil
	}

	T.files_downloaded.Add(1)

	return mark_complete()
}

/*
func (T *FolderDownloadTask) ProcessFile(file *KiteObject, local_path string) (err error) {

	var flag BitFlag

	T.file_count.Add(1)

	download_record_name := fmt.Sprintf("%d:%s:%s:%d", file.ID, file.Name, file.Created, file.Size)

	clear_from_db := func(file_id string) {
		for _, k := range T.db.downloads.Keys() {
			if strings.Split(k, ":")[0] == file_id {
				T.db.downloads.Unset(k)
			}
		}
	}

	found := T.db.downloads.Get(download_record_name, &flag)

	if T.input.track && found && flag.Has(complete) {
		if T.input.move {
			clear_from_db(file.ID)
		} else {
			return nil
		}
	}

	var mtime time.Time

	if !IsBlank(file.ClientModified) {
		mtime, err = ReadKWTime(file.ClientModified)
		if err != nil {
			return err
		}
	} else if !IsBlank(file.Modified) {
		mtime, err = ReadKWTime(file.Modified)
		if err != nil {
			return err
		}
	}

	file_name := CombinePath(local_path, file.Name)
	tmp_file_name := fmt.Sprintf("%s.%s.incomplete", file_name, file.ID)

	mark_complete := func() (err error) {
		clear_from_db(file.ID)
		if T.input.move {
			return T.KW.File(file.ID).Delete()
		}
		if T.input.track {
			flag.Set(complete)
			T.db.downloads.Set(download_record_name, &flag)
			T.db.files.Set(CombinePath(file.Path, file.Name), &file)
		}
		return nil
	}

	dstat, err := os.Stat(file_name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if dstat != nil && dstat.Size() == file.Size && dstat.ModTime().UTC().Unix() == mtime.UTC().Unix() {
		return mark_complete()
	}

	f, err := T.KW.FileDownload(file)
	if err != nil {
		return err
	}

	fstat, err := os.Stat(tmp_file_name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if !found && fstat != nil {
		if err := os.Remove(tmp_file_name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	flag.Set(incomplete)
	clear_from_db(file.ID)
	T.db.downloads.Set(download_record_name, &flag)

	dst, err := os.OpenFile(tmp_file_name, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	if fstat != nil && found {
		offset, err := dst.Seek(fstat.Size(), 0)
		if err != nil {
			return err
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			return err
		}
	}

	if fstat == nil || fstat.Size() != file.Size {
		update_transfer := func(num int) {
			T.transferred.Add(num)
		}
		f = TransferCounter(f, update_transfer)
		_, err := io.Copy(dst, f)
		if err != nil {
			f.Close()
			dst.Close()

			if file.AdminQuarantineStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, file is under administrator quarantine.", strings.TrimSuffix(local_path, SLASH), file.Name)
				os.Remove(tmp_file_name)
				return nil
			}
			if file.AVStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, anti-virus status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.AVStatus)
				os.Remove(tmp_file_name)
				return nil
			}
			if file.DLPStatus != "allowed" {
				Notice("%s/%s: Cannot be downloaded, dli status is currently set to: %s", strings.TrimSuffix(local_path, SLASH), file.Name, file.DLPStatus)
				os.Remove(tmp_file_name)
				return nil
			}
			return err
		}
	}

	T.files_downloaded.Add(1)
	f.Close()
	dst.Close()

	err = Rename(tmp_file_name, file_name)
	if err != nil {
		return err
	}

	err = os.Chtimes(file_name, time.Now(), mtime)
	if err == nil {
		return mark_complete()
	}
	return
}*/
