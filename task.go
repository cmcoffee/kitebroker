package main

import (
	"fmt"
	"github.com/cmcoffee/go-logger"
	"strings"
	"time"
	"os"
	"strconv"
	"sync"
	"io/ioutil"
)

var task_time time.Time
var users []string

// main task handler.
func TaskHandler() {
	task_time = time.Now()

	task := Config.Get("configuration", "task")

	load_users := func(task string) (users []string) {
		data, err := ioutil.ReadDir(Config.Get("configuration", "local_path"))
		errChk(err)

		my_user := Config.Get("configuration", "account")

		users = make([]string, 0)

		for _, elem := range data {
			if !elem.IsDir() { continue }
			if strings.ContainsRune(elem.Name(), '@') {
				users = append(users, elem.Name())
			}
		}

		switch task {
			case "folder_download":
				fallthrough
			case "folder_upload":
				if len(users) == 1 || auth_flow != SIGNATURE_AUTH { users = []string{my_user} }
		}

		if len(users) == 0 { users = []string{my_user}}

		return
	}

	users = load_users(task)

	for _, user := range users {
			var jfunc func() error

			s := Session(user)

			if local_path := Config.Get(task, "local_path"); local_path != NONE {
				errChk(MkDir(cleanPath(Config.Get(task, "local_path"))))
			}

			switch task {
				case "folder_download":
					jfunc = s.DownloadFolder
				case "folder_upload":
					jfunc = s.UploadFolder
				case "dli_export":
					jfunc = s.DLIReport
				default:
					logger.Err("- Error: Unrecognized or missing task of %s.\n", task)
					return
			}

			logger.Log("----------> Starting task %s for %s.", Config.Get("configuration", "task"), s)
			ShowLoader()
			err := jfunc()
			HideLoader()
			if err != nil { 
				logger.Err(err)
			}
		logger.Log("\n")			
	}
}

// Background clean up task for maintaining DB.
func backgroundCleanup() {
	if snoop { return }
	ival, err := time.ParseDuration(Config.Get("configuration", "cleanup_interval"))
	errChk(err)

	var last_cleanup time.Time
	found, err := DB.Get("kitebroker", "last_cleanup", &last_cleanup)
	if err != nil {
		errChk(err)
	}

	if !found {
		if err := DB.Set("kitebroker", "last_cleanup", time.Now()); err != nil {
			errChk(err)
		}
	}

	go func() {
		for {
			if time.Since(last_cleanup) >= ival {
				if err := cleanupLocal("local_files"); err != nil {
					logger.Err(fmt.Sprintf("cleanup: %s", err.Error()))
				}
				if err := cleanupDownloads(); err != nil {
					logger.Err(fmt.Sprintf("cleanup: %s", err.Error()))
				}
				last_cleanup = time.Now()
				if err := DB.Set("kitebroker", "last_cleanup", last_cleanup); err != nil {
					errChk(fmt.Errorf("cleanup: %s", err.Error()))
				}
			}
			// Put thread to sleep until it's time to perform cleanup.
			time.Sleep(last_cleanup.Add(ival).Sub(time.Now()))
		}
	}()
}

