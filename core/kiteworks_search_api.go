package core

// KiteSearchResult represents the result of a search query.
type KiteSearchResult struct {
	Files   []KiteObject `json:"files,omitempty"`
	Folders []KiteObject `json:"folders,omitempty"`
	Emails  []KiteMail   `json:"emails,omitempty"`
	ID      string       `json:"id,omitempty"`
	Metadata struct {
		Total             int `json:"total,omitempty"`
		TotalFilesCount   int `json:"totalFilesCount,omitempty"`
		TotalFoldersCount int `json:"totalFoldersCount,omitempty"`
		TotalEmailsCount  int `json:"totalEmailsCount,omitempty"`
	} `json:"metadata,omitempty"`
	FullTextSearch bool `json:"fullTextSearch,omitempty"`
}

// KiteQueryResult represents the result of a query search.
type KiteQueryResult struct {
	Files            []KiteObject `json:"files,omitempty"`
	Folders          []KiteObject `json:"folders,omitempty"`
	Emails           []KiteMail   `json:"emails,omitempty"`
	TotalFiles       int          `json:"totalFiles,omitempty"`
	TotalFolders     int          `json:"totalFolders,omitempty"`
	TotalEmails      int          `json:"totalEmails,omitempty"`
	EmailSuggestions []string     `json:"emailSuggestions,omitempty"`
	FileSuggestions  []string     `json:"fileSuggestions,omitempty"`
}

// Search performs a search using the legacy /rest/search endpoint.
// Params should include Query values for filtering (e.g., content, searchType, objectId).
func (K KWSession) Search(params ...interface{}) (result KiteSearchResult, err error) {
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/search",
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// Query performs a search using the /rest/query endpoint.
// Params should include Query values for filtering (e.g., query, searchType, includeContent).
func (K KWSession) Query(params ...interface{}) (result KiteQueryResult, err error) {
	err = K.Call(APIRequest{
		Method: "GET",
		Path:   "/rest/query",
		Params: SetParams(params),
		Output: &result,
	})
	return
}

// SourceQuery performs a search on a specific data source (e.g., SharePoint sites).
func (s kw_source) Query(params ...interface{}) (result KiteQueryResult, err error) {
	err = s.Call(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/sources/%s/query", s.source_id),
		Params: SetParams(params),
		Output: &result,
	})
	return
}
