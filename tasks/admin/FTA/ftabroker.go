package FTA

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"strings"
	"sync"
	"io"
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

const (
	DOWNLOADER   = 2
	COLLABORATOR = 3
	MANAGER      = 4
	OWNER        = 5
	VIEWER       = 6
	UPLOADER     = 7
)

type Broker struct {
	map_lock     sync.Mutex
	perm_map     map[string]map[string]int
	kw_perm_map  map[int]map[string]int
	workspace    []string
	manager      string
	wg           LimitGroup
	folders      Tally
	perm_updates Tally
	file_count   Tally
	transfered   Tally
	files        bool
	user_list    []string
	elevate      bool
	kitedrive    bool
	standard_profile_id int
	new_folders_only bool
	filemover    chan *ftacopy
	cache        Database
	uploads      Table
	api          *FTAClient
	notify 		 bool
	skip_permissions bool
	KiteBrokerTask
}

func (T Broker) New() Task {
	return new(Broker)
}

func (T Broker) Name() string {
	return "ftabroker"
}

func (T Broker) Desc() string {
	return "Transfer files/repair permissions on kiteworks folders based on FTA CSV export."
}

// Parse CSV file from FTA.
func (T *Broker) read_csv(file string) (output map[string]map[string]int, err error) {

	T.cache = OpenCache()

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
		raw_text := s.Text()
		r := csv.NewReader(bytes.NewReader([]byte(raw_text)))
		r.LazyQuotes = true
		o, err := r.Read()
		if err != nil {
			if err == io.EOF {
				continue
			}
			return nil, fmt.Errorf("%s: %s '%s'", file, strings.Replace(err.Error(), "line 1", fmt.Sprintf("line %d", line), 1), raw_text)
		} 
		if len(o) >= 10 {
			if o[ws_expiry] == "Available Until" {
				continue
			}
		} else {
			continue
		}

		if len(T.workspace) > 0 {
			found := false
			for _, v := range T.workspace {
				if strings.Split(o[ws_path], "/")[0] == v {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		output[o[ws_path]] = make(map[string]int)

		var owner string

		if o[ws_owner] != NONE {
			output[o[ws_path]][o[ws_owner]] = OWNER
			owner = o[ws_owner]
			T.cache.Set("all_users", owner, 1)
		}

		for _, v := range strings.Split(o[ws_manager], ", ") {
			if v != NONE && v != owner {
				output[o[ws_path]][v] = MANAGER
				T.cache.Set("all_users", v, 1)
			}
		}
		for _, v := range strings.Split(o[ws_contributor], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = COLLABORATOR
				T.cache.Set("all_users", v, 1)
			}
		}
		for _, v := range strings.Split(o[ws_uploader], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = UPLOADER
				T.cache.Set("all_users", v, 1)
			}
		}
		for _, v := range strings.Split(o[ws_viewer], ", ") {
			if v != NONE {
				output[o[ws_path]][v] = DOWNLOADER
				T.cache.Set("all_users", v, 1)
			}
		}

		if len(output[o[ws_path]]) == 0 {
			Debug("%s: No users, %v.", o[ws_path], raw_text)
		}
	}

	return
}

func (T *Broker) GetMembers(in_map map[string]int, role...int) (output []string) {
	T.map_lock.Lock()
	defer T.map_lock.Unlock()

	for k, v := range in_map {
		for _, r := range role {
			if v == r {
				output = append(output, k)
			}
		}
	}
	return
}

func (T *Broker) configure_api(server,client_id,client_secret,signature,redirect_uri string) {

	if IsBlank(server, client_id, client_secret, signature, redirect_uri) {
		var blank_str []string
		if IsBlank(server) {
			blank_str = append(blank_str, "--server")
		}
		if IsBlank(client_id) {
			blank_str = append(blank_str, "--client_id")
		}
		if IsBlank(client_secret) {
			blank_str = append(blank_str, "--secret_key")
		}
		if IsBlank(signature) {
			blank_str = append(blank_str, "--signature_key")
		}
		if IsBlank(redirect_uri) {
			blank_str = append(blank_str, "--redirect_uri")
		}
		Fatal("Missing required parameters for: %s", strings.Join(blank_str, ", "))
	}

	T.api = &FTAClient{new(APIClient)}
	T.api.Server = server
	T.api.ApplicationID = client_id
	T.api.ClientSecret(client_secret)
	T.api.Signature(signature)
	T.api.VerifySSL = false
	T.api.RedirectURI = redirect_uri

}

func (T *Broker) Init() (err error) {
	server := T.Flags.String("server", "<FTA Server>", "Server for FTA System")
	client_id := T.Flags.String("client_id", "<client_id>", "Client ID for FTA API.")
	secret_key := T.Flags.String("secret_key", "<secret_key>", "Secret Key for FTA API.")
	signature_key := T.Flags.String("signature_key", "<signature_key>", "Signature key for FTA API.")
	redirect_uri := T.Flags.String("redirect_uri", "https://kitebroker/", "Redirect URI for FTA API.")

	csv := T.Flags.String("csv", "<workspace_list.csv>", "FTA CSV File")
	T.Flags.MultiVar(&T.workspace, "folder", "<specific folder>", "Perform permission updates on a specific kiteworks folder.")
	T.Flags.StringVar(&T.manager, "manager", "<manager>", "Fallback manager to create folder if folder does not exist.")
	T.Flags.BoolVar(&T.files, "files", "Copy files from FTA system.")
	T.Flags.BoolVar(&T.new_folders_only, "new_folders_only", "Don't modify existing folders.")
	//flags.BoolVar(&T.kitedrive, "kitedrive", false, "Copy kitedrive folders over.")
	T.Flags.BoolVar(&T.notify, "notify", "Send notifications to users added.")
	T.Flags.MultiVar(&T.user_list, "filter_users", "<users>", "Only work when manager/owner is in provided users.")
	T.Flags.BoolVar(&T.elevate, "auto_elevate", "Automatiaclly elevate managers to owners, if no owner is assigned.")
	T.Flags.IntVar(&T.standard_profile_id, "standard_profile_id", 1, "Standard user profile id.")
	T.Flags.BoolVar(&T.skip_permissions, "skip_permissions", "Do not apply permissions to folders.")
	T.Flags.Order("csv", "manager", "folder","filter_users","auto_elevate","standard_profile_id","files","server","client_id","secert_key","signature_key","redirect_uri")
	if err := T.Flags.Parse(); err != nil {
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

	if T.files {
		T.configure_api(*server,*client_id,*secret_key,*signature_key,*redirect_uri)
	}

	T.perm_map, err = T.read_csv(*csv)
	if err != nil {
		return err
	}

	return
}

func (T *Broker) Main() (err error) {
	T.wg = NewLimitGroup(10)

	if T.files {
		T.api.TokenStore = KVLiteStore(OpenCache())
		T.api.Retries = T.KW.Retries
		T.api.ProxyURI = T.KW.ProxyURI
		T.api.AgentString = T.KW.AgentString
		T.api.RequestTimeout = 0
		T.api.ConnectTimeout = T.KW.ConnectTimeout
		T.api.APIClient.NewToken = T.api.newFTAToken
		T.api.ErrorScanner = T.api.ftaError
		T.api.SetLimiter(1)
		T.api.TokenErrorCodes = []string{"221", "120", "ERR_AUTH_UNAUTHORIZED", "INVALID_GRANT"}
		T.filemover = make(chan *ftacopy, 25)
		T.uploads = T.DB.Table("uploads")
		T.wg.Add(1)
		go T.FileMover()
	}

	T.kw_perm_map = make(map[int]map[string]int)

	wg := NewLimitGroup(25)

	T.folders = T.Report.Tally("Folders Processed")
	T.perm_updates = T.Report.Tally("Permission Updates")
	T.file_count = T.Report.Tally("Files Processed")
	if T.files {
		T.transfered = T.Report.Tally("Data Transfered", HumanSize)
	}

	message := func() string {
		return fmt.Sprintf("Please wait ... [folders: %d/files: %d]", T.folders.Value(), T.file_count.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})

	var work_list []string

	for k, _ := range T.perm_map {
		if !strings.Contains(k, "/") {
			work_list = append(work_list, k)
		}
	}

	if T.manager != NONE {
		if err := T.MakeManager(T.manager); err != nil {
			return err
		}
	}

	for _, k := range work_list {
		wg.Add(1)
		go func(k string) {
			T.FixFolder(k)
			wg.Done()
		}(k)
	}

	wg.Wait()

	if T.files {
		//T.CopyKitedrive()
		T.filemover<-nil
	}

	T.wg.Wait()
	return nil
}

// Copies kitedrive contents for each user found in CSV.
func (T *Broker) CopyKitedrive() {
	for _, u := range T.cache.Keys("all_users") {
		Log("Processing user %s's kitedrive folder.", u)
	}
}

// Fixes folder permissions
func (T *Broker) FixFolder(folder string) {
	kw_user, target, err := T.FindManager(folder)
	if err != nil {
		Err("%s: %v", folder, err)
		return
	}
	if err == nil && kw_user == NONE {
		return
	}
	if T.manager != NONE {
		kw_user = T.manager
		Log("Override given for manager: %s", T.manager)
	}
	T.ProcessFolders(kw_user, *target)
}

// Process FTA folders for kiteworks.
func (T *Broker) ProcessFolders(kw_user string, target KiteObject) {
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

		Log("Processing folder '%s' as '%s'.", folder.Path, kw_user)

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

		if !T.skip_permissions {
			T.SetPermissions(kw_user, folder)
		} else {
			T.folders.Add(1)
		}
		T.CopyFiles(kw_user, folder)

		children, err := T.KW.Session(kw_user).Folder(folder.ID).Folders()
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

func (T *Broker) CreateUser(user string) (err error) {
	// We have not found the user, it's time to create a user at this point.
	if err := T.KW.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/users",
		Params: SetParams(PostJSON{"email": user, "userTypeId": T.standard_profile_id, "verified": true, "sendNotification": T.notify, "active": true}, Query{"returnEntity": false}),
	}); err != nil && !IsAPIError(err, "ERR_ENTITY_EXISTS") {
		return fmt.Errorf("Error initializing user %s: %s", user, err.Error())
	}
	return nil
}


// Creates a manager for the folder.
func (T *Broker) MakeManager(user string) (err error) {
	if user == NONE {
		return 

	}

	var users []KiteUser


	if err := T.KW.DataCall(APIRequest{
		Method: "GET",
		Path:	"/rest/admin/users",
		Params: SetParams(Query{"email": user, "deleted": false}),
		Output: &users,
	}, -1, 1000); err != nil {
		return err
	}

	if len(users) == 0 {
		if err := T.CreateUser(user); err == nil {
			if err := T.KW.DataCall(APIRequest{
				Method: "GET",
				Path:	"/rest/admin/users",
				Params: SetParams(Query{"email": user, "deleted": false}),
				Output: &users,
				}, -1, 1000); err != nil {
					return err
				}
		} else {
			return fmt.Errorf("%s: Could not create user on kiteworks system: %v", user, err)
		}
		if len(users) == 0 {
			return fmt.Errorf("%s does not exist on '%s', cannot use as --manager.", user, T.KW.Server)
		}
	}

	if !users[0].Verified {
		if err := T.KW.Call(APIRequest{
			Method: "PUT",
			Path: SetPath("/rest/admin/users/%d", users[0].ID),
			Params: SetParams(PostJSON{"verified": true}),
		}); err != nil {
			return err
		}
	}

	return 
}

