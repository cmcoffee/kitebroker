package admin

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"strings"
	"sync"
)

const (
	DOWNLOADER   = 2
	COLLABORATOR = 3
	MANAGER      = 4
	OWNER        = 5
	VIEWER       = 6
	UPLOADER     = 7
)

const (
	ws_name = iota
	ws_path
	ws_expiry
	ws_last_update
	ws_owner
	ws_manager
	ws_contributor
	ws_uploader
	ws_viewer
	ws_quota
	ws_replicate
)

type FolderPermissionFixer struct {
	ppt          Passport
	map_lock     sync.Mutex
	perm_map     map[string]map[string]int
	kw_perm_map  map[int]map[string]int
	workspace    []string
	wg           LimitGroup
	folders      Tally
	perm_updates Tally
}

func (T FolderPermissionFixer) New() Task {
	return new(FolderPermissionFixer)
}

// Parse CSV file from FTA.
func read_csv(file string) (output map[string]map[string]int, err error) {
	output = make(map[string]map[string]int)

	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)

	line := 0

	for s.Scan() {
		line++
		r := csv.NewReader(bytes.NewReader(s.Bytes()))
		o, err := r.Read()
		if err != nil {
			return nil, fmt.Errorf("%s: Parse Error On Line %d!!! (%v)", file, err)
		}
		if len(o) >= 11 {
			if o[ws_expiry] == "Available Until" {
				continue
			}
		} else {
			continue
		}

		output[o[ws_path]] = make(map[string]int)

		if o[ws_owner] != NONE {
			output[o[ws_path]][o[ws_owner]] = OWNER
		}

		for _, v := range strings.Split(o[ws_manager], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = MANAGER
			}
		}
		for _, v := range strings.Split(o[ws_contributor], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = COLLABORATOR
			}
		}
		for _, v := range strings.Split(o[ws_uploader], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = UPLOADER
			}
		}
		for _, v := range strings.Split(o[ws_viewer], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = DOWNLOADER
			}
		}
	}

	return
}

func (T *FolderPermissionFixer) GetMembers(in_map map[string]int, role int) (output []string) {
	T.map_lock.Lock()
	defer T.map_lock.Unlock()

	for k, v := range in_map {
		if v == role {
			output = append(output, k)
		}
	}
	return
}

func (T *FolderPermissionFixer) Init(flags *FlagSet) (err error) {
	csv := flags.String("csv", "<workspace_list.csv>", "FTA CSV File")
	flags.ArrayVar(&T.workspace, "folder", "<specific folder>", "Perform permission updates on a specific kiteworks folder.")
	if err := flags.Parse(); err != nil {
		return err
	}

	for _, v := range T.workspace {
		if strings.Contains(v, "/") {
			return fmt.Errorf("--folder must not be a top-level folder.")
		}
	}

	if *csv == NONE {
		return fmt.Errorf("Must provide a --csv.")
	}

	T.perm_map, err = read_csv(*csv)
	if err != nil {
		return err
	}

	return
}

func (T *FolderPermissionFixer) Main(passport Passport) (err error) {
	T.ppt = passport
	T.kw_perm_map = make(map[int]map[string]int)

	T.wg = NewLimitGroup(10)

	if len(T.workspace) > 0 {
		for k, _ := range T.perm_map {
			found := false
			for i := 0; i < len(T.workspace); i++ {
				if strings.Split(strings.ToLower(k), "/")[0] == strings.ToLower(T.workspace[i]) {
					found = true
				}
			}
			if !found {
				delete(T.perm_map, k)
			}
		}

		lower := make(map[string]struct{})

		for k, _ := range T.perm_map {
			lower[strings.ToLower(k)] = struct{}{}
		}

		for _, ws := range T.workspace {
			if _, found := lower[strings.ToLower(ws)]; !found {
				Err("%s: Unable to find workspace in csv.", ws)
			}
		}
	}

	wg := NewLimitGroup(25)

	T.folders = T.ppt.Tally("Folders Processed")
	T.perm_updates = T.ppt.Tally("Permission Updates")

	message := func() string {
		return fmt.Sprintf("Please wait ... [folders: %d/updates: %d]", T.folders.Value(), T.perm_updates.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})

	var work_list []string

	for k, _ := range T.perm_map {
		if !strings.Contains(k, "/") {
			work_list = append(work_list, k)
		}
	}

	for _, k := range work_list {
		wg.Add(1)
		go func(k string) {
			T.FixFolder(k)
			wg.Done()
		}(k)
	}

	T.wg.Wait()
	wg.Wait()

	return nil
}

func (T *FolderPermissionFixer) FixFolder(folder string) {
	kw_user, target, err := T.FindManager(folder)
	if err != nil {
		Err("%s: %v", folder, err)
		return
	}
	Log("Processing folder '%s' as '%s'.", folder, kw_user)
	T.ProcessFolders(kw_user, *target)
}

func (T *FolderPermissionFixer) ProcessFolders(kw_user string, target KiteObject) {
	var (
		current []KiteObject
		next    []KiteObject
		n       int
	)

	current = append(current[:0], target)

	for {
		if n > len(current)-1 {
			if len(next) > 0 {
				current = append(current[:0], next[0:]...)
				next = next[0:0]
			} else {
				break
			}
			n = 0
		}

		folder := current[n]

		if n+1 < len(current)-1 {
			if T.wg.Try() {
				go func(kw_user string, folder KiteObject) {
					defer T.wg.Done()
					T.ProcessFolders(kw_user, folder)
				}(kw_user, folder)
				n++
				continue
			}
		}
		T.SetPermissions(kw_user, folder)
		children, err := T.ppt.Session(kw_user).Folder(folder.ID).Folders()
		if err != nil {
			Err("%s[%d]: %v", folder.Path, folder.ID, err)
			n++
			continue
		}
		for _, c := range children {
			switch c.Type {
			case "d":
				next = append(next, c)
			}
		}
		n++
		continue

	}
}

