package tasks

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FolderDownloadTask struct {
	input struct {
		src        []string
		dst        string
		redownload bool
		owned_only bool
	}
	crawl_limiter    LimitGroup
	dwnld_limiter    LimitGroup
	folders          map[int]string
	folder_count     Tally
	file_count       Tally
	transfered       Tally
	files            Table
	files_downloaded Tally
	downloads        Table
	dwnld_chan       chan *download
	ppt              Passport
}

type download struct {
	path string
	file *KiteObject
}

func (T *FolderDownloadTask) New() Task {
	return new(FolderDownloadTask)
}

// Init function.
func (T *FolderDownloadTask) Init(flag *FlagSet) (err error) {
	flag.BoolVar(&T.input.owned_only, "owned_folders_only", false, "Only process folders I own.")
	flag.ArrayVar(&T.input.src, "src", "<kw folder>", "Specify folders or file you wish to download.\n\t  (use multiple --src args for multi-folder/file)")
	flag.Alias("src", "s")
	flag.StringVar(&T.input.dst, "dst", "<local destination folder>", "Specify local path to store downloaded folders/files.")
	flag.Alias("dst", "d")
	flag.BoolVar(&T.input.redownload, "redownload", false, "Redownload previously downloaded files.")
	if err = flag.Parse(); err != nil {
		return err
	}

	if T.input.dst == NONE {
		return fmt.Errorf("--dst is a required paramented.")
	}

	return nil
}

// Main function
func (T *FolderDownloadTask) Main(ppt Passport) (err error) {
	T.ppt = ppt

	T.crawl_limiter = NewLimitGroup(50)
	T.dwnld_limiter = NewLimitGroup(50)

	T.files = T.ppt.Shared("sync").Table("files")
	T.downloads = T.ppt.Shared(T.ppt.Username).Table("downloads")

	var into_root bool
	if !strings.HasSuffix(T.input.dst, SLASH) && len(T.input.src) == 1 {
		into_root = true
	}

	T.input.dst, err = filepath.Abs(T.input.dst)
	if err != nil {
		return err
	}

	T.folder_count = T.ppt.Tally("Folders Analyzed")
	T.file_count = T.ppt.Tally("Files Analyzed")
	T.files_downloaded = T.ppt.Tally("Files Downloaded")
	T.transfered = T.ppt.Tally("Transfered", HumanSize)

	message := func() string {
		return fmt.Sprintf("Please wait ... [files: %d/folders: %d]", T.file_count.Value(), T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()
	defer DefaultPleaseWait()

	var folders []KiteObject

	for _, f := range T.input.src {
		folder, err := T.ppt.Folder(0).Find(f)
		if err != nil {
			Err("%s: %v", f, err)
			continue
		}
		folders = append(folders, folder)
	}

	if T.input.src == nil {
		folders, err = T.ppt.TopFolders()
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
				break
			}
			T.dwnld_limiter.Add(1)
			go func(m *download) {
				defer T.dwnld_limiter.Done()
				if err := T.ProcessFile(m.file, m.path); err != nil {
					Err("%s: %v", m.file.Name, err)
				}
			}(m)
		}
	}()

	for i, _ := range folders {
		T.folder_count.Add(1)
		T.crawl_limiter.Add(1)

		if into_root {
			folders[i].Name = NONE
		}

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
		path string
		*KiteObject
	}

	var folders []child
	folders = append(folders, child{local_path, folder})

	var n int
	var next []child

	// Do iterative loop if no threads are available, do recursion if there are.
	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if T.crawl_limiter.Try() {
						go func(path string, obj *KiteObject) {
							T.ProcessFolder(obj, local_path)
							T.crawl_limiter.Done()
						}(o.path, o.KiteObject)
					} else {
						folders = append(folders, next[i])
					}
				}
				next = next[0:0]
				n = 0
				if len(folders) == 0 {
					break
				}
			} else {
				break
			}
		}

		obj := folders[n]
		switch obj.Type {
		case "d":
			if obj.CurrentUserRole.ID < 5 && T.input.owned_only {
				n++
				continue
			}
			if obj.Secure {
				folder, err := T.ppt.Folder(obj.ID).Info()
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
			err := MkDir(CombinePath(obj.path, obj.Name))
			if err != nil {
				Err("%s: %v", obj.Path, err)
				n++
				continue
			}
			objs, err := T.ppt.Folder(obj.ID).Contents()
			if err != nil {
				Err(err)
				n++
				continue
			}
			for i := 0; i < len(objs); i++ {
				switch objs[i].Type {
				case "d":
					next = append(next, child{CombinePath(obj.path, obj.Name), &objs[i]})
				case "f":
					T.dwnld_chan <- &download{CombinePath(obj.path, obj.Name), &objs[i]}
				}
			}
		case "f":
			T.dwnld_chan <- &download{obj.path, obj.KiteObject}
		}
		n++
	}
}

const (
	incomplete = 1 << iota
	complete
)

func (T *FolderDownloadTask) ProcessFile(file *KiteObject, local_path string) (err error) {

	var flag BitFlag

	T.file_count.Add(1)

	found := T.downloads.Get(fmt.Sprintf("%d", file.ID), &flag)

	if !T.input.redownload && found && flag.Has(complete) {
		return nil
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
	tmp_file_name := fmt.Sprintf("%s.incomplete", file_name)

	mark_complete := func() {
		flag.Set(complete)
		T.downloads.Set(fmt.Sprintf("%d", file.ID), &flag)
		T.files.Set(CombinePath(file.Path, file.Name), &file)
	}

	dstat, err := os.Stat(file_name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if dstat != nil && dstat.Size() == file.Size {
		if dstat.ModTime().UTC().Unix() == mtime.UTC().Unix() {
			md5, err := MD5Sum(file_name)
			if err != nil {
				return err
			} else {
				if md5 == file.Fingerprint {
					mark_complete()
					return nil
				}
			}
		}
	}

	f, err := T.ppt.FileDownload(file)
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
		flag.Set(incomplete)
		T.downloads.Set(fmt.Sprintf("%d", file.ID), &flag)
	}

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

	num, err := io.Copy(dst, f)
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

	T.transfered.Add(num)
	T.files_downloaded.Add(1)
	f.Close()
	dst.Close()

	err = Rename(tmp_file_name, file_name)
	if err != nil {
		return err
	}

	err = os.Chtimes(file_name, time.Now(), mtime)
	if err == nil {
		mark_complete()
	}
	if err == nil {
		flag.Set(complete)
		T.downloads.Set(fmt.Sprintf("%d", file.ID), &flag)
	}
	return
}
