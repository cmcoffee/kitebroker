package FTA

import (
	"encoding/base64"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// base64 decoder
func b64decode(input string) string {
	if input == NONE {
		return NONE
	}
	str, _ := base64.StdEncoding.DecodeString(input)
	return string(str)
}

// base64 encoder
func b64encode(input string) string {
	if input == NONE {
		return NONE
	}
	return base64.StdEncoding.EncodeToString([]byte(input))
}

// Wrapper for FTA workspace
type fta_rest_workspace struct {
	workspace_id string
	*FTASession
}

// Wrapper for FTA workspace file
type fta_rest_file struct {
	file_id string
	*FTASession
}

func (s FTASession) TestUser(username string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   "/seos/account/get_profile",
	})
}

// Wrapper for FTA workspace file
func (s FTASession) File(file_id string) fta_rest_file {
	return fta_rest_file{
		file_id,
		&s,
	}
}

// Wrapper for FTA workspace
func (s FTASession) Workspace(workspace_id string) fta_rest_workspace {
	return fta_rest_workspace{
		workspace_id,
		&s,
	}
}

func (T Broker) Find(s *FTASession, workspace string) (result FTAObject, err error) {
	workspaces := SplitPath(workspace)

	current, err := s.Workspace(NONE).Children()
	if err != nil {
		return
	}

	// Need to look for nested workspaces from users who weren't added at the top-level.
	if len(workspaces) > 1 {
		for _, v := range current {
			found := false
			if v.Path == workspace {
				return v, nil
			}
			split_path := strings.Split(v.Path, "/")
			for i, p := range split_path {
				if workspaces[0] == p {
					if len(workspaces) > 1 {
						found = true
						workspaces = append(workspaces[:0], workspaces[1:]...)
					} else if i == len(split_path) - 1 {
							return v, nil
					}
				}
			}
			if found {
				current, err = s.Workspace(v.ID).Children()
				if err != nil {
					return
				}
				break
			}
		}
	}

	// Search for workspace path.
	for i, v := range workspaces {
		found := false
		for _, ws := range current {
			if ws.Name() == v {
				if i == len(workspaces)-1 {
					return ws, nil
				}
				current, err = s.Workspace(ws.ID).Children()
				if err != nil {
					return
				}
				found = true
				break
			}
		}
		if found == false {
			break
		}
	}

	err = fmt.Errorf("Requested item not found.")
	return
}

// Get all items in workspace, folders and files.
func (f fta_rest_workspace) Children(params ...interface{}) (children []FTAObject, err error) {
	var offset int

	for {
		var WS struct {
			Result struct {
				ItemList struct {
					Folders []FTAObject `json:"ws_list"`
					Files   []FTAObject `json:"file_list"`
				} `json:"item_list"`
			} `json:"result"`
		}

		err = f.Call(APIRequest{
			Method: "POST",
			Path:   "/seos/workspaces/list",
			Output: &WS,
			Params: SetParams(params, PostForm{"id": f.workspace_id, "return_fields": "id,name,description,file_handle,size,owner,creator,parent_id,last_update_time,path", "return_items": "all", "order_by": "id", "order_type": "asc", "offset": offset, "limit": 20}),
		})
		if err != nil {
			return
		}
		for _, v := range WS.Result.ItemList.Folders {
			v.Type = "d"
			v.Desc = b64decode(v.Desc)
			v.Creator = b64decode(v.Creator)
			children = append(children, v)
			offset++
		}
		for _, v := range WS.Result.ItemList.Files {
			v.Type = "f"
			v.Desc = b64decode(v.Desc)
			v.FileHandle = b64decode(v.FileHandle)
			v.Creator = b64decode(v.Owner)
			children = append(children, v)
			offset++
		}
		if len(WS.Result.ItemList.Files) < 20 && len(WS.Result.ItemList.Folders) < 20 {
			break
		}
	}
	return
}

// Get all users of workspace.
func (f fta_rest_workspace) Users(params ...interface{}) (users []FTAUser, err error) {
	var WS struct {
		Result struct {
			Users []struct {
				Name     string      `json:"name`
				UserID   string      `json:"user_id"`
				UserType interface{} `json:"user_type"`
			} `json:"users"`
		} `json:"result"`
	}

	err = f.Call(APIRequest{
		Method: "POST",
		Path:   "/seos/wsusers/list",
		Output: &WS,
		Params: SetParams(params, PostForm{"id": f.workspace_id}),
	})
	if err != nil {
		return
	}
	for _, u := range WS.Result.Users {
		var utype int
		switch v := u.UserType.(type) {
		case int:
			utype = v
		case string:
			utype, err = strconv.Atoi(v)
			if err != nil {
				return
			}
		}
		users = append(users, FTAUser{
			Name:     u.Name,
			UserID:   u.UserID,
			UserType: utype,
		})
	}
	return
}

type FTAUser struct {
	Name     string `json:"name`
	UserID   string `json:"user_id"`
	UserType int    `json:"user_type"`
}

type FTAObject struct {
	ID             string `json:"id"`
	RawName        string `json:"name"`
	Desc           string `json:"description"`
	FileHandle     string `json:"file_handle"`
	ParentID       string `json:"parent_id"`
	SizeStr        string `json:"size"`
	Type           string `json:"result_type"`
	Owner          string `json:"owner"`
	Creator        string `json:"creator"`
	ModTimeStr     string `json:"last_update_time"`
	Path           string `json:"path"`
}

