package admin

import (
	"encoding/csv"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"strings"
	"sync"
	"time"
	//"strings"
)

type MetadataTask struct {
	// input variables
	input struct {
		profile_id   int
		user_emails  []string
		folders      []string
		output_file  string
		max_days     int
		no_overwrite bool
	}
	csv_writer    *csv.Writer
	user_count    Tally
	folder_count  Tally
	file_count    Tally
	skipped_users int64
	file_activity map[string]bool
	record_lock   sync.RWMutex
	msg           string
	locker        sync.Mutex
	// Required for all tasks
	KiteBrokerTask
}

func (T MetadataTask) New() Task {
	return new(MetadataTask)
}

func (T MetadataTask) Name() string {
	return "folder_metadata"
}

func (T MetadataTask) Desc() string {
	return "Retrieves folder metadata from user's folders."
}

func (T *MetadataTask) Init() (err error) {
	all_users := T.Flags.Bool("all_users", "Apply folder and file limits to everyone in all profiles.")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Users to specify.")
	T.Flags.MultiVar(&T.input.folders, "folder", "<My Folder>", "Specify folder name(s) you want to retrieve metadata on.")
	T.Flags.StringVar(&T.input.output_file, "to_file", "unused_folders.csv", "Specify file for output of metadata information.")
	T.Flags.IntVar(&T.input.max_days, "days_back", 365, "Max number of days to look for an activity.")
	T.Flags.BoolVar(&T.input.no_overwrite, "no", "Do not overwrite existing CSV file, create new instead.")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if !*all_users && T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		err = fmt.Errorf("Please specify --all_users, and/or select users based on --profile_id or --users.")
	}

	return
}

func (T *MetadataTask) Write(path string, user string) (err error) {
	T.locker.Lock()
	defer T.locker.Unlock()
	Log("%s,%s,\"%s\"", path, user, T.msg)
	err = T.csv_writer.Write([]string{path, user, T.msg})
	if err == nil {
		T.csv_writer.Flush()
	}
	return
}

func (T *MetadataTask) Main() (err error) {
	// Main function

	T.file_activity = make(map[string]bool)
	T.file_count = T.Report.Tally("Files Scanned")
	T.user_count = T.Report.Tally("Total Users")
	T.folder_count = T.Report.Tally("Folders Scanned")
	T.msg = fmt.Sprintf("No Download/Upload/View File Activties found within %d days.", T.input.max_days)

	filename_split := strings.Split(T.input.output_file, ".")
	if len(filename_split) == 1 {
		filename_split = append(filename_split, ".csv")
	}

	var filename string

	var wg sync.WaitGroup

	for i := 0; ; i++ {
		if i > 0 {
			filename = fmt.Sprintf("%s (%d).%s", filename_split[0], i, filename_split[1])
		} else {
			filename = strings.Join(filename_split[0:], ".")
		}

		_, err := os.Stat(filename)
		if err != nil && os.IsNotExist(err) {
			file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				return err
			}
			T.csv_writer = csv.NewWriter(file)
			break
		} else if err != nil {
			return err
		} else {
			if !T.input.no_overwrite {
				if err := Delete(filename); err != nil {
					return err
				}
				i = -1
			}
			continue
		}
	}

	params := Query{"active": true, "verified": true, "allowsCollaboration": true, "suspended": false}

	user_getter, err := T.KW.Admin().Users(T.input.user_emails, T.input.profile_id, params)
	if err != nil {
		return err
	}

	total_users := user_getter.Total()

	user_counter := func() int64 {
		return T.skipped_users + T.user_count.Value() + int64(user_getter.Failed())
	}

	message := func() string {
		return fmt.Sprintf("Please wait ... [users: %d of %d/folders scanned: %d/files scanned: %d]", user_counter(), total_users, T.folder_count.Value(), T.file_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			Log("Gathering folder activities for %s ..", user.Email)
			sess := T.KW.Session(user.Email)
			T.user_count.Add(1)
			if len(T.input.folders) > 0 {
				for _, v := range T.input.folders {
					folder_obj, err := sess.Folder("0").Find(v)
					if err != nil {
						Err(fmt.Errorf("%s: %s", v, err))
						continue
					}
					wg.Add(1)
					go func(sess KWSession, folder_obj KiteObject) {
						defer wg.Done()
						T.FolderProcessor(sess, folder_obj)
					}(sess, folder_obj)
				}
			} else {
				folders, err := sess.OwnedFolders()
				if err != nil {
					Err("[%s]: %v", user.Email, err)
					continue
				}
				for _, v := range folders {
					wg.Add(1)
					go func(sess KWSession, folder_obj KiteObject) {
						defer wg.Done()
						T.FolderProcessor(sess, folder_obj)
					}(sess, v)
				}
			}
		}
		wg.Wait()
	}

	return
}