var ErrNoManagers = fmt.Errorf("No viable managers were found in kiteworks for folder!")	

func (T *FolderPermissionFixer) MakeManager(folder string) (kw_user string, err error) {
	var owner string

	fta_managers := T.GetMembers(T.perm_map[folder], OWNER)
	if len(fta_managers) > 0 {
		owner = fta_managers[0]
	}
	if owner == NONE {
		return NONE, ErrNoManagers

	}
	Log("%s: Attmpting to create owner: %s", folder, owner)

	var users []KiteUser

	err = T.ppt.DataCall(APIRequest{
		Method: "GET",
		Path:	"/rest/admin/users",
		Params: SetParams(Query{"email": owner, "deleted": false}),
		Output: &users,
	}, -1, 1000)
	if err != nil {
		return NONE, err
	}

	if len(users) == 0 {
		return NONE, ErrNoManagers	
	}

	err = T.ppt.Call(APIRequest{
		Method: "PUT",
		Path: SetPath("/rest/admin/users/%d", users[0].ID),
		Params: SetParams(PostJSON{"suspended": false, "verified": true}),
	})

	if err != nil {
		return NONE, err
	}

	return owner, nil
}

func (T *FolderPermissionFixer) FindManager(folder string) (kw_user string, kw_folder *KiteObject, err error) {
	fta_managers := T.GetMembers(T.perm_map[folder], MANAGER)

	if len(fta_managers) == 0 {
		return NONE, nil, fmt.Errorf("No managers found for specified workspace in csv!")
	}

	fta_managers = append(fta_managers, "migration_user@accellion.com")

	var (
		folder_id int
		kw_f      KiteObject
		managers  []string
	)

	created_manager := false 
	for {
		for i, u := range fta_managers {
			sess := T.ppt.Session(u)
			if result, err := sess.Folder(0).Find(folder); err != nil {
				if err != ErrNotFound && u != "migration_user@accellion.com" {
					Err("%s: (%s): %v", folder, u, err)
				}
				continue
			} else {
				if result.ID != folder_id && folder_id != 0 {
					return NONE, nil, fmt.Errorf("Multiple folders with same name detected!")
				} else {
					folder_id = result.ID
				}

				if result.CurrentUserRole.Rank >= 400000 {
					managers = append(managers, fta_managers[i])
				}	

				kw_f = result
			}
		}
		if len(managers) > 0 {
			return managers[0], &kw_f, nil
		} else {
			if !created_manager {
				user, err := T.MakeManager(folder)
				if err != nil {
					return NONE, nil, err
				}
				fta_managers = append(fta_managers, user)
				created_manager = true
				continue
			}
		}
	}
	return NONE, nil, fmt.Errorf("No viable managers were found in kiteworks for folder!")
}

func (T *FolderPermissionFixer) SetPermissions(kw_user string, target KiteObject) {
	T.map_lock.Lock()

	T.kw_perm_map[target.ID] = make(map[string]int)

	previous := make(map[string]int)

	fta_map := T.perm_map[target.Path]
	if kw_map, found := T.kw_perm_map[target.ParentID]; found {
		for k, v := range kw_map {
			if fta_map[k] == v {
				previous[k] = v
				delete(fta_map, k)
			}
		}
	}

	for k, v := range previous {
		T.kw_perm_map[target.ID][k] = v
	}

	T.map_lock.Unlock()


	kw_sess := T.ppt.Session(kw_user)

	set_perm := func(kw_sess KWSession, users []string, role_id int, target KiteObject) (err error) {
		if len(users) > 0 {
			if len(users) == 1 && users[0] == NONE {
				return
			}
			if err := kw_sess.Folder(target.ID).AddUsersToFolder(users, role_id); err != nil {
				if !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED", "ERR_ENTITY_IS_OWNER") {
					return err
				}
			}
			T.perm_updates.Add(1)
			for _, u := range users {
				T.map_lock.Lock()
				T.kw_perm_map[target.ID][u] = role_id
				T.map_lock.Unlock()
			}
		}
		return nil
	}

	var managers []string

	owner := T.GetMembers(fta_map, OWNER)
	if len(owner) > 0 && owner[0] != NONE {
		managers = append(managers, owner[0])
	}

	managers = append(managers, T.GetMembers(fta_map, MANAGER)[0:]...)

	if err := set_perm(kw_sess, managers, MANAGER, target); err != nil {
		Err("%s[%d]: %v", target.Path, target.ID, err)
	}

	collabs := T.GetMembers(fta_map, COLLABORATOR)

	if err := set_perm(kw_sess, collabs, COLLABORATOR, target); err != nil {
		Err("%s[%d]: %v", target.Path, target.ID, err)
	}

	uploaders := T.GetMembers(fta_map, UPLOADER)

	if err := set_perm(kw_sess, uploaders, UPLOADER, target); err != nil {
		Err("%s[%d]: %v", target.Path, target.ID, err)
	}

	downloaders := T.GetMembers(fta_map, DOWNLOADER)

	if err := set_perm(kw_sess, downloaders, DOWNLOADER, target); err != nil {
		Err("%s[%d]: %v", target.Path, target.ID, err)
	}

	T.folders.Add(1)
}
