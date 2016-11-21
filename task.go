package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// KiteBroker Flags
const (
	DOWNLOADED = 1 << iota
	MOVED
	UPLOADED
	UPLOADING
)

type FileRecord struct {
	Flag uint64 `json:"flag"`
}

type Job struct {
	user 	string
	name    string
	*Cache
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
					fmt.Printf("\r%s Working, Please wait. ", str)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func ShowLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 0, 1)
}

func HideLoader() {
	atomic.CompareAndSwapInt32(&show_loader, 1, 0)
}

// Runs through jobs attribute.
func JobHandler(users []string) {
	if len(users) == 0 {
		users = append(users[0:], DB.SGet("tokens", "whoami"))
	}
	for _, user := range users {
		for _, job := range Config.Get("configuration", "job_queue") {

			var jfunc func() error

			j := &Job{
				job,
				user,
				NewCache(),
			}

			switch Config.SGet(job, "task") {
				case "download_my_folder":
					jfunc = j.DownloadMyFolder
				case "download_folder":
					jfunc = j.DownloadFolder
				case "upload":
				case "account_creation":
					if auth_flow != SIGNATURE_AUTH {
						errChk(fmt.Errorf("task = account_creation requires 'auth_mode = signature', currently set as 'auth_mode = %s'.", Config.SGet("configuration", "auth_mode")))
					}
					jfunc = j.Add_Accounts_CSV
				default:
					fmt.Printf("- Error: Unrecognized or missing task for job %s.\n", job)
					return
			}
			fmt.Printf("\r-> (%s) starting job %s\n", user, j.name)
			ShowLoader()
			err := jfunc()
			HideLoader()
			if err != nil {
				fmt.Printf("\r- Error(%s): %s\n", job, err.Error())
			}
		}
	}
}

func (j *Job) DownloadMyFolder() (err error) {
	delete_after_download := getBoolVal(Config.SGet(j.name, "delete_remote_files_on_download"))
	err = MkDir(Config.SGet(j.name, "local_path"))
	if err != nil {
		return err
	}
	fmt.Printf("\r(%s) Downloading \"My Folder\" as %s.\n", j.name, j.user)
	s := Session(j.user)
	if err != nil { return err }
	folder_id, err := s.MyFolderID()
	if err != nil {
		return fmt.Errorf("Problem obtaining \"My Folder\" data for %s: %s\n", j.user, err.Error())
	}
	err = j.DownloadMap(s, folder_id, "/"+j.user+"/")
	if err != nil {
		return err
	}

	wg.Wait()

	ids, err := DB.ListNKeys(j.name + "_folders")
	if err != nil {
		return err
	}

	for _, id := range ids {
		files, err := s.ListFiles(id)
		if err != nil {
			fmt.Printf("\r(%s) Error: %s\n", j.name, err.Error())
			continue
		}
		for _, f := range files.Data {
			path, found := j.CacheGetName("folder_map", id)
			if !found {
				DB.Unset(j.name+"_folders", id)
				continue
			}
			wg.Add(1)
			go func(file_id int, file_name, local_path, remote_path string) {

				err := j.Download(s, file_id, local_path+"/"+path)
				if err != nil {
					fmt.Printf("\r(%s) Error: %s\n", j.name, err.Error())
				}
				if delete_after_download {
					fmt.Printf("\r(%s) Removing %s from appliance.\n", j.name, file_name)
					err = s.EraseFile(file_id)
					if err != nil {
						fmt.Printf("\r(%s) Error removing %s: %s\n", j.name, file_name, err.Error())
					}
				}
				wg.Done()
			}(f.ID, f.Name, Config.SGet(j.name, "local_path"), path)
		}
	}
	wg.Wait()
	return
}

func (j *Job) DownloadFolder() (err error) {
	delete_after_download := getBoolVal(Config.SGet(j.name, "delete_remote_files_on_download"))
	err = MkDir(Config.SGet(j.name, "local_path"))
	if err != nil {
		return err
	}
	username := Config.SGet(j.name, "user")
	r_path := Config.SGet(j.name, "remote_folder")

	fmt.Printf("\r(%s) Downloading folder '%s' as %s.\n", j.name, r_path, username)

	s := Session(username)
	folder_id, err := s.FindFolder(r_path)
	if err != nil {
		return err
	}

	err = j.DownloadMap(s, folder_id, "/")
	if err != nil {
		return err
	}

	wg.Wait()

	ids, err := DB.ListNKeys(j.name + "_folders")
	if err != nil {
		return err
	}

	for _, id := range ids {

		files, err := s.ListFiles(id)
		if err != nil {
			fmt.Printf("\r(%s) Error: %s\n", j.name, err.Error())
			continue
		}
		for _, f := range files.Data {
			path, found := j.CacheGetName("folder_map", id)
			if !found {
				DB.Unset(j.name+"_folders", id)
				continue
			}
			wg.Add(1)
			go func(file_id int, file_name, local_path, remote_path string) {

				err := j.Download(s, file_id, local_path+"/"+path)
				if err != nil {
					fmt.Printf("\r(%s) Error: %s\n", j.name, err.Error())
				}
				if delete_after_download {
					fmt.Printf("\r(%s) Removing %s from appliance.\n", j.name, file_name)
					err = s.EraseFile(file_id)
					if err != nil {
						fmt.Printf("\r(%s) Error removing %s: %s\n", j.name, file_name, err.Error())
					}
				}
				wg.Done()
			}(f.ID, f.Name, Config.SGet(j.name, "local_path"), path)
		}
	}

	wg.Wait()
	return
}

