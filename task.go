package main

import (
	"fmt"
	"github.com/cmcoffee/go-nfo"
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

		// target users will be email subdirectories found under local_path.
		if task == "send_file" || task == "dli_export" {
			str := users[:0]
			for _, v := range users {
				str = append(str, v)
			}
			users = str
		} else {
			users = []string{my_user}
		}

		return
	}

	if users = load_users(task); len(users) == 0 {
		nfo.Notice("No valid user subfolders (ie.. %s) found, task %s aborted.", FullPath("user@domain.com"), task)
		nfo.Log("\n")
	}

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
		case "csv_onboarding":
			jfunc = s.CSVOnboard
		default:
			nfo.Err("- Error: Unrecognized or missing task of %s.\n", task)
			return
		}

		nfo.Log("----------> Starting task %s %s %s.", Config.Get("configuration", "task"), prep, s)

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
			nfo.Err(err)
		}
		nfo.Log("\n")
	}
}

// Background clean up task for maintaining DB.
func backgroundCleanup() {
	if snoop {
		return
	}

	secs, err := strconv.Atoi(Config.Get("configuration", "cleanup_time_secs"))
	if err != nil {
		nfo.Warn("Could not parse cleanup_time_secs, defaulting to 86400 seconds. (1 day)")
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
						nfo.Debug(fmt.Sprintf("cleanup error: %s", err.Error()))
					}
				case "folder_download":
					if err = cleanupDownloads(); err != nil {
						nfo.Debug(fmt.Sprintf("cleanup error: %s", err.Error()))
					}
				case "folder_upload":
					if err = cleanupLocal("uploads"); err != nil {
						nfo.Debug(fmt.Sprintf("cleanup: %s", err.Error()))
					}
					if err = cleanupLocal("folders"); err != nil {
						nfo.Debug(fmt.Sprintf("cleanup: %s", err.Error()))
					}
				}
				atomic.CompareAndSwapInt32(&cleanup_working, 1, 0)
				if err != nil {
					nfo.Debug("cleanup process error: %s", err.Error())
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

// Cleanup received emails.
func cleanupRecv() error {
	records, err := DB.ListNKeys("inbox")
	if err != nil {
		return err
	}

	for _, key := range records {

		var s Session
		if _, err = DB.Get("inbox", key, &s); err != nil {
			nfo.Debug("cleanup process error: %s", err.Error())
			continue
		}

		if IsDeleted(s, fmt.Sprintf("/rest/mail/%d", key)) {
			DB.Unset("inbox", key)
		}
	}
	return nil
}

// Checks if remote file is deleted.
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
		err = user.Call("GET", path, &M, Query{"mode": "compact", "with": "(deleted)"})
		if user.RetryToken(err) {
			continue
		}
		break
	}

	if err != nil {
		if KiteError(err, ERR_ENTITY_NOT_FOUND|ERR_ENTITY_DELETED_PERMANENTLY) {
			return true
		}
		nfo.Debug(err)
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

// Displays files that will be skipped in scan.
func show_skipped_files() {
	rdir, err := ioutil.ReadDir(FullPath(NONE))
	if err != nil {
		return
	}
	var bad_files []string
	for _, finfo := range rdir {
		fname := finfo.Name()
		if !finfo.IsDir() {
			bad_files = append(bad_files, fname)
		}
	}
	if len(bad_files) > 0 {
		for _, name := range bad_files {
			nfo.Notice("skipped %s: file not in a kiteworks subfolder under local_path.", name)
		}
	}
}

// Cleanup old download records that are no longer relevant.
func cleanupDownloads() error {
	records, err := DB.ListNKeys("downloads")
	if err != nil {
		return err
	}

	var dl_record FileRecord

	for _, key := range records {
		found, err := DB.Get("downloads", key, &dl_record)
		if err != nil {
			nfo.Debug("cleanup process error: %s", err.Error())
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

	// get folders which we have been told to handle on kw.
	sync_folders := Config.MGet("configuration", "kw_folder_filter")
	if len(sync_folders) == 1 && sync_folders[0] == NONE {
		if err = MkDir(local_path); err != nil {
			return err
		}

		rdir, err := ioutil.ReadDir(local_path)
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

	show_skipped_files()

	sync_folders = cleanSlice(sync_folders)

	for _, parent_folder := range sync_folders {
		_, err := Stat(parent_folder)
		if err != nil {
			nfo.Err(err)
			continue
		}

		folders, files := scanPath(parent_folder)
		nfo.Log("- %s (Total Folders: %d, Total Files: %d)", parent_folder, len(folders), len(files))

		if strings.ToLower(Config.Get("folder_upload:opts", "create_empty_folders")) == "yes" {
			for _, folder := range folders {
				_, err = s.getKWDestination(folder, false)
				if err != nil {
					nfo.Err("%s: Scan failure, %s.", folder, err)
					continue
				}
			}
		}

		if found, err := s.pushFiles(files); err != nil {
			show_no_files_found = false
			nfo.Err(err)
			continue
		} else if found {
			show_no_files_found = false
		}
	}

	if show_no_files_found {
		nfo.Log("No new files to upload.")
	}

	return nil
}

// Processes files for uploading.
func (s Session) pushFiles(files []string) (files_uploaded bool, err error) {
	var record *UploadRecord

	skip_zero_byte := func() bool {
		t := strings.ToLower(Config.Get("folder_upload:opts", "skip_empty_files"))
		if t == "yes" || t == "true" {
			return true
		} else {
			return false
		}
	}()

	for _, file := range files {
		// Ignore incomplete files
		if strings.HasSuffix(file, ".incomplete") {
			continue
		}
		found, err := DB.Get("uploads", file, &record)
		if err != nil {
			return true, err
		}

		if found && checkFile(file, record) && record.Flag == DONE {
			continue
		} else {
			if fstat, err := Stat(file); err != nil {
				return true, err
			} else {
				if fstat.Size() == 0 && skip_zero_byte {
					continue
				}
			}
		}

		path := splitLast(file, SLASH)[0]

		if path == NONE {
			continue
		}

		fid, err := s.getKWDestination(path, true)
		if err != nil {
			return true, err
		}

		if _, err := s.Upload(file, fid); err != nil {
			if err != ErrUploaded && err != ErrNotReady {
				files_uploaded = true
				nfo.Err("%s: %s.", file, err)
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
				if KiteError(err, ERR_ENTITY_DELETED_PERMANENTLY) {
					DB.Unset("folders", strings.Join(split_path[0:i], SLASH))
				}
				missing++
			} else {
				break
			}
		} else {
			missing++
		}
	}

	var no_search bool

	if fid == -1 {
		// Search for folder initially.
		no_search = true
		fid, _ = s.FindFolder(split_path[0])

		// If we didn't find it, we're going to make a new folder.
		if fid == -1 {
			no_search = false
			fid, err = s.MyBaseDirID()
			if err != nil {
				return -1, err
			}
		}
	}

	for i := split_len - missing; i < split_len; i++ {
		var cid int

		if i == 0 && split_path[i] == string(s) {
			continue
		}
		missing_folder := split_path[i]
		new_path := strings.Join(split_path[0:i+1], SLASH)

		if !no_search {
			cid, err = s.FindChildFolder(fid, missing_folder)
		} else {
			cid = fid
			no_search = false
		}

		if cid == -1 {
			nfo.Log("Creating kiteworks folder: %s", strings.TrimPrefix(new_path, string(s)+SLASH))
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
	kw_file KiteData
	path    string
}

type download_list struct {
	mutex sync.Mutex
	list []download_task
}
func (d *download_list) AddFile(file download_task) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.list = append(d.list, file)
}

// DownloadFolder task.
func (s Session) DownloadFolder() (err error) {
	var files_found bool
	var f_list download_list

	queue := make(chan (interface{}), 0)
	defer func() { queue <- exit{} }()

	// Downloader background function.
	go func() {
		var halt bool
		for e := range queue {
			switch msg := e.(type) {
			case download_task:
				files_found = true
				if err := s.Download(msg.kw_file, msg.path); err != nil && err != ErrDownloaded {
					nfo.Err("%s: %s.", fmt.Sprintf("%s/%s", msg.path, msg.kw_file.Name), err)
				} 
			case exit:
				halt = true
			}
			if halt {
				break
			}
		}
	}()

	// Establish which folders we will be downloading.
	sync_map := make(map[string]int)
	sync_folders := cleanSlice(Config.MGet("configuration", "kw_folder_filter"))

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
		//nfo.Log("Processing folder: %s", kw_folder)

		if fid, found := sync_map[kw_folder]; found {
			folder_id = fid
		} else {
			folder_id, err = s.FindFolder(kw_folder)
			if err != nil {
				if len(users) == 1 {
					nfo.Err(err)
					files_found = true
				}
				continue
			}
		}

		if err := s.mapFolders(kw_folder, folder_id, &f_list); err != nil {
			return err
		}
	}

	for _, task := range f_list.list {
		if err := s.Download(task.kw_file, task.path); err != nil && err != ErrDownloaded {
			nfo.Err("%s: %s.", fmt.Sprintf("%s/%s", task.path, task.kw_file.Name), err)
		} else if err == nil {
			files_found = true
		}
	}

	if !files_found {
		nfo.Log("\nNo new files found.")
	}

	return
}

// Creates folders locally, add to to kw_folders.
func (s Session) mapFolders(path string, folder_id int, f_list *download_list) (err error) {
	var vg sync.WaitGroup

	err = MkPath(path)
	if err != nil {
		return err
	}

	files, err := s.ListFiles(folder_id)
	if err != nil {
		nfo.Err(err)
	}

	for _, file := range files.Data {
		f_list.AddFile(download_task{file, path})
	}

	folders, err := s.ListFolders(folder_id)
	if err != nil {
		return err
	}

	nfo.Log("- %s (Nested Folders: %d Files: %d)", path, len(folders.Data), len(files.Data))

	for _, folder := range folders.Data {
		if !snoop {
		vg.Add(1)
			go func(folder_name string, folder_id int, f_list *download_list) {
				err := s.mapFolders(folder_name, folder_id, f_list)
				if err != nil {
					nfo.Err("%s: Scan fail, %s.", folder_name, err)
				}
				vg.Done()
			}(path+SLASH+folder.Name, folder.ID, f_list)
		} else {
			err := s.mapFolders(path+SLASH+folder.Name, folder.ID, f_list)
			if err != nil {
				nfo.Err("%s: Scan fail, %s.", path+SLASH+folder.Name, err)
			}
		}
	}
	vg.Wait()
	return
}
