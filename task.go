package main

import (
	"fmt"
	"github.com/cmcoffee/go-logger"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var cleanup_working int32
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
			if !elem.IsDir() {
				continue
			}
			if isEmail(elem.Name()) {
				users = append(users, elem.Name())
			}
		}

		switch task {
		case "send_file":
			str := users[:0]
			for _, v := range users {
				if v == my_user { continue }
				str = append(str, v)
			}
			users = str
		case "recv_file":
			users = []string{my_user}
		case "folder_download":
			fallthrough
		case "folder_upload":
			if auth_flow != SIGNATURE_AUTH {
				users = []string{my_user}
			}
		}

		if len(users) == 0 {
			users = []string{my_user}
		}

		return
	}

	users = load_users(task)

	for _, user := range users {
		var jfunc func() error

		s := Session(user)

		prep := "for"

		switch task {
		case "send_file":
			prep = "to"
			jfunc = s.SendFile
		case "recv_file":
			jfunc = s.RecvFile
		case "folder_download":
			jfunc = s.DownloadFolder
		case "folder_upload":
			jfunc = s.UploadFolder
		case "dli_export":
			prep = "of"
			jfunc = s.DLIReport
		default:
			logger.Err("- Error: Unrecognized or missing task of %s.\n", task)
			return
		}

		logger.Log("----------> Starting task %s %s %s.", Config.Get("configuration", "task"), prep, s)

		var err error

		ShowLoader()
		for {
			err = jfunc()
			HideLoader()
			if s.RetryToken(err) {
				continue
			}
			break
		}
		HideLoader()
		if err != nil {
			if err == NoValidToken {
				logger.Fatal(err)
			}
			logger.Err(err)
		}
		logger.Log("\n")
	}
}