// Add User via CSV
func (j *Job) Add_Accounts_CSV() (err error) {
	csv_file := Config.SGet(j.name, "csv_file")
	f, err := os.Open(csv_file)
	if err != nil {
		return err
	}
	r := csv.NewReader(f)

	admin := Session(Config.SGet(j.name, "admin_bind"))
	_, err = admin.MyUser()
	if err != nil {
		return fmt.Errorf("admin_bind: %s", err)
	}

	manager := Session(Config.SGet(j.name, "manager_bind"))

	_, err = manager.MyUser()
	if err != nil {
		return fmt.Errorf("manager_bind: %s", err)
	}

	roles, err := admin.GetRoles()
	if err != nil {
		return fmt.Errorf("admin_bind: %s", err)
	}

	for _, elem := range roles.Data {
		j.CacheSet("role_map", elem.Name, elem.ID)
	}

	// Performs calls to add user, adding the folder id to the map and account_id as well.
	add_user := func(account, folder, role string) (err error) {
		folder_id, found := j.CacheGetID("folder_map", folder)

		if !found {
			folder_id, err = manager.FindFolder(folder)

			if err != nil {
				folder_id = -1
			}

			j.CacheSet("folder_map", folder, folder_id)
		}

		if folder_id < 0 {
			return fmt.Errorf("Couldn't find requested folder.")
		}

		role_id, found := j.CacheGetID("role_map", role)

		if !found {
			return fmt.Errorf("Role %s not found when trying to add %s to %s.", role, account, folder)
		}

		account_id, _ := j.CacheGetID("account_map", account)

		return manager.AddUserToFolder(account_id, folder_id, role_id, false)
	}

	for {
		records, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		if len(records) < 1 {
			continue
		}

		wg.Add(1)
		go func(records []string) {
			defer wg.Done()

			var account_id int

			verify := getBoolVal(Config.SGet(j.name, "verify"))
			notify := getBoolVal(Config.SGet(j.name, "notify"))
			err = admin.AddUser(records[0], records[1], verify, notify)
			if err != nil {
				if !strings.Contains(err.Error(), "409") {
					fmt.Printf("\r(%s) - Error: %s\n", j.name, err.Error())
					goto Done
				} else {
					fmt.Printf("\r(%s) Account %s already exists.\n", j.name, records[0])
				}
			} else {
				fmt.Printf("\r(%s) Account %s added to system.\n", j.name, records[0])
			}
			account_id, err = admin.FindUser(records[0])
			if err != nil {
				fmt.Printf("\r- Error(%s): %s\n", j.name, err.Error())
				goto Done
			}

			j.CacheSet("account_map", records[0], account_id)

			for i := 1; i < len(records)-1; i = i + 2 {
				err = add_user(records[0], records[i], records[i+1])
				if err != nil {
					if !strings.Contains(err.Error(), "409") {
						fmt.Printf("\r(%s) - Error: Account[%s] Folder[%s] Role[%s]: %s\n", j.name, records[0], records[i], records[i+1], err.Error())
					} else {
						fmt.Printf("\r(%s) %s is already member of folder %s.\n", j.name, records[0], records[i])
					}
				} else {
					fmt.Printf("\r(%s) Added %s to folder %s.\n", j.name, records[0], records[i])
				}
			}
		Done:
		}(records)
	}

	wg.Wait()
	return nil
}

// Create local folder, renames if renamed on appliance.
func (j *Job) downloadFolder(path string, folder_id int) (err error) {

	table_name := fmt.Sprintf("%s_folders", j.name)
	l_path := Config.SGet(j.name, "local_path") + "/"
	l_path = cleanPath(l_path)

	finfo, err := os.Stat(l_path + path)
	if err != nil && os.IsNotExist(err) {
		var old_path string
		found, err := DB.Get(table_name, folder_id, &old_path)
		if err != nil {
			return err
		}
		if found {
			if old_path != path {
				err = os.Rename(l_path+old_path, l_path+path)
				if err != nil {
					return err
				}
				return DB.Set(table_name, folder_id, path)
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
	return DB.Set(table_name, folder_id, path)
}

// Follow folders and create local paths that don't exist on local machine.
func (j *Job) DownloadMap(s Session, folder_id int, path string) (err error) {

	path = cleanPath(path)

	err = j.downloadFolder(path, folder_id)
	if err != nil {
		return err
	}

	j.CacheSet("folder_map", folder_id, path)

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
			if err = j.DownloadMap(s, folder_id, path); err != nil {
				fmt.Printf("- Error: %s", err.Error())
			}
			wg.Done()
		}(f.ID, path+"/"+f.Name)
	}
	return
}

// Follow path and create folders where folders don't exist on server.
func (j *Job) UploadMap(s Session, folder_id int, path string) (err error) {

	path = cleanPath(path)

	remote_folders, err := s.ListFolders(folder_id)
	if err != nil {
		return err
	}

	j.CacheSet("folder_map", path, folder_id)

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
			go func(s Session, folder_id int, path string) {
				if err = j.UploadMap(s, folder_id, path); err != nil {
					fmt.Printf("- Error: %s", err.Error())
					wg.Done()
				}
			}(s, fmap[name], cleanPath(path+"/"+name))
		} else {
			wg.Add(1)
			go func(s Session, folder_id int, name string) {
				if new_id, err := s.CreateFolder(name, folder_id); err != nil {
					fmt.Printf("- Error: %s", err.Error())
				} else {
					if err = j.UploadMap(s, new_id, path+"/"+name); err != nil {
						fmt.Printf("- Error: %s", err.Error())
					}
				}
				wg.Done()
			}(s, folder_id, name)
		}
	}

	return
}