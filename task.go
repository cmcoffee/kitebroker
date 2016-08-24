package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync/atomic"
	"time"
)

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
					fmt.Printf("\r%s Working, Please wait. ", str)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func ShowLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 0, 1)
	fmt.Printf("\r")
}

func HideLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 1, 0)
	fmt.Printf("\r")
}

// Handles task setting.
func TaskHandler() (err error) {
	var task func(c *Cache) error

	switch Config.SGet("configuration", "task") {
		case "download":
			//task = DownloadFolder
		case "upload":
			//task = UploadFolder
		default:
			fmt.Printf("- Error: Unrecognized task: %s.\n", Config.SGet("configuration", "task"))
			return
	}

	c := NewCache()
	defer c.Flush()

	fmt.Printf("\r-> starting task %s\n", Config.SGet("configuration", "task"))
	ShowLoader()
	err = task(c)
	HideLoader()
	return
}

// Create local folder, renames if renamed on appliance.
func mkFolder(path string, folder_id int) (err error) {

	l_path := Config.SGet("configuration", "local_path") + "/"
	l_path = cleanPath(l_path)

	finfo, err := os.Stat(l_path + path)
	if err != nil && os.IsNotExist(err) {
		var old_path string
		found, err := DB.Get("folders", folder_id, &old_path)
		if err != nil {
			return err
		}
		if found {
			if old_path != path {
				err = os.Rename(l_path+old_path, l_path+path)
				if err != nil {
					return err
				}
				return DB.Set("folders", folder_id, path)
			}
		}
		err = os.Mkdir(l_path+path, 0755)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// If the folder doesn't exist, create it.
	if finfo != nil && finfo.IsDir() == false {
		err = os.Remove(l_path + path)
		if err != nil {
			return err
		}
		err = os.Mkdir(path, 0755)
	}
	return DB.Set("folders", folder_id, path)
}

// Follow folders and create local paths that don't exist on local machine.
func (s *Session) DownloadMap(folder_id int, path string, c *Cache) (err error) {

	if c == nil {
		c = NewCache()
	}

	path = cleanPath(path)

	err = mkFolder(path, folder_id)
	if err != nil {
		return err
	}

	c.Set("folder_map", folder_id, path)

	remote_folders, err := s.ListFolders(folder_id)
	if err != nil {
		return err
	}

	for _, f := range remote_folders.Data {
		if f.ID == folder_id {
			continue
		}
		if f.Deleted {
			continue
		}
		wg.Add(1)
		go func(folder_id int, path string) {
			if err = s.DownloadMap(folder_id, path, c); err != nil {
				fmt.Printf("- Error: %s", err.Error())
			}
			wg.Done()
		}(f.ID, path+"/"+f.Name)
	}
	return
}

// Follow path and create folders where folders don't exist on server.
func (s Session) UploadMap(folder_id int, path string, c *Cache) (err error) {

	path = cleanPath(path)
	if c == nil {
		c = NewCache()
	}


	remote_folders, err := s.ListFolders(folder_id)
	if err != nil {
		return err
	}

	fmap := make(map[string]int)

	for _, f := range remote_folders.Data {
		fmap[f.Name] = f.ID
	}

	local_folders, err := ioutil.ReadDir(path)

	for _, f := range local_folders {
		if !f.IsDir() {
			continue
		}
		name := f.Name()
		if _, found := fmap[name]; found {
			wg.Add(1)
			go func(folder_id int, path string) {
				if err = s.UploadMap(folder_id, path, c); err != nil {
					fmt.Printf("- Error: %s", err.Error())
					wg.Done()
				}
			}(fmap[name], cleanPath(path+"/"+name))
		} else {
			wg.Add(1)
			go func(folder_id int, name string) {
				if new_id, err := s.CreateFolder(name, folder_id); err != nil {
					fmt.Printf("- Error: %s", err.Error())
				} else {
					if err = s.UploadMap(new_id, path+"/"+name, c); err != nil {
						fmt.Printf("- Error: %s", err.Error())
					}
				}
				wg.Done()
			}(folder_id, name)
		}
	}

	return
}
