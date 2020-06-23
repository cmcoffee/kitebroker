package tasks

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var xo Passport

type FolderDownloadTask struct {
	input struct {
		src        []string
		dst        string
		redownload bool
	}
	loader       *Loader
	crawl_wg     sync.WaitGroup
	dwnld_wg     sync.WaitGroup
	folders      map[int]string
	limiter      chan struct{}
	folder_count Tally
	file_count   Tally
	transfered   Tally
	files        Table
	downloads    Table
	dwnld_chan   chan *download
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
	flag.ArrayVar(&T.input.src, "src", "<kw folder>", "Specify folders or file you wish to download.\n\t  (use multiple --src args for multi-folder/file)")
	flag.Alias(&T.input.src, "src", "s")
	flag.StringVar(&T.input.dst, "dst", "<local destination folder>", "Specify local path to store downloaded folders/files.")
	flag.Alias(&T.input.dst, "dst", "d")
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
func (T *FolderDownloadTask) Main(passport Passport) (err error) {
	xo = passport

	T.files = xo.Sub(xo.Username).Table("files")
	T.downloads = xo.Sub(xo.Username).Table("downloads")

	var into_root bool
	if !strings.HasSuffix(T.input.dst, SLASH) && len(T.input.src) == 1 {
		into_root = true
	}

	T.input.dst, err = filepath.Abs(T.input.dst)
	if err != nil {
		return err
	}

	T.folder_count = xo.Tally("Folders Processed")
	T.file_count = xo.Tally("Files Processed")
	T.transfered = xo.Tally("Transfered", HumanSize)

	T.loader = PleaseWait

	message := func() string {
		return fmt.Sprintf("Please wait ... [files: %d/folders: %d]", T.file_count.Value(), T.folder_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()
	PleaseWait = T.loader

	T.limiter = make(chan struct{}, 20)

	var folders []KiteObject

	for _, f := range T.input.src {
		folder, err := xo.FindFolder(0, f)
		if err != nil {
			Err("%s: %v", f, err)
			continue
		}
		folders = append(folders, folder)
	}

	if T.input.src == nil {
		folders, err = xo.TopFolders()
		if err != nil {
			return err
		}
	}

	err = MkDir(T.input.dst)
	if err != nil {
		return err
	}

	T.dwnld_chan = make(chan *download, 0)

	T.dwnld_wg.Add(1)
	go func() {
		limiter := make(chan struct{}, xo.GetTransferLimit())
		defer T.dwnld_wg.Done()
		for {
			m := <-T.dwnld_chan
			if m == nil {
				break
			}
			T.dwnld_wg.Add(1)
			limiter <- struct{}{}
			go func(m *download) {
				defer T.dwnld_wg.Done()
				if err := T.ProcessFile(m.path, m.file); err != nil {
					Err("%s: %v", m.file.Name, err)
				}
				<-limiter
			}(m)
		}
	}()

	for i, _ := range folders {
		T.folder_count.Add(1)
		T.crawl_wg.Add(1)
		T.limiter <- struct{}{}

		if into_root {
			folders[i].Name = NONE
		}

		go func(path string, folder *KiteObject) {
			if err := T.ProcessFolder(path, folder); err != nil {
				Err("%s: %s", folder.Path, err.Error())
			}
			<-T.limiter
			T.crawl_wg.Done()
		}(T.input.dst, &folders[i])
	}

	T.crawl_wg.Wait()

	// Shutdown downloader.
	T.dwnld_chan <- nil
	T.dwnld_wg.Wait()

	return nil
}

func (T *FolderDownloadTask) ProcessFolder(local_path string, folder *KiteObject) (err error) {
	type child struct {
		path string
		*KiteObject
	}

	var folders []child
	folders = append(folders, child{local_path, folder})

	var n int
	var next []child

	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					select {
					case T.limiter <- struct{}{}:
						T.crawl_wg.Add(1)
						go func(path string, obj *KiteObject) {
							if err := T.ProcessFolder(path, obj); err != nil {
								Err(fmt.Sprintf("%s: %s", o.KiteObject.Path, err.Error()))
							}
							<-T.limiter
							T.crawl_wg.Done()
						}(o.path, o.KiteObject)
					default:
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
			if obj.Secure {
				folder, err := xo.FolderInfo(obj.ID)
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
			err = MkDir(CombinePath(obj.path, obj.Name))
			if err != nil {
				Err("%s: %v", obj.Path, err)
				n++
				continue
			}
			objs, err := xo.FolderContents(obj.ID)
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
	return nil
}

const (
	incomplete = 1 << iota
	complete
)

func (T *FolderDownloadTask) ProcessFile(path string, file *KiteObject) (err error) {

	var flag BitFlag

	T.file_count.Add(1)

	found := T.downloads.Get(fmt.Sprintf("%d", file.ID), &flag)

	if !T.input.redownload && found && flag.Has(complete) {
		return nil
	}

	mtime, err := ReadKWTime(file.ClientModified)
	if err != nil {
		return
	}

	file_name := CombinePath(path, file.Name)
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

	f, err := xo.FileDownload(file)
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
	T.loader.Hide()

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
		os.Remove(tmp_file_name)
		if file.AdminQuarantineStatus != "allowed" {
			Notice("%s/%s: Cannot be downloaded, file is under administrator quarantine.", strings.TrimSuffix(path, SLASH), file.Name)
			return nil
		}
		if file.AVStatus != "allowed" {
			Notice("%s/%s: Cannot be downloaded, anti-virus status is currently set to: %s", strings.TrimSuffix(path, SLASH), file.Name, file.AVStatus)
			return nil
		}
		if file.DLPStatus != "allowed" {
			Notice("%s/%s: Cannot be downloaded, dli status is currently set to: %s", strings.TrimSuffix(path, SLASH), file.Name, file.DLPStatus)
			return nil
		}
		return err
	}
	T.transfered.Add(num)
	f.Close()
	dst.Close()

	err = Rename(tmp_file_name, file_name)
	if err != nil {
		return err
	}

	flag.Set(complete)
	T.downloads.Set(fmt.Sprintf("%d", file.ID), &flag)

	err = os.Chtimes(file_name, time.Now(), mtime)
	if err == nil {
		mark_complete()
	}
	return
}