// Creates Paths if Folder is to be created.
func (T *Broker) CreatePaths(user, folder string) (kw_user string, kw_folder *KiteObject, err error) {
	if user == NONE {
		users := T.GetMembers(T.perm_map[folder], OWNER)
		if len(users) == 0 {
			if !T.elevate {
				return NONE, nil, fmt.Errorf("%s: No owner assigned in CSV.", folder)
			} else {
				users = T.GetMembers(T.perm_map[folder], MANAGER)
				if len(users) == 0 {
					return NONE, nil, fmt.Errorf("%s: No owner or managers assigned in CSV.", folder)
				} else {
					Log("%s: Elevating to owner of '%s' as no owner was assigned.", users[0], folder)
				}
			}
		}
		kw_user = users[0]
	} else {
		owner := T.GetMembers(T.perm_map[folder], OWNER)
		if (len(owner) > 0 && strings.ToLower(owner[0]) != strings.ToLower(user)) && !T.elevate {
			return NONE, nil, fmt.Errorf("%s is a manager of %s, but not owner and --elevate is not enabled.", user, folder)
		} else if T.elevate {
			Log("%s: Elevating to owner of '%s'.", user, folder)
		}
		kw_user = user
	}

	if err = T.MakeManager(kw_user); err != nil {
		return kw_user, nil, err
	}


	base, err := T.KW.Session(kw_user).Folder(0).ResolvePath(folder)
	if err != nil {
		return NONE, nil, fmt.Errorf("%s - %v", kw_user, err)
	}

	kw_folder = &base

	kwcache := make(map[string]*KiteObject)

	find_parent := func(folder string) (base_id int, new_folder string) {
		split := strings.Split(folder, "/")
		if len(split) < 2 {
			return 0, folder
		}

		parent := strings.Join(split[0:len(split)-1], "/")
		if v, ok := kwcache[parent]; ok {
			return v.ID, strings.TrimPrefix(folder, fmt.Sprintf("%s/", parent))
		}

		return 0, folder
	}

	for k, _ := range T.perm_map {
		f := strings.Split(k, "/")
		if f != nil && len(f) > 0 {
			if f[0] == folder {
				base_id, new_folder := find_parent(k)
				if kwf, err := T.KW.Session(kw_user).Folder(base_id).ResolvePath(new_folder); err != nil {
					Err("%s: %v", k, err)
				} else {
					kwcache[k] = &kwf
				}
			}
		}
	}

	return 
}

// Find a viable manager for the folder in kiteworks.
func (T *Broker) FindManager(folder string) (kw_user string, kw_folder *KiteObject, err error) {
	fta_managers := T.GetMembers(T.perm_map[folder], OWNER, MANAGER)

	if T.manager != NONE {
		found := false 
		mgr := strings.ToLower(T.manager)
		for _, v := range fta_managers {
			if strings.ToLower(v) == mgr {
				found = true
			}
		}
		if !found {
			return NONE, nil, nil
		} 
		fta_managers = append([]string{T.manager}, fta_managers[0:]...)
	}

	var new_managers []string

	if len(T.user_list) > 0 {
		for _, m := range fta_managers {
			if len(new_managers) > 0 {
				break
			}
			for _, u := range T.user_list {
				if strings.ToLower(u) == strings.ToLower(m) {
					new_managers = []string{u}
					break
				}
			} 
		}
		if len(new_managers) == 0 {
			return NONE, nil, nil
		} else {
			fta_managers = new_managers
		}
	}

	if len(fta_managers) == 0 {
		return NONE, nil, fmt.Errorf("No managers found for specified workspace in csv!")
	}

	var (
		folder_id int
		kw_f      KiteObject
		managers  []string
	)

	for i, u := range fta_managers {
		sess := T.KW.Session(u)
		if result, err := sess.Folder(0).Find(folder); err != nil {
			if err != ErrNotFound && !IsAPIError(err, "INVALID_GRANT", "223")  {
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
				if _, err = sess.Folder(folder_id).Members(); err == nil {
					managers = append(managers, fta_managers[i])
				}
			}	

			kw_f = result
		}
	}

	if len(managers) == 0 {
		var target_user string

		if len(new_managers) > 0 || T.manager != NONE {
			if T.manager == NONE {
				target_user = new_managers[0]
			} else {
				target_user = T.manager
			}
		} else {
			target_user = NONE
		}

		m, new_folder, err := T.CreatePaths(target_user, folder)
		if err != nil {
			return NONE, nil, err
		}
		return m, new_folder, nil
	} else {
		if T.new_folders_only {
			return NONE, nil, nil
		}
		return managers[0], &kw_f, nil
	}
	return NONE, nil, fmt.Errorf("No viable managers were found in kiteworks for folder!")
}

