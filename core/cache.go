package core

import (
	"os"
)

// FileCache provides a cache for file information.
type FileCache struct {
	DB Database
}

// file_cache_record stores file size and modification time.
type file_cache_record struct {
	Size     int64
	Modified int64
}

// CacheFolder caches file information for a given folder.
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

// Exists checks if a folder exists in the cache.
// It returns true if the folder is present, false otherwise.
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

// Check verifies if a file's information is already cached.
// It returns true if the file exists in the cache and its
// size and modification time match, false otherwise.
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

// Drop removes the cache entry for the given folder.
func (F *FileCache) Drop(folder *KiteObject) {
	if F.DB == nil {
		F.DB = OpenCache()
	}
	F.DB.Drop(folder.ID)
}