func (T FTAObject) Name() string {
	if T.Type == "f" {
		return b64decode(T.RawName)
	}
	return T.RawName
}

func (T FTAObject) Size() int64 {
	i, _ := strconv.ParseInt(T.SizeStr, 10, 64)
	return i
}

func (T FTAObject) ModTime() time.Time {
	time_split := strings.Split(T.ModTimeStr, " ")
	t, _ := time.Parse(time.RFC3339, fmt.Sprintf("%sZ", strings.Join(time_split,"T")))
	return t
}

// Get all comments for workspace file.
func (F fta_rest_file) Comments() (comments []FTAComment, err error) {
	var offset int

	for {
		var C struct {
			Result struct {
				ItemList []FTAComment `json:"item_list"`
			} `json:"result"`
		}
		err = F.Call(APIRequest{
			Method: "POST",
			Path:   "/seos/wscomments/list",
			Output: &C,
			Params: SetParams(PostForm{"id": F.file_id, "include_replies": 1, "return_fields": "id,user_id,create_time,comment,reply_to,replies,number_of_replies", "offset": offset, "limit": 20}),
		})
		if err != nil {
			return
		}
		for _, v := range C.Result.ItemList {
			v.Comment = b64decode(v.Comment)
			for i, r := range v.Replies {
				v.Replies[i].Comment = b64decode(r.Comment)
			}
			comments = append(comments, v)
			offset++
		}
		if offset < 20 {
			break
		}
	}
	return
}

type FTAComment struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	Comment    string `json:"comment"`
	CreateTime string `json:"create_time"`
	Replies    []struct {
		ID         string `json:"id"`
		UserID     string `json:"user_id"`
		Comment    string `json:"comment"`
		CreateTime string `json:"create_time"`
	} `json:"replies"`
}

// Download workspace file.
func (F fta_rest_file) Download() (ReadSeekCloser, error) {
	header, err := F.chunk_header(NONE)
	if err != nil {
		return nil, err
	}

	var reqs []*http.Request

	if header.Subfiles == 0 {
		if req, err := F.generate_download_req(F.file_id, header.FileHandle, 0, 0); err != nil {
			return nil, err
		} else {
			if req != nil {
				reqs = append(reqs, req)
			}
		}
	} else {
		for i := int64(1); i <= header.Subfiles; i++ {
			if req, err := F.generate_download_req(F.file_id, header.FileHandle, i, header.Subfiles); err != nil {
				return nil, err
			} else {
				if req != nil {
					reqs = append(reqs, req)
				}
			}
		}
	}

	return F.FTAClient.Download(reqs[0], reqs[1:]...), nil
}

// Gather information about the file from FTA.
func (F fta_rest_file) chunk_header(file_handle string) (finfo *FTAFinfo, err error) {
	for i := 0; i < int(F.Retries); i++ {
		req, err := F.NewRequest("GET", "/seos/wsfiles/download")
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		q := req.URL.Query()
		if token, err := F.GetToken(F.Username); err == nil {
			q.Add("oauth_token", token.AccessToken)
		} else {
			return nil, err
		}
		q.Add("id", F.file_id)
		if file_handle != NONE {
			q.Add("file_handle", b64encode(file_handle))
		}
		req.Header.Del("Authorization")
		req.Header.Set("User-Agent", "AFetcher")
		req.URL.RawQuery = q.Encode()
		resp, err := F.SendRequest(F.Username, req)
		if err != nil {
			Debug(err)
			F.TokenStore.Delete(F.Username)
			F.BackoffTimer(uint(i))
			continue
		}

		finfo = new(FTAFinfo)
		if resp.StatusCode == 200 && resp.Header != nil {
			if sz_str := resp.Header.Get("Esize"); sz_str != NONE {
				finfo.Size, _ = strconv.ParseInt(sz_str, 0, 64)
			}
			if subfile := resp.Header.Get("Subfile"); subfile != NONE {
				finfo.Subfiles, _ = strconv.ParseInt(subfile, 0, 64)
			}
			if file_handle := resp.Header.Get("File_handle"); file_handle != NONE {
				finfo.FileHandle = file_handle
			}
		}
		return finfo, err
	}
	return nil, fmt.Errorf("Error obtaining download headers for file.")
}

// Create a download request for FTA.
func (F fta_rest_file) generate_download_req(id string, file_handle string, subfile, total_subfiles int64) (*http.Request, error) {
	req, err := F.NewRequest("GET", "/seos/wsfiles/download")
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	q := req.URL.Query()
	if token, err := F.GetToken(F.Username); err == nil {
		q.Add("oauth_token", token.AccessToken)
	} else {
		return nil, err
	}
	req.Header.Del("Authorization")
	q.Add("id", F.file_id)
	if subfile == 0 {
		q.Add("file_handle", b64encode(file_handle))
	} else {
		sub_file_handle := fmt.Sprintf("%s/subfiles-%d-%d", file_handle, subfile, total_subfiles)
		q.Add("file_handle", b64encode(sub_file_handle))
		sub_header, err := F.chunk_header(sub_file_handle)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Size", fmt.Sprintf("%d", sub_header.Size))
	}
	req.URL.RawQuery = q.Encode()
	return req, nil
}

type FTAFinfo struct {
	Size       int64
	FileHandle string
	Subfiles   int64
}
