package core

import (
	"os"
)

type FileCache struct {
	DB Database
}

type file_cache_record struct {
	Size     int64
	Modified int64
}

func (F *FileCache) CacheFolder(sess KWSession, folder *KiteObject) (err error) {
	if F.DB == nil {
		F.DB = OpenCache()
	}

	if F.Exists(folder) {
		return
	}

	type FileInfo struct {
		Type           string `json:"type"`
		Name           string `json:"name"`
		Size           int64  `json:"size"`
		ClientModified string `json:"clientModified"`
	}

	var folder_info []FileInfo

	err = sess.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/children", folder.ID),
		Output: &folder_info,
		Params: SetParams(Query{"deleted": false, "with": "(path,currentUserRole)"}),
	}, -1, 1000)

	if err != nil {
		return
	}

	for _, v := range folder_info {
		if v.Type == "f" {
			mod_time, _ := ReadKWTime(v.ClientModified)
			record := &file_cache_record{
				Size:     v.Size,
				Modified: mod_time.Unix(),
			}
			F.DB.Set(folder.ID, v.Name, &record)
		}
	}

	return
}

func (F *FileCache) Exists(folder *KiteObject) bool {
	if F.DB == nil {
		F.DB = OpenCache()
		return false
	}
	for _, v := range F.DB.Tables() {
		if v == folder.ID {
			return true
		}
	}
	return false
}

func (F *FileCache) Check(finfo os.FileInfo, folder *KiteObject) bool {
	if F.DB == nil {
		F.DB = OpenCache()
		return false
	}
	var record file_cache_record
	if F.DB.Get(folder.ID, finfo.Name(), &record) == false {
		return false
	}

	if record.Modified == finfo.ModTime().Unix() && record.Size == finfo.Size() {
		return true
	}

	return false
}

func (F *FileCache) Drop(folder *KiteObject) {
	if F.DB == nil {
		F.DB = OpenCache()
	}
	F.DB.Drop(folder.ID)
}