// Cleans up upload records, make sure files are there, if not delete record.
func cleanupLocal(table string) (error) {
	records, err := DB.ListKeys(table)
	if err != nil {
		return err
	}
	for _, key := range records {
		if _, err := os.Stat(key); os.IsNotExist(err) {
			if err := DB.Unset(table, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func cleanupDownloads() (error) {
	records, err := DB.ListKeys("kw_files")
	if err != nil {
		return err
	}
	var dl_record FileRecord

	for _, key := range records {
		found, err := DB.Get("kw_files", key, &dl_record)
		if err != nil { return err }
		if !found { continue }
		file_id, err := strconv.Atoi(key)
		if err != nil { return err }
		data, err := dl_record.User.FileInfo(file_id)
		if data.Deleted {
			if err := DB.Unset("kw_files", key); err != nil { return err }
		}
	}
	return nil
}


type file_upload struct {
	LFile    string `json:"file"`
	FolderID int    `json:"folder_id"`
}

type exit struct{}

func (s Session) UploadFolder() (err error) {

	show_no_files_found := true
	sync_map := make(map[string]int)

	top_folders, err := s.GetFolders()
	if err != nil { return err }

	for _, f := range top_folders.Data {
		sync_map[f.Name] = f.ID
	}

	sync_folders := Config.MGet("configuration", "kw_folder")
	if len(sync_folders) == 1 && sync_folders[0] == NONE {
		switch auth_flow {
			case SIGNATURE_AUTH:
				for _, f := range top_folders.Data {
					if strings.ContainsRune(f.Name, '@') { continue }
					sync_folders = append(sync_folders, f.Name)
				}
			default:
				rdir, err := ioutil.ReadDir(Config.Get("configuration", "local_path"))
				if err != nil { return err }
				for _, finfo := range rdir {
					fname := finfo.Name()
					if finfo.IsDir() && !strings.ContainsRune(fname, '@') { sync_folders = append(sync_folders, fname) }
				}
		}

	} 

	sync_folders = cleanSlice(sync_folders)

 
	for _, parent_folder := range sync_folders {

		var folder_id int

		if fid, found := sync_map[parent_folder]; found {
			folder_id = fid
		} else {
			folder_id, err = s.FindFolder(parent_folder)
			if err != nil {
				if auth_flow == SIGNATURE_AUTH && len(users) == 1 {
					show_no_files_found = false
					logger.Err(err)
					continue
				}
			} 
		}

		if folder_id == -1 {
			base_id, err := s.MyBaseDirID()
			if err != nil {
				logger.Err(err)
				continue
			}
			logger.Log("Creating new kiteworks folder: [%s]", parent_folder)
			if folder_id, err = s.CreateFolder(parent_folder, base_id); err != nil {
				logger.Err(err)
				continue
			}
			DB.Set("folders", parent_folder, &folder_id)
		}

		var root_folder string

		if strings.Contains(parent_folder, "My Folder") && auth_flow == SIGNATURE_AUTH {
			root_folder = Config.Get("configuration", "local_path") + SLASH + string(s) + strings.TrimPrefix(parent_folder, "My Folder")
		} else {
			root_folder = Config.Get("configuration", "local_path")	+ SLASH + parent_folder
		}

		folders, files := scanPath(root_folder)

		for _, folder := range folders {
			_, err = s.getKWDestination(StripLocalPath(folder), false)
			if err != nil {
				logger.Err(err)
				continue
			}
		}

		if found, err := s.pushFiles(folder_id, files); err != nil { 
			show_no_files_found = false
			logger.Err(err)
			continue
		} else if found {
			show_no_files_found = false
		}

	}

	if show_no_files_found {
		logger.Log("No new files to uplaod.")
	}

	return nil
}

func (s Session) pushFiles(parent_id int, files []string) (files_uploaded bool, err error) {
	var record UploadRecord
	for _, file := range files {
		file = StripLocalPath(file)
		found, err := DB.Get("uploads", file, &record)
		if err != nil { return true, err }
		if !found {
			f_path := splitLast(file, SLASH)
			path := f_path[0]

			var fid int

			if len(path) == 0 {
				fid = parent_id
			} else {
				fid, err = s.getKWDestination(StripLocalPath(path), true)
				if err != nil { return true, err }
			}
			file = AppendLocalPath(file)
			if _, err := s.Upload(file, fid); err != nil {
				if err != ErrUploaded {
					files_uploaded = true
					return files_uploaded, err
				}
			} else {
				files_uploaded = true
			}
		}
	}
	return
}

type set struct{}

func (s Session) getKWDestination(search_path string, verify bool) (fid int, err error) {
	split_path := strings.Split(search_path, SLASH)
	split_len := len(split_path)

	var missing int

	fid = -1
	folder_id := -1

	for i := split_len; i >= 1; i-- {
		found, err := DB.Get("folders", strings.Join(split_path[0:i], SLASH), &folder_id)
		if err != nil { return -1, err }
		if found {
			fid = folder_id
			if !verify && missing == 0 { break }
			finfo, err := s.FolderInfo(fid)
			if err != nil || finfo.Deleted {
				missing++
			} else {
				break
			}
		} else {
			missing++
		}
	}

	if fid == -1 {
		var first_folder string
		if split_path[0] == string(s) {
			first_folder = "My Folder"
		} else {
			first_folder = split_path[0]
		}
		fid, err = s.FindFolder(first_folder)
		if err != nil { return -1, err }
	}

	for i := missing; i > 0; i-- {
		missing_folder := split_path[split_len-i]
		if missing_folder == string(s) {
			missing_folder = "My Folder"
		}
		new_path := strings.Join(split_path[0:split_len+1-i], SLASH)
		cid, _ := s.FindChildFolder(missing_folder, fid)
		if cid == -1 {
			logger.Log("Creating new kiteworks folder: [%s]", strings.Replace(new_path, string(s), "My Folder", 1))
			fid, err = s.CreateFolder(missing_folder, fid)
			if err != nil { return -1, err }
		} else {
			fid = cid
		}
		err = DB.Set("folders", new_path, fid)
	}
	return
}

type download_task struct {
	kw_file KiteData
	local_path string
}

// DownloadFolder task.
func (s Session) DownloadFolder() (err error) {
	var files_found bool

	queue := make(chan(interface{}), 0)

	local_path := LocalPath()

	err = MkDir(local_path)
	if err != nil {
		return err
	}

	go func() {
		var halt bool
		for e := range queue {
			switch msg := e.(type) {
				case download_task:
					if err := s.Download(msg.kw_file, msg.local_path); err != nil && err != ErrDownloaded {
						logger.Err(err) 
						files_found = true
					} else if err == nil {
						files_found = true
					}
				case exit:
					halt = true
			}
			if halt { break }
		}
	}()

	sync_map := make(map[string]int)

	sync_folders := cleanSlice(Config.MGet("configuration", "kw_folder"))
	if len(sync_folders) == 0 {
		kw_folders, err := s.GetFolders()
		if err != nil { return err }
		for _, f := range kw_folders.Data {
			sync_folders = append(sync_folders, f.Name)
			sync_map[f.Name] = f.ID
		}
	}

	var folder_id int

	for _, kw_folder := range sync_folders {

		if fid, found := sync_map[kw_folder]; found {
			folder_id = fid
		} else {
			folder_id, err = s.FindFolder(kw_folder)
			if err != nil {
				if len(users) == 1 { 
					logger.Err(err)
					files_found = true
				}
				continue
			}
		}

		if kw_folder == "My Folder" && auth_flow == SIGNATURE_AUTH { kw_folder = string(s) }

		root_folder := cleanPath(local_path + SLASH + kw_folder)
		if err := s.mapFolders(root_folder, folder_id, queue); err != nil { return err }
	}

	queue<-exit{}

	if !files_found {
		logger.Log("No new files found.")
	}

	return
}

// Creates folders locally, add to to kw_folders.
func (s Session) mapFolders(local_path string, folder_id int, queue chan interface{}) (err error) {
	var vg sync.WaitGroup

	err = MkDir(local_path)
	if err != nil {
		return err
	}

	files, err := s.ListFiles(folder_id)
	if err != nil {
		logger.Err(err)
	}

	for _, file := range files.Data {
		queue<-download_task{file, local_path}
	}

 	folders, err := s.ListFolders(folder_id)
	if err != nil { 
		logger.Err(err)
	}

	for _, folder := range folders.Data {
		vg.Add(1)
		go func(folder_name string, folder_id int, queue chan interface{}) {
			err := s.mapFolders(folder_name, folder_id, queue)
			if err != nil {
				logger.Err(err)
			}
			vg.Done()
		}(local_path, folder.ID, queue)
	}
	vg.Wait()
	return
}