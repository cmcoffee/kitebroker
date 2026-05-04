package core

import "time"

// KiteRequestFile represents a file request in Kiteworks.
type KiteRequestFile struct {
	ID        int    `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Remaining int    `json:"remaining,omitempty"`
	FileLimit int    `json:"fileLimit,omitempty"`
	Requestor struct {
		ID    string `json:"id,omitempty"`
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"requestor,omitempty"`
	Recipient struct {
		ID    string `json:"id,omitempty"`
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"recipient,omitempty"`
	Email struct {
		ID      string `json:"id,omitempty"`
		Subject string `json:"subject,omitempty"`
		Body    string `json:"body,omitempty"`
	} `json:"email,omitempty"`
	Expire  string      `json:"expire,omitempty"`
	Status  string      `json:"status,omitempty"`
	Created string      `json:"created,omitempty"`
	Links   []KiteLinks `json:"links,omitempty"`
}

// KiteRequestFileSource represents a source file attached to a request file by the requestor.
type KiteRequestFileSource struct {
	FileID        string `json:"fileId,omitempty"`
	RequestFileID int    `json:"requestFileId,omitempty"`
	ActionID      int    `json:"actionId,omitempty"`
}

// KiteRequestFileUpload represents a file uploaded by the recipient to a request file.
type KiteRequestFileUpload struct {
	FileID        string `json:"fileId,omitempty"`
	RequestFileID int    `json:"requestFileId,omitempty"`
}

// kw_rest_request_file represents a request file accessor for the Kiteworks REST API.
type kw_rest_request_file struct {
	ref string
	*KWSession
}

// RequestFile returns a request file accessor for the given reference.
func (K KWSession) RequestFile(ref string) kw_rest_request_file {
	return kw_rest_request_file{ref, &K}
}

// Info retrieves information about the request file.
func (s kw_rest_request_file) Info(params ...interface{}) (result KiteRequestFile, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/requestFile/%s", s.ref),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// Expire expires the request file by setting its status to deleted.
func (s kw_rest_request_file) Expire() (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/requestFile/%s", s.ref),
	})
}

// Sources retrieves attached files included by the requestor.
func (s kw_rest_request_file) Sources() (result []KiteRequestFileSource, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/requestFile/%s/sources", s.ref),
		Output: &result,
	}, -1, 1000)
	return
}

// SourceInfo retrieves detailed info about a specific source file.
func (s kw_rest_request_file) SourceInfo(object_id string, params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/requestFile/%s/sources/%s", s.ref, object_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// DownloadSource downloads a source file from the request file.
func (s kw_rest_request_file) DownloadSource(object_id string) (output ReadSeekCloser, err error) {
	req, err := s.NewRequest("GET", SetPath("/rest/requestFile/%s/sources/%s/content", s.ref, object_id))
	if err != nil {
		return nil, err
	}
	return s.WebDownload(req), nil
}

// Uploads retrieves files uploaded by the logged-in uploader.
func (s kw_rest_request_file) Uploads() (result []KiteRequestFileUpload, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/requestFile/%s/uploads", s.ref),
		Output: &result,
	}, -1, 1000)
	return
}

// UploadInfo retrieves detailed info about a specific uploaded file.
func (s kw_rest_request_file) UploadInfo(object_id string, params ...interface{}) (result KiteObject, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/requestFile/%s/uploads/%s", s.ref, object_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// DeleteUpload deletes a specific uploaded file from the request.
func (s kw_rest_request_file) DeleteUpload(object_id string) (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/requestFile/%s/uploads/%s", s.ref, object_id),
	})
}

// DownloadUpload downloads an uploaded file from the request file.
func (s kw_rest_request_file) DownloadUpload(object_id string) (output ReadSeekCloser, err error) {
	req, err := s.NewRequest("GET", SetPath("/rest/requestFile/%s/uploads/%s/content", s.ref, object_id))
	if err != nil {
		return nil, err
	}
	return s.WebDownload(req), nil
}

// InitiateUpload initiates a chunked upload session for the request file.
// Returns the upload ID to be used with KWSession.uploadFile for chunked transfer.
func (s kw_rest_request_file) InitiateUpload(filename string, size int64, mod_time time.Time, params ...interface{}) (int, error) {
	var upload struct {
		ID int `json:"id"`
	}

	if err := s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/requestFile/%s/actions/initiateUpload", s.ref),
		Params: SetParams(PostJSON{"filename": filename, "totalSize": size, "clientModified": WriteKWTime(mod_time.UTC()), "totalChunks": s.chunksCalc(size)}, Query{"returnEntity": true}, params),
		Output: &upload,
	}); err != nil {
		return -1, err
	}
	return upload.ID, nil
}

// Upload uploads a file to the request file using the chunked upload flow.
func (s kw_rest_request_file) Upload(filename string, size int64, mod_time time.Time, src ReadSeekCloser, params ...interface{}) (file *KiteObject, err error) {
	uid, err := s.InitiateUpload(filename, size, mod_time, params...)
	if err != nil {
		return nil, err
	}
	return s.uploadFile(filename, uid, src)
}

// AddComment adds a comment to a file within the request file.
func (s kw_rest_request_file) AddComment(object_id string, contents string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/requestFile/%s/comment/%s", s.ref, object_id),
		Params: SetParams(PostJSON{"contents": contents}),
	})
}

// Reply sends a reply to the request file.
func (s kw_rest_request_file) Reply(body string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/requestFile/%s/reply", s.ref),
		Params: SetParams(PostJSON{"body": body}),
	})
}

// CreateRequestFileToFolder creates a request file link for a folder.
// Recipients will upload files to the specified folder.
func (s kw_rest_folder) CreateRequestFile(params ...interface{}) (result KiteRequestFile, err error) {
	err = s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/folders/%s/actions/requestFile", s.folder_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// CreateRequestFileToInbox creates a request file link to the user's inbox.
// Recipients will upload files to the requestor's inbox.
func (K KWSession) CreateRequestFileToInbox(params ...interface{}) (result KiteRequestFile, err error) {
	err = K.Call(APIRequest{
		Method: "POST",
		Path:   "/rest/mail/actions/requestFile",
		Params: SetParams(params),
		Output: &result,
	})
	return
}
