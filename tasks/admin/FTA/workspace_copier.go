package FTA

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"strconv"
	"strings"
	"time"
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
	FTA_VIEW_AND_DOWNLOAD = 0
	FTA_COLLABORATOR      = 1
	FTA_MANAGER           = 2
	FTA_UPLOADER          = 3
)

// Uploads file to folder and adds comments.
func (T Broker) UploadFile(kw_user string, fta_user string, target *KiteObject, file FTAObject) {
	if !strings.HasPrefix(target.Path, "My Folder") {
		kw_user = T.ppt.Username
	}

	file.LastUpdateTime = strings.Replace(file.LastUpdateTime, " ", "T", 1)
	file.LastUpdateTime = fmt.Sprintf("%sZ", file.LastUpdateTime)

	t, err := time.Parse(time.RFC3339, file.LastUpdateTime)
	if err != nil {
		Err("%s[%s]: (%s) %v", file.Name, file.ID, file.LastUpdateTime, err)
		return
	}

	kw_sess := T.ppt.Session(kw_user)

	var upload struct {
		ID int `json:"id"`
	}

	record_id := fmt.Sprintf("%s.%s.%d.%d", file.ID, kw_sess.Username, t.Unix(), target.ID)

	// Look for a previous upload to see if we can resume.
	if found := T.ppt.Get("uploads", record_id, &upload); !found {
		size, err := strconv.ParseInt(file.Size, 0, 64)
		if err != nil {
			Err("%s[%s]: (%s) %v", file.Name, file.ID, file.Size, err)
			return
		}

		if err := kw_sess.Call(APIRequest{
			//Version: 5,
			Method: "POST",
			Path:   SetPath("/rest/folders/%d/actions/initiateUpload", target.ID),
			Params: SetParams(PostJSON{"filename": file.Name, "totalSize": size, "clientModified": WriteKWTime(t), "totalChunks": kw_sess.Chunks(size)}, Query{"returnEntity": true}),
			Output: &upload,
		}); err != nil {
			Err("%s[%d]: %v", target, target.ID, err)
			return
		}
		T.ppt.Set("uploads", record_id, &upload)
	}

	dl, err := T.api.Session(fta_user).File(file.ID).Download()
	if err != nil {
		Err("%s[%s]: (%s) %v", file.Name, file.ID, file.Size, err)
		return
	}

	_, err = kw_sess.Upload(file.Name, upload.ID, dl)
	if err != nil {
		Err("%s[%d]: %v", target.Path, target.ID, err)
		T.ppt.Unset("uploads", record_id)
		return
	}

	T.ppt.Unset("uploads", record_id)
	comments, err := T.api.Session(fta_user).File(file.ID).Comments()
	if err != nil {
		Err("%s[%s]: %v", file.Name, file.ID, err)
		return
	}

	for _, comment := range comments {
		Log(comment)
	}

	return
}

// Sets permission on destination kiteworks folder.
// Takes the FTA permissions and maps them to kiteworks folder permissions, does not apply permissions if permissions in previous folder.
func (T Broker) SetPermissions(kw_user string, fta_user string, target *KiteObject, source *FTAObject) (err error) {
	T.perm_map_lock.Lock()
	defer T.perm_map_lock.Unlock()

	users, err := T.api.Session(fta_user).Workspace(source.ID).Users()
	if err != nil {
		return err
	}

	fta_map := make(map[string]int)

	if !strings.HasPrefix(target.Path, "My Folder") {
		fta_map[strings.ToLower(T.ppt.Username)] = COLLABORATOR
	}

	for _, u := range users {
		if strings.ToLower(u.UserID) == strings.ToLower(kw_user) {
			continue
		}
		switch u.UserType {
		case FTA_VIEW_AND_DOWNLOAD:
			fta_map[u.UserID] = DOWNLOADER
		case FTA_COLLABORATOR:
			fta_map[u.UserID] = COLLABORATOR
		case FTA_MANAGER:
			fta_map[u.UserID] = MANAGER
		case FTA_UPLOADER:
			fta_map[u.UserID] = UPLOADER
		}
	}

	if _, found := T.perm_map[target.ID]; !found {
		T.perm_map[target.ID] = make(map[string]int)
	}

	if kw_map, found := T.perm_map[target.ParentID]; found {
		for k, v := range fta_map {
			if kw_map[k] == v {
				delete(fta_map, k)
				T.perm_map[target.ID][k] = v
			}
		}
	}

	kw_map := make(map[int][]string)

	for k, v := range fta_map {
		kw_map[v] = append(kw_map[v], k)
	}

	for k, v := range kw_map {
		err = T.ppt.Session(kw_user).Folder(target.ID).AddUsersToFolder(v, k)
		if err != nil && !IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED", "ERR_ENTITY_IS_OWNER") {
			Err(err)
		} else {
			for _, u := range v {
				T.perm_map[target.ID][u] = k
			}
		}
	}

	return nil
}

func (T Broker) ProcessWorkspace(kw_user string, fta_user string, dst *KiteObject, src *FTAObject) {

	kw_sess := T.ppt.Session(kw_user)
	fta_sess := T.api.Session(fta_user)

	type CPT struct {
		target *KiteObject
		source *FTAObject
	}

	var (
		current []CPT
		next    []CPT
		n       int
	)

	current = append(current, CPT{dst, src})

	for {
		if n > len(current)-1 {
			current = current[0:0]
			if len(next) > 0 {
				current = next[0:]
			} else {
				break
			}
			next = next[0:0]
			n = 0
			if len(current) == 0 {
				break
			}
		}

		source := current[n].source
		target := current[n].target

		children, err := fta_sess.Workspace(source.ID).Children()
		if err != nil {
			Err("%s[%s]: %s", source.Name, source.ID, err.Error())
			n++
			continue
		}

		var file_list []FTAObject

		for i, child := range children {
			switch child.Type {
			case "d":
				folder, err := kw_sess.Folder(target.ID).Find(child.Name, SetParams(Query{"deleted": false}))
				if err != nil {
					if err == ErrNotFound {
						folder, err = kw_sess.Folder(target.ID).NewFolder(child.Name)
						if err != nil {
							if IsAPIError(err, "ERR_INPUT_FOLDER_NAME_INVALID_START_END_FORMAT") {
								Notice("%s: %v", source.Name, err)
							} else {
								Err("%s[%d]: %v", target.Path, target.ID, err)
							}
							continue
						}
					} else {
						Err("%s[%d]: %v", target.Path, target.ID, err)
						continue
					}
				}
				child.full_path = fmt.Sprintf("%s/%s", source.full_path, child.Name)
				next = append(next, CPT{&folder, &children[i]})
			case "f":
				file_list = append(file_list, children[i])
			}
		}
		T.SetPermissions(kw_user, fta_user, target, source)
		kw_files, err := kw_sess.Folder(target.ID).Files()
		if err != nil {
			Err("%s[%d]: %v", target.Path, target.ID, err)
			n++
			continue
		}
		kw_file_map := make(map[string]struct{})
		for _, f := range kw_files {
			kw_file_map[strings.ToLower(f.Name)] = struct{}{}
		}
		for _, f := range file_list {
			if _, found := kw_file_map[strings.ToLower(f.Name)]; !found {
				T.UploadFile(kw_user, fta_user, target, f)
			}
		}
		n++
		continue
	}

	return
}