// Background clean up task for maintaining DB.
func backgroundCleanup() {
	if snoop {
		return
	}

	secs, err := strconv.Atoi(Config.Get("configuration", "cleanup_time_secs"))
	if err != nil {
		logger.Warn("Could not parse cleanup_time_secs, defaulting to 86400 seconds. (1 day)")
		secs = 86400
	}

	ival := time.Second * time.Duration(secs)

	var last_cleanup time.Time
	_, err = DB.Get("kitebroker", "last_cleanup", &last_cleanup)
	if err != nil {
		errChk(err)
	}

	go func() {
		for {
			var err error
			if time.Since(last_cleanup) >= ival {
				atomic.CompareAndSwapInt32(&cleanup_working, 0, 1)
				switch Config.Get("configuration", "task") {
					case "recv_file":
						if err = cleanupRecv(); err != nil {
							logger.Debug(fmt.Sprintf("cleanup error: %s", err.Error()))
						}
					case "folder_download":
						if err = cleanupDownloads(); err != nil {
							logger.Debug(fmt.Sprintf("cleanup error: %s", err.Error()))
						}
					case "folder_upload":
						if err = cleanupLocal("uploads"); err != nil {
							logger.Debug(fmt.Sprintf("cleanup: %s", err.Error()))
						}
						if err = cleanupLocal("folders"); err != nil {
							logger.Debug(fmt.Sprintf("cleanup: %s", err.Error()))
						}
				}
				atomic.CompareAndSwapInt32(&cleanup_working, 1, 0)
				if err != nil {
					logger.Debug("cleanup process error: %s", err.Error())
					time.Sleep(time.Minute)
					continue
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

func cleanupRecv() error {
	records, err := DB.ListNKeys("inbox")
	if err != nil {
		return err
	}

	for _, key := range records {

		var s Session
		if _, err = DB.Get("inbox", key, &s); err != nil {
			logger.Debug("cleanup process error: %s", err.Error())
			continue
		}

		if IsDeleted(s, fmt.Sprintf("/rest/mail/%d", key)) {
			DB.Unset("inbox", key)
		}
	}
	return nil
}

func IsDeleted(user Session, path string) bool {
	var auth *KiteAuth
	found, _ := DB.Get("tokens", user, &auth)
	if !found { 
		return false
	}

	var M struct {
		Deleted bool `json:"deleted"`
	}

	var err error

	for {
		err = user.Call("GET", path, &M, Query{"mode":"compact", "with":"(deleted)"})
			if user.RetryToken(err) {
				continue
			}
		break
	}

	if err != nil {
		if KiteError(err, ERR_ENTITY_NOT_FOUND|ERR_ENTITY_DELETED_PERMANENTLY) {
			return true
		}
		logger.Debug(err)
		return false
	}
	return M.Deleted
}

// Cleans up upload records, make sure files are there, if not delete record.
func cleanupLocal(table string) error {
	records, err := DB.ListKeys(table)
	if err != nil {
		return err
	}
	for _, key := range records {
		if _, err := Stat(key); os.IsNotExist(err) {
			DB.Unset(table, key)
		}
	}
	return nil
}

func cleanupDownloads() error {
	records, err := DB.ListNKeys("downloads")
	if err != nil {
		return err
	}

	var dl_record FileRecord

	for _, key := range records {
		found, err := DB.Get("downloads", key, &dl_record)
		if err != nil {
			logger.Debug("cleanup process error: %s", err.Error())
			continue
		}

		if !found {
			continue
		}

		if IsDeleted(dl_record.User, fmt.Sprintf("/rest/files/%d", key)) {
			DB.Unset("downloads", key)
		}
	}
	return nil
}

type file_upload struct {
	LFile    string `json:"file"`
	FolderID int    `json:"folder_id"`
}

type exit struct{}

// Performs upload folder task.
func (s Session) UploadFolder() (err error) {

	show_no_files_found := true
	sync_map := make(map[string]int)

	top_folders, err := s.GetFolders()
	if err != nil {
		return err
	}

	for _, f := range top_folders.Data {
		sync_map[f.Name] = f.ID
	}

	sync_folders := Config.MGet("configuration", "kw_folder")
	if len(sync_folders) == 1 && sync_folders[0] == NONE {
		start_path := Config.Get("configuration", "local_path")

		if auth_flow == SIGNATURE_AUTH {
			start_path = start_path + SLASH + string(s)
		}

		if err = MkDir(start_path); err != nil { return err }

		rdir, err := ioutil.ReadDir(start_path)
		if err != nil {
			return err
		}
		for _, finfo := range rdir {
			fname := finfo.Name()
			if finfo.IsDir() {
				sync_folders = append(sync_folders, fname)
			}
		}
	}

	sync_folders = cleanSlice(sync_folders)

	for _, parent_folder := range sync_folders {

		var root_folder string

		if auth_flow == SIGNATURE_AUTH {
			root_folder = string(s) + SLASH + parent_folder
		} else {
			root_folder = parent_folder
		}

		folders, files := scanPath(root_folder)

		if strings.ToLower(Config.Get("folder_upload:opts", "create_empty_folders")) == "yes" {
			for _, folder := range folders {
				_, err = s.getKWDestination(folder, false)
				if err != nil {
					logger.Err(err)
					continue
				}
			}
		}

		if found, err := s.pushFiles(files); err != nil {
			show_no_files_found = false
			logger.Err(err)
			continue
		} else if found {
			show_no_files_found = false
		}

	}

	if show_no_files_found {
		logger.Log("No new files to upload.")
	}

	return nil
}

// Processes files for uploading.
func (s Session) pushFiles(files []string) (files_uploaded bool, err error) {
	var record *UploadRecord

	for _, file := range files {
		found, err := DB.Get("uploads", file, &record)
		if err != nil {
			return true, err
		}
		if found && checkFile(file, record) {
			continue
		} else {
			if fstat, err := Stat(file); err == nil {
				if fstat.Size() == 0 {
					continue
				}
			} else {
				return true, err
			}

		}

		path := splitLast(file, SLASH)[0]

		fid, err := s.getKWDestination(path, true)
		if err != nil {
			return true, err
		}

		if _, err := s.Upload(file, fid); err != nil {
			if err != ErrUploaded && err != ErrNotReady {
				files_uploaded = true
				return files_uploaded, err
			}
		} else {
			files_uploaded = true
		}
	}
	return
}

type set struct{}

// Verifies kiteworks folder exists for file upload, creates folder if one does not exist.
func (s Session) getKWDestination(search_path string, verify bool) (fid int, err error) {
	split_path := strings.Split(search_path, SLASH)
	split_len := len(split_path)

	var missing int

	fid = -1
	folder_id := -1

	for i := split_len; i >= 1; i-- {
		found, err := DB.Get("folders", strings.Join(split_path[0:i], SLASH), &folder_id)
		if err != nil {
			return -1, err
		}
		if found {
			if missing == 0 && verify == false {
				return folder_id, nil
			}
			fid = folder_id
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
		fid, err = s.MyBaseDirID()
		if err != nil {
			return -1, err
		}
	}

	for i := split_len - missing; i < split_len; i++ {
		if i == 0 && split_path[i] == string(s) {
			continue
		}
		missing_folder := split_path[i]
		new_path := strings.Join(split_path[0:i+1], SLASH)
		cid, _ := s.FindChildFolder(fid, missing_folder)
		if cid == -1 {
			logger.Log("Creating new kiteworks folder: [%s]", strings.TrimPrefix(new_path, string(s) + SLASH))
			fid, err = s.CreateFolder(fid, missing_folder)
			if err != nil {
				return -1, err
			}
		} else {
			fid = cid
		}
		err = DB.Set("folders", new_path, fid)
	}
	return
}

type download_task struct {
	kw_file    KiteData
	path string
}

// DownloadFolder task.
func (s Session) DownloadFolder() (err error) {
	var files_found bool

	queue := make(chan (interface{}), 0)

	go func() {
		var halt bool
		for e := range queue {
			switch msg := e.(type) {
			case download_task:
				if err := s.Download(msg.kw_file, msg.path); err != nil && err != ErrDownloaded {
					logger.Err(err)
					files_found = true
				} else if err == nil {
					files_found = true
				}
			case exit:
				halt = true
			}
			if halt {
				break
			}
		}
	}()

	sync_map := make(map[string]int)

	sync_folders := cleanSlice(Config.MGet("configuration", "kw_folder"))
	if len(sync_folders) == 0 {
		kw_folders, err := s.GetFolders()
		if err != nil {
			return err
		}
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

		var root_folder string

		if auth_flow != SIGNATURE_AUTH {
			root_folder = kw_folder
		} else {
			root_folder = string(s) + SLASH + kw_folder
		}

		if err := s.mapFolders(root_folder, folder_id, queue); err != nil {
			return err
		}
	}

	queue <- exit{}

	if !files_found {
		logger.Log("No new files found.")
	}

	return
}

// Creates folders locally, add to to kw_folders.
func (s Session) mapFolders(path string, folder_id int, queue chan interface{}) (err error) {
	var vg sync.WaitGroup

	err = MkPath(path)
	if err != nil {
		return err
	}

	files, err := s.ListFiles(folder_id)
	if err != nil {
		logger.Err(err)
	}

	for _, file := range files.Data {
		queue <- download_task{file, path}
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
		}(path+SLASH+folder.Name, folder.ID, queue)
	}
	vg.Wait()
	return
}
