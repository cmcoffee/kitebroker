package main

import (
	"fmt"
	"github.com/cmcoffee/go-logger"
	"io/ioutil"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Task struct {
	task_id string
	session Session
	queue   chan interface{}
}

var loader = []string{
	"[>  ]",
	"[>> ]",
	"[>>>]",
	"[ >>]",
	"[  >]",
	"[   ]",
	"[  <]",
	"[ <<]",
	"[<<<]",
	"[<< ]",
	"[<  ]",
	"[   ]",
}

var show_loader = int32(0)

func init() {
	go func() {
		for {
			for _, str := range loader {
				if atomic.LoadInt32(&show_loader) == 1 {
					if snoop {
						goto Exit
					}
					logger.Put("\r%s Working, Please wait...", str)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	Exit:
	}()
}

// Displays loader. "[>>>] Working, Please wait."
func ShowLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 0, 1)
}

// Hides display loader.
func HideLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 1, 0)
}

var task_time time.Time

// main task handler.
func TaskHandler(users []string) {
	task_time = time.Now()

	task := Config.Get("configuration", "task")

	for _, user := range users {
			var jfunc func() error

			t := &Task{
				task_id: task,
				session: Session(user),
			}

			if local_path := Config.Get(task, "local_path"); local_path != NONE {
				errChk(MkDir(cleanPath(Config.Get(task, "local_path"))))
			}

			switch task {
				case "folder_download":
					jfunc = t.DownloadFolder
				case "folder_upload":
					jfunc = t.UploadFolder
				case "dli_export":
					if auth_flow != SIGNATURE_AUTH {
						errChk(fmt.Errorf("task = dli_report requires 'auth_mode = signature', currently set as 'auth_mode' = %s'.", Config.Get("configuration", "auth_mode")))
					}
					jfunc = t.DLIReport
				default:
					logger.Err("- Error: Unrecognized or missing task of %s.\n", task)
					return
			}

			logger.Log("----------> Starting task %s of %s.", t.task_id, t.session)
			ShowLoader()
			err := jfunc()
			HideLoader()
			if err != nil { 
				logger.Err(err)
			}
		logger.Log("\n")			
	}
}

type file_upload struct {
	LFile    string `json:"file"`
	FolderID int    `json:"folder_id"`
}

type exit struct{}

func (t *Task) UploadFolder() (err error) {
	t.queue = make(chan interface{}, 128)
	var wg sync.WaitGroup
	var files_found bool

	kw_folder_id, err := t.session.FindFolder(Config.Get(t.task_id, "kw_folder"))
	if err != nil {
		return err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for data := range t.queue {
			switch m := data.(type) {
			case file_upload:
				err := t.Upload(m.LFile, m.FolderID)
				if err != nil && err == ErrUploaded {
					continue
				} else if err != nil {
					logger.Err(err)
					continue
				}
				files_found = true
			case exit:
				return
			}
		}
	}()

	err = t.kw_umap(kw_folder_id, cleanPath(Config.Get(t.task_id, "local_path")))

	t.queue <- exit{}
	wg.Wait()

	if !files_found {
		logger.Log("No new files to upload.")
	}

	return
}

// Walks through local folders, creates folders that don't exist on kiteworks and uploads files to them.
func (t *Task) kw_umap(folder_id int, local_path string) (err error) {
	var wg sync.WaitGroup

	// First find all folders in folder_id.
	kw_folders, err := t.session.ListFolders(folder_id)
	if err != nil {
		return err
	}

	// Second generate a map of folders.
	kw_map := make(map[string]int)
	for _, f := range kw_folders.Data {
		kw_map[f.Name] = f.ID
	}

	// Third list everything in folder.
	f_read, err := ioutil.ReadDir(local_path)
	if err != nil {
		return err
	}

	// Range through all elements in folder.
	for _, finfo := range f_read {

		name := finfo.Name()
		u_path := cleanPath(local_path + "/" + name)

		if !finfo.IsDir() {
			t.queue <- file_upload{u_path, folder_id}
		} else {
			if _, found := kw_map[name]; found {
				wg.Add(1)
				go func(folder_id int, u_path string) {
					defer wg.Done()
					err := t.kw_umap(folder_id, u_path)
					if err != nil {
						logger.Err(err)
						return
					}
				}(kw_map[name], u_path)
			} else {
				logger.Log("Creating new kiteworks folder for %s.", strings.Replace(u_path, Config.Get(t.task_id, "local_path"), Config.Get(t.task_id, "kw_folder"), 1))
				new_folder_id, err := t.session.CreateFolder(name, folder_id)
				if err != nil {
					logger.Err(err)
					continue
				}
				wg.Add(1)
				go func(folder_id int, u_path string) {
					defer wg.Done()
					err = t.kw_umap(new_folder_id, u_path)
					if err != nil {
						logger.Err(err)
						return
					}
				}(new_folder_id, u_path)
			}
		}
	}

	wg.Wait()
	return
}

