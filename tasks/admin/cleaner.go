package admin

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"time"
)

type FileCleanerTask struct {
	input struct {
		user string
		folder string
		max_file_age int
		perm_delete bool
	}
	limiter LimitGroup
	expiry time.Time
	files_removed Tally
	files_count Tally
	folders_count Tally
	space_recovered Tally
	sess KWSession
	dry_run bool
	KiteBrokerTask
}

func (T FileCleanerTask) New() Task {
	return new(FileCleanerTask)
}

func (T FileCleanerTask) Name() string {
	return "file_cleanup"
}

func (T FileCleanerTask) Desc() string {
	return "Remove files from system older than a specified date."
}

func (T *FileCleanerTask) Init() (err error) {
	T.Flags.StringVar(&T.input.user, "user", "<user@domain.com>", "User to cleanup.")
	T.Flags.StringVar(&T.input.folder, "folder", "<My Folder>", "Folder to check and clean.")
	T.Flags.IntVar(&T.input.max_file_age, "max_days", -1, "Maximum lifetime of file, delete anything older.")
	T.Flags.BoolVar(&T.input.perm_delete, "perm", "Permanently delete files older than --max_days.")
	T.Flags.BoolVar(&T.dry_run, "dry_run", "Don't actually delete, just show expired files.")
	T.Flags.Order("user","folder","max_days","perm")
	if err := T.Flags.Parse(); err != nil {
		return err
	}
	if T.input.max_file_age == -1 || IsBlank(T.input.user) || IsBlank(T.input.folder) {
		err = fmt.Errorf("Required parameters not configured.")
	}
	return
}

func (T *FileCleanerTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	T.folders_count = T.Report.Tally("Folders Analyzed")
	T.files_count = T.Report.Tally("Files Analyzed")

	if !T.dry_run {
		T.space_recovered = T.Report.Tally("Storage Freed", HumanSize)
		T.files_removed = T.Report.Tally("Files Removed")
	} else {
		T.files_removed = T.Report.Tally("Simulated Files Removed")
		T.space_recovered = T.Report.Tally("Simulated Storage Freed", HumanSize)
	}

	message := func() string {
		return fmt.Sprintf("Working .. [Files Analyzed: %d/Deleted: %d(%s)]", T.files_count.Value(), T.files_removed.Value(), HumanSize(T.space_recovered.Value()))
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	T.sess = T.KW.Session(T.input.user)

	top, err := T.sess.Folder("0").Find(T.input.folder)
	if err != nil {
		return err
	}

	if top.Type != "d" {
		return fmt.Errorf("%s: Not a folder.", top.Name)
	}

	T.expiry = time.Now().UTC().Add((time.Hour * 24) * time.Duration(T.input.max_file_age) * -1)

	T.ProcessFolder(&top)
	T.limiter.Wait()
 
	return nil

}

func (T *FileCleanerTask) CheckFile(file *KiteObject) {
	T.files_count.Add(1)
	var err error

	mod_time, err := ReadKWTime(file.Modified)
	if err != nil {
		Err("%s: Could not parse modified time on file: %s", file.Path, file.Modified)
		return
	}

	if mod_time.Unix() < T.expiry.Unix() {
		Log("file: %s (%s) - last modified: %s", file.Path, HumanSize(file.Size), mod_time)
	} else {
		return
	}

	if T.dry_run {
		T.space_recovered.Add64(file.Size)
		T.files_removed.Add(1)
		return
	}

	if T.input.perm_delete {
		err = T.sess.File(file.ID).Delete()
		if err == nil {
			err = T.sess.File(file.ID).PermDelete()
		}
	} else {
		err = T.sess.File(file.ID).Delete()
	}
	if err != nil {
		Err("%s: %v", file.Path, err)
	} else {
		T.files_removed.Add(1)
		T.space_recovered.Add64(file.Size)
	}
}

func (T *FileCleanerTask) ProcessFolder(folder *KiteObject) {
	// Folder is already complete, return to caller.
	var folders []*KiteObject

	folders = append(folders, folder)

	var n int
	var next []*KiteObject

	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if T.limiter.Try() {
						go func(folder *KiteObject) {
							defer T.limiter.Done()
							T.ProcessFolder(folder)
						}(o)
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
		if folders[n].Type == "d" {
			T.folders_count.Add(1)
			childs, err := T.sess.Folder(folders[n].ID).Contents()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					} else if childs[i].Type == "f" {
						T.CheckFile(&childs[i])
					}
				}
			} else {
				Err("%s: %v", folders[n].Path, err)
			}
		} else if folders[n].Type == "f" {
			T.CheckFile(folders[n])
		}
		n++
	}
	return


}