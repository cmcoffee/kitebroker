package core

// KiteComment represents a comment on a file or folder.
type KiteComment struct {
	ID       int    `json:"id"`
	ParentID int    `json:"parentId,omitempty"`
	ObjectID string `json:"objectId,omitempty"`
	FolderID string `json:"folderId,omitempty"`
	UserID   string `json:"userId,omitempty"`
	Created  string `json:"created,omitempty"`
	Modified string `json:"modified,omitempty"`
	Contents string `json:"contents,omitempty"`
	User     struct {
		ID    string `json:"id,omitempty"`
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"user,omitempty"`
}

// kw_rest_comment represents a comment accessor for the Kiteworks REST API.
type kw_rest_comment struct {
	comment_id int
	*KWSession
}

// Comment returns a comment accessor for the given comment ID.
func (K KWSession) Comment(comment_id int) kw_rest_comment {
	return kw_rest_comment{comment_id, &K}
}

// Info retrieves information about the comment.
func (s kw_rest_comment) Info(params ...interface{}) (result KiteComment, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/comments/%d", s.comment_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// Update updates the text of the comment.
func (s kw_rest_comment) Update(contents string) (err error) {
	return s.Call(APIRequest{
		Method: "PUT",
		Path:   SetPath("/rest/comments/%d", s.comment_id),
		Params: SetParams(PostJSON{"contents": contents}),
	})
}

// Delete deletes the comment.
func (s kw_rest_comment) Delete() (err error) {
	return s.Call(APIRequest{
		Method: "DELETE",
		Path:   SetPath("/rest/comments/%d", s.comment_id),
	})
}

// Permissions retrieves the permissions available on the comment.
func (s kw_rest_comment) Permissions(params ...interface{}) (result []KitePermission, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/permissions/comment/%d", s.comment_id),
		Params: SetParams(params),
		Output: &result,
	}, -1, 1000)
	return
}

// Comments retrieves comments on the file.
func (s kw_rest_file) Comments(params ...interface{}) (result []KiteComment, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/files/%s/comments", s.file_id),
		Params: SetParams(params),
		Output: &result,
	}, -1, 1000)
	return
}

// AddCommentReply adds a reply to an existing comment on the file.
func (s kw_rest_file) AddCommentReply(parent_id int, contents string) (err error) {
	return s.Call(APIRequest{
		Method: "POST",
		Path:   SetPath("/rest/files/%s/comments", s.file_id),
		Params: SetParams(PostJSON{"parentId": parent_id, "contents": contents}),
	})
}

// Comments retrieves comments on the folder.
func (s kw_rest_folder) Comments(params ...interface{}) (result []KiteComment, err error) {
	err = s.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/comments", s.folder_id),
		Params: SetParams(params),
		Output: &result,
	}, -1, 1000)
	return
}