// Update permissions on kiteworks folder.
func (T *Broker) SetPermissions(kw_user string, target KiteObject) {
	T.map_lock.Lock()

	T.kw_perm_map[target.ID] = make(map[string]int)

	fta_map := make(map[string]int)
	for k, v := range fta_map {
		fta_map[k] = v
	}

	if kw_map, found := T.kw_perm_map[target.ParentID]; found {
		for k, v := range kw_map {
			if fta_map[k] == v {
				T.kw_perm_map[target.ID][k] = v
				delete(fta_map, k)
			}
		}
	}

	T.map_lock.Unlock()


	kw_sess := T.KW.Session(kw_user)

	set_perm := func(kw_sess KWSession, users []string, role_id int, target KiteObject) (err error) {
		if len(users) > 0 {
			if (len(users) == 1 && users[0] == NONE) || len(users) == 0 {
				return
			}
			for _, u := range users {
				if found := T.cache.Get("created", u, nil); !found {
					T.CreateUser(u)
					T.cache.Set("created", u, 0)
				} 
			}
			if err := kw_sess.Folder(target.ID).AddUsersToFolder(users, role_id, T.notify, true); err != nil {
				if !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED", "ERR_ENTITY_IS_OWNER", "ERR_ENTITY_USER_HAS_INSUFFICIENT_PERMISSIONS") {
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

func (T *Broker) FileMover() {
	defer T.wg.Done()
	wg := NewLimitGroup(25)
	for {
		msg := <-T.filemover
		if msg == nil {
			wg.Wait()
			return
		} 
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer T.file_count.Add(1)
			var err error
			for i := 0; i < int(T.api.Retries); i++ {
				err = T.UploadFile(msg.user, msg.src, msg.dst)
				if err != nil {
					Warn("(%s) %s: %v (%d/%d)", msg.user, msg.src.Name(), err, i+1, T.api.Retries)
					T.api.TokenStore.Delete(msg.user)
					T.KW.BackoffTimer(uint(i))
				} else {
					return
				}
			}
			Err("(%s) %s: %v", msg.user, msg.src.Name(), err)
		}()

	}
}

func (T *Broker) UploadFile(user string, source *FTAObject, folder *KiteObject) (err error) {
	if folder.ID == 0 {
		Notice("%s: Uploading files to base path is not permitted, ignoring file.", source.Name())
		return nil
	}

	kw_file_info, err := T.KW.Session(user).Folder(folder.ID).Find(source.Name())
	if err != nil && err != ErrNotFound {
		return err
	}

	if kw_file_info.ID > 0 {
		return nil
	}

	file, err := T.api.Session(user).File(source.ID).Download()
	if err != nil { 
		return err
	}
	x := TransferCounter(file, T.transfered.Add)

	_, err = T.KW.Session(user).Upload(source.Name(), source.Size(), source.ModTime(), false, true, false, *folder, x)
	x.Close()
	return
} 

type ftacopy struct {
	user string
	src *FTAObject
	dst *KiteObject
}

func (T *Broker) FindDownloader(folder_path string) (fta_user string, err error) {
	users := T.GetMembers(T.perm_map[folder_path], OWNER, MANAGER, COLLABORATOR)

	if len(users) == 0 {
		return NONE, nil
	}

	Debug("%s: %v", folder_path, T.perm_map[folder_path])
	for _, u := range users {
		_, err := T.Find(T.api.Session(u), folder_path)
		if err != nil && err != ErrNotFound && !IsAPIError(err, "INVALID_GRANT", "223") {
			Debug("(FTA) (%s) %s: Error attempting to find folder: %v", u, folder_path, err)
		} else if err == nil {
			return u, nil
		}
	}
	return NONE, fmt.Errorf("(FTA) %s: Unable to find a suitable downloader for downloading files.", folder_path)
}

// Copies files from FTA to kiteworks.
func (T *Broker) CopyFiles(kw_user string, folder KiteObject) {
	if !T.files {
		return
	}

	fta_user, err := T.FindDownloader(folder.Path)
	if err != nil {
		Err(err)
		return
	}

	// Likely this is a folder we found on kiteworks, but is not in csv.
	if fta_user == NONE {
		fta_user = kw_user
	}

	fta_session := T.api.Session(fta_user)

	result, err := T.Find(fta_session, folder.Path)
	if err != nil {
		Warn("Folder on kiteworks was unable to be refrenced on FTA: (%s): %v", folder.Path, err)
		return
	}
	children, err := fta_session.Workspace(result.ID).Children()
	if err != nil {
		Log("(FTA) %s[%s]: %v", folder.Path, folder.ID, err)
		return
	}
	for i, c := range children {
		if c.Type == "f" {
			T.filemover<-&ftacopy{
				user: kw_user,
				src: &children[i],
				dst: &folder,
			}
		}
	}
}