func (T *MetadataTask) UpdateActivity(folder_id string) {
	T.record_lock.Lock()
	T.file_activity[folder_id] = true
	T.record_lock.Unlock()
}

func (T *MetadataTask) CheckForActivity(folder_id string) bool {
	T.record_lock.RLock()
	defer T.record_lock.RUnlock()
	return T.file_activity[folder_id]
}

func (T *MetadataTask) FolderProcessor(sess KWSession, obj KiteObject) {
	start_folder := obj
	fproc := func(user *KWSession, obj *KiteObject) (err error) {
		time_start := time.Now().AddDate(0, 0, T.input.max_days*-1)
		if T.CheckForActivity(start_folder.ID) {
			return AbortError(nil)
		}
		created, err := ReadKWTime(obj.Created)
		if err != nil {
			return AbortError(err)
		}
		var modified time.Time

		if !IsBlank(obj.ClientModified) {
			modified, err = ReadKWTime(obj.ClientModified)
			if err != nil {
				return AbortError(err)
			}
		}
		if obj.Type == "d" {
			T.folder_count.Add(1)
			if obj.Name == "My Folder" {
				T.UpdateActivity(start_folder.ID)
				return AbortError(nil)
			}
			if created.After(time_start) {
				T.UpdateActivity(start_folder.ID)
				return AbortError(nil)
			}
			//activities, err := user.Folder(obj.ID).Activities()
			//if err != nil {
			//	return AbortError(err)
			//}
			//for _, a := range activities {
			//if a.Data.Comment.Content != "" {
			//		Log("[%s]: %s: %s", a.User.Name, a.Message, a.Data.Comment.Content)
			//	} else {
			//Log("[%s] %s - %s - %s", obj.Path, a.Created, a.User.Name, a.Message)
			//Critical(T.Write(obj.Path, a.Created, a.User.Name, a.Message))
			//	}
			//}
		} else if obj.Type == "f" {
			if created.After(time_start) || modified.After(time_start) {
				T.UpdateActivity(start_folder.ID)
				T.file_count.Add(1)
				return AbortError(nil)
			}
			activities, err := user.File(obj.ID).Activities(Query{"noDayBack": T.input.max_days})
			if err != nil {
				return AbortError(err)
			}
			T.file_count.Add(1)
			for _, a := range activities {
				switch a.Event {
				case "download":
					fallthrough
				case "view_file":
					act_created, err := ReadKWTime(a.Created)
					if err != nil {
						Err("%s/%s: %v", obj.Path, obj.Name, err)
						T.UpdateActivity(start_folder.ID)
						return AbortError(nil)
					}
					if act_created.After(time_start) {
						T.UpdateActivity(start_folder.ID)
						return AbortError(nil)
					}
				default:
					continue
				}
			}
			if obj.Locked() {
				if err := sess.File(obj.ID).Unlock(); err != nil {
					if !IsAPIError(err, "ERR_ENTITY_UNLOCKED") {
						Err("[%s] Error unlocking file %s: %s", sess.Username, obj.Path, err)
					}
				}
			}
		} else {
			return
		}
		return nil
	}
	sess.FolderCrawler(fproc, obj)
	if !T.CheckForActivity(obj.ID) {
		T.Write(obj.Name, sess.Username)
	}
}
