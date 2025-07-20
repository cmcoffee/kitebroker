package user

import (
	"fmt"
	. "kitebroker/core"
)

type PushFileTask struct {
	input struct {
		folders []string
	}
	user_count    Tally
	limiter       LimitGroup
	file_limiter  LimitGroup
	files_count   Tally
	folders_count Tally
	total_files   Tally
	//pcache Table
	profiles map[int]KWProfile
	users    Table
	KiteBrokerTask
}

func (T PushFileTask) New() Task {
	return new(PushFileTask)
}

func (T PushFileTask) Name() string {
	return "push_files"
}

func (T PushFileTask) Desc() string {
	return "Pushs files within folders to mobile devices."
}

func (T *PushFileTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.folders, "folders", "<My Folder>", "Folder(s) to comb for pushing files, and folders nested..")
	T.Flags.InlineArgs("folders")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *PushFileTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	//T.pcache = OpenCache().Table("profiles")
	T.folders_count = T.Report.Tally("Folders Scanned")
	T.total_files = T.Report.Tally("Total Files")
	T.files_count = T.Report.Tally("Files Pushed")
	T.user_count = T.Report.Tally("Analyzed Users")
	T.profiles, err = T.KW.Profiles()
	if err != nil {
		return err
	}

	message := func() string {
		return fmt.Sprintf("[ Working .. Folders Scanned: %d | Files Pushed: %d/Total: %d ]", T.folders_count.Value(), T.files_count.Value(), T.total_files.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	var folders []*KiteObject
	sess := T.KW
	if len(T.input.folders) == 0 {
		if err := sess.DataCall(APIRequest{
			Method: "GET",
			Path:   "/rest/folders/top",
			Params: SetParams(Query{"deleted": false, "with": "(currentUserRole)"}),
			Output: &folders,
		}, -1, 1000); err != nil {
			return err
		}
	} else {
		for _, v := range T.input.folders {
			f, err := sess.Folder("0").Find(v)
			if err != nil {
				Err("[%s]: %v", v, err)
				continue
			}
			folders = append(folders, &f)
		}
	}

	for _, v := range folders {
		// Only process folders this user owns.
		if v.CurrentUserRole.ID < 3 {
			continue
		}
		T.limiter.Add(1)
		go func(sess KWSession, folder *KiteObject) {
			defer T.limiter.Done()
			T.ProcessFolder(&sess, folder)
		}(sess, v)
	}

	T.limiter.Wait()
	T.file_limiter.Wait()

	T.limiter.Wait()
	return nil

}

func (T *PushFileTask) PushFile(sess *KWSession, file *KiteObject) {
	T.total_files.Add(1)
	if !file.Pushed {
		Log("Pushing file %s ...", file.Path)
		err := T.KW.File(file.ID).Push()
		if err != nil {
			Err("%s: %v", file.Path, err)
		} else {
			T.files_count.Add(1)
		}
	}
}

func (T *PushFileTask) CheckFile(sess *KWSession, file *KiteObject) {
	if T.limiter.Try() {
		go func(sess *KWSession, file *KiteObject) {
			T.PushFile(sess, file)
			T.limiter.Done()
		}(sess, file)
	} else {
		T.PushFile(sess, file)
	}
}

func (T *PushFileTask) ProcessFolder(sess *KWSession, folder *KiteObject) {
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
							T.ProcessFolder(sess, folder)
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
		Log("Scanning Folder \"%s\" ...", folders[n].Path)
		if folders[n].Type == "d" {
			T.folders_count.Add(1)
			childs, err := sess.Folder(folders[n].ID).Contents()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					} else if childs[i].Type == "f" {
						T.CheckFile(sess, &childs[i])
					}
				}
			} else {
				Err("%s: %v", folders[n].Path, err)
			}
		} else if folders[n].Type == "f" {
			T.CheckFile(sess, folders[n])
		}
		n++
	}
	return

}