// DownloadFolder task.
func (t *Task) DownloadFolder() (err error) {
	var wg sync.WaitGroup
	var files_found bool

	DB.Truncate("dl_folders")

	local_path := cleanPath(Config.Get(t.task_id, "local_path"))

	err = MkDir(local_path)
	if err != nil {
		return err
	}

	for _, kw_folder := range Config.MGet(t.task_id, "kw_folder") {

		folder_id, err := t.session.FindFolder(kw_folder)
		if err != nil {
			logger.Err(err)
			continue
		}

		start_folder, err := t.session.FolderInfo(folder_id)
		if err != nil { 
			logger.Err(err)
			continue
		}

		var delete_sources bool

		if strings.ToLower(Config.Get(t.task_id, "delete_source_files_on_complete")) == "yes" {
			delete_sources = true
		}

		var save_file_info bool

		if strings.Contains(strings.ToLower(Config.Get(t.task_id, "save_metadata")), "yes") {
			save_file_info = true
		}

		t.queue = make(chan interface{}, 128)

		// File downloader thread.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for data := range t.queue {
				switch m := data.(type) {
					case KiteData:
						files, err := t.session.ListFiles(m.ID)
						if err != nil {
							logger.Err(err)
							continue
						}
						for _, f := range files.Data {
							err = t.Download(f)
							if err != nil && err == ErrDownloaded {
								continue
							} else if err != nil {
								logger.Err(err)
							} else {
								files_found = true
								if save_file_info {
									if err := t.session.MetaData(&f); err != nil { logger.Err(err) }
								}	
								if delete_sources {
									logger.Log("Removing file %s from %s.", f.Name, m.Name)
									if err := t.session.DeleteFile(f.ID); err != nil { logger.Err(err) }
								}
							}
						}
					case exit:
						return
				}
			}
		}()

		// Change how we handle "My Folder" when using signature auth.
		if auth_flow == SIGNATURE_AUTH && strings.Contains(kw_folder, "My Folder") {
			local_path = cleanPath(local_path + "/" + string(t.session) + "/")
			if err := MkDir(local_path); err != nil { return err }
			if err := DB.Set("dl_folders", folder_id, local_path); err != nil { return err }

			t.queue <- start_folder

			folders, err := t.session.ListFolders(folder_id)
			if err != nil { logger.Err(err) }

			for _, kwf := range folders.Data {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err = t.kw_dmap(local_path, kwf); err != nil { logger.Err(err) } 
				}()
			}
		} else {
			if err = t.kw_dmap(local_path, start_folder); err != nil { logger.Err(err) }
		}
		t.queue <- exit{}
		wg.Wait()
	}
	if !files_found {
		logger.Log("No new files to download.")
	}
	return err
}

// Folder crawler, maps all remote folders to destinations on local disk.
func (t *Task) kw_dmap(start_path string, folder_data KiteData) (err error) {
	var wg sync.WaitGroup

	t.queue <- folder_data

	local_path := cleanPath(start_path + "/" + folder_data.Name)

	err = MkDir(local_path)
	if err != nil {
		return err
	} else {
		err := DB.Set("dl_folders", folder_data.ID, local_path)
		if err != nil {
			return err
		}
	}

	folders, err := t.session.ListFolders(folder_data.ID)
	if err != nil {
		return err
	}

	for _, folder := range folders.Data {
		wg.Add(1)
		go func(local_path string, folder KiteData) {
			defer wg.Done()
			if err := t.kw_dmap(local_path, folder); err != nil { logger.Err(err) }
		}(local_path, folder)
	}
	wg.Wait()
	return
}
