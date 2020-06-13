package main

import (
	"fmt"
	. "github.com/cmcoffee/go-kwlib"
	"time"
	"os"
	"io"
	"sync/atomic"
	"sync"
)

func init() {
	glo.menu.RegisterAdmin("dli_export", "DLI Export Tool", new(DLIExporter))
}

const (
	dli_export_activities = 1 << iota
	dli_export_files
	dli_export_emails
)

type DLIExporter struct {
	start_date time.Time
	exp_activities bool
	exp_emails bool
	exp_files bool
	reset bool
	path string
	files_errs int32
	emails_errs int32
	activities_errs int32
	LastFiles time.Time
	LastActivities time.Time
	LastEmails time.Time
}

type dli_record_user struct {
	Name string
	ID int
	LastFiles time.Time
	LastActivites time.Time
	LastEmais time.Time
}

type dli_record struct {
	LastFiles time.Time
	LastActivities time.Time
	LastEmails time.Time
}

type dli_export struct {
	StartDate string `json:"startDate"`
	EndDate string `json:"endDate"`
	ID string `json:"id"`
	Filename string `json:"fileName"`
	Type string `json:"type"`
	Status string `json:"status"`
}

func (T *DLIExporter) New() task {
	return new(DLIExporter)
}

func (T *DLIExporter) Init(flag *FlagSet) (err error) {
	start_date := flag.String("start_date", "<2020-05-19>", "Starting date for export.")
	//end_date := flag.String("end_date", "<2020-05-20>", "Ending date for export.")
	flag.StringVar(&T.path, "save_to", "<<destination>>", "Destination folder for storing DLI exports.")
	flag.BoolVar(&T.exp_activities, "activities", false, "Export activities for specified users.")
	flag.BoolVar(&T.exp_files, "files", false, "Export files for specified users.")
	flag.BoolVar(&T.exp_emails, "emails", false, "Export emails for specified users.")
	flag.BoolVar(&T.reset, "reset", false, "Reset incremental updates back to start_date.")

	if err := flag.Parse(); err != nil {
		return err
	}

	if T.path == NONE {
		return fmt.Errorf("--save_to: Need to specify path for saving reports.")
	}

	if *start_date != NONE {
		*start_date = fmt.Sprintf("%sT00:00:00Z", *start_date)
		T.start_date, err = time.Parse(time.RFC3339, *start_date)
		if err != nil {
			return err
		}
	}

	if !T.exp_activities && !T.exp_emails && !T.exp_files {
		return fmt.Errorf("You must select at least one item to export: --activities, --files and/or --emails.")
	}



	return nil
}

/* 
status = "inprocess"
		 "nodata"
		 "completed"
		 "deleted"
*/

func (T *DLIExporter) Main() (err error) {
	if T.reset {
		glo.db.Drop("dli_export")
		T.reset = false
	}
	T.files_errs = 0
	T.activities_errs = 0
	T.emails_errs = 0

	var dli_exports dli_record
	glo.db.Get("dli_export", "dli_export", &dli_exports);

	T.LastEmails = dli_exports.LastEmails
	T.LastFiles = dli_exports.LastFiles
	T.LastActivities = dli_exports.LastActivities 

	var start_time time.Time
	var timeSet bool

	if T.exp_files {
		timeSet = true
		start_time = dli_exports.LastFiles
	}

	if T.exp_emails {
		if timeSet {
			if start_time.Unix() > T.LastEmails.Unix() {
				start_time = T.LastEmails
			}
		} else {
			timeSet = true
			start_time = T.LastEmails
		}
	}

	if T.exp_activities {
		if timeSet {
			if start_time.Unix() > T.LastActivities.Unix() {
				start_time = T.LastActivities
			}
		} else {
			timeSet = true
			start_time = T.LastActivities
		}
	}

	if T.start_date.Unix() > start_time.Unix() {
		start_time = T.start_date
	}

	/*if start_time.IsZero() {
		start_time = start_time.AddDate(1969, 0, 0)
	}*/

	now := time.Now().UTC()

	users, err := T.GetUsersByActivity(start_time.UTC(), now)
	if err != nil {
		return err
	}

	if len(users) == 0 {
		return
	}

	var wg sync.WaitGroup
	limiter := make(chan struct{}, 3)

	ProgressBar.New("users", len(users))
	for _, u := range users {
		wg.Add(1)
		limiter<-struct{}{}
		go func(u int, start_time, now time.Time) {
			defer wg.Done()
			defer ProgressBar.Add(1)
			time.Sleep(time.Second * 10)
			defer func () { <-limiter }()
			if err := T.ProcessUser(u, now); err != nil {
				Err(err)
			}

		}(u, start_time, now)
	}

	wg.Wait()
	ProgressBar.Done()

	if T.exp_files && T.files_errs == 0 {
		dli_exports.LastFiles = now
	}
	if T.exp_activities && T.activities_errs == 0 {
		dli_exports.LastActivities = now
	}
	if T.exp_emails && T.emails_errs == 0 {
		dli_exports.LastEmails = now
	}

	glo.db.Set("dli_export", "dli_export", &dli_exports);

	return nil
}

// Delete Export.
func (T *DLIExporter) DeleteExport(export *dli_export) {
	glo.user.Call(APIRequest{
		Method: "DELETE", 
		Path: SetPath("/rest/dli/exports/%s", export.ID),
	})
}

// Query status of report
func (T *DLIExporter) DLICheck(input *dli_export) (err error) {
	err = glo.user.Call(APIRequest{
		Method: "GET", 
		Path: SetPath("/rest/dli/exports/%s", input.ID),
		Params: SetParams(Query{"id": input.ID}),
		Output: &input,
	})
	if err != nil {
		return err
	}
	return
}

func (T *DLIExporter) RegisterError(task string) {
	switch task {
	case "files":
		atomic.StoreInt32(&T.files_errs, 1)
	case "emails":
		atomic.StoreInt32(&T.emails_errs, 1)
	case "activities":
		atomic.StoreInt32(&T.activities_errs, 1)
	}
}

// Process each user.
func (T *DLIExporter) ProcessUser(user_id int, EndDate time.Time) (err error) {
	var exports []dli_export

	if T.exp_emails {
		if T.start_date.Unix() > T.LastEmails.Unix() {
			T.LastEmails = T.start_date
		}
		e, err := T.GenerateReports(user_id, dli_export_emails, T.LastEmails, EndDate)
		if err != nil {
			T.RegisterError("emails")
			return err
		}
		exports = append(exports, e[0:]...)
	}
	if T.exp_activities {
		if T.start_date.Unix() > T.LastActivities.Unix() {
			T.LastActivities = T.start_date
		}
		e, err := T.GenerateReports(user_id, dli_export_activities, T.LastActivities, EndDate)
		if err != nil {
			T.RegisterError("activities")
			return err
		}
		exports = append(exports, e[0:]...)
	}
	if T.exp_files {
		if T.start_date.Unix() > T.LastFiles.Unix() {
			T.LastFiles = T.start_date
		}
		e, err := T.GenerateReports(user_id, dli_export_files, T.LastFiles, EndDate)
		if err != nil {
			T.RegisterError("files")
			return err
		}
		exports = append(exports, e[0:]...)
	}

	cleaner := Defer(func() {
		for _, e := range exports {
			T.DeleteExport(&e)
		}})
	defer cleaner()

	for _, e := range exports {
		if e.Status != "nodata" {
			if err = T.Download(&e); err != nil {
				T.RegisterError(e.Type)
				return err
			}
		}
	}
	return nil
}

/*func (T *DLIExprter) GetUsersByActivity2(StartDate, EndDate time.time) (user_ids []int, err error) {
// Returns User Information
func (s KWSession) GetUsers(limit, offset int) (output []KiteUser, err error) {

	var OutputArray struct {
		Users []KiteUser `json:"data"`
	}

	req := APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/admin/users"),
		Params: SetParams(Query{"limit": limit, "offset": offset, "allowsCollaboration": true}),
		Output: &OutputArray,
	}

	return OutputArray.Users, s.Call(req)

}
} */
 
// Find User Activities
func (T *DLIExporter) GetUsersByActivity(StartDate, EndDate time.Time) (user_ids []int, err error) {
	type user_data struct {
		Name string `json:"name"`
		UserID int  `json:"userId"`
	}

	type activities struct {
		Data []struct {
			User user_data `json:"user"`
		} `json:"data"`
	}

	var offset int
	umap := make(map[int]struct{})

	Log("Searching for activities between %v and %v.", StartDate, EndDate)

	for {
		var output activities
		err = glo.user.Call(APIRequest{
			APIVer: 13,
			Method: "GET",
			Path: "/rest/activities",
			Params: SetParams(Query{"startDate": WriteKWTime(StartDate), "endDate": WriteKWTime(EndDate), "offset": offset, "limit": 1000}),
			Output: &output,
		})
		if err != nil {
			return nil, err
		}
		if output.Data != nil {
			for _, v := range output.Data {
				if _, ok := umap[v.User.UserID]; !ok {
					Log("Found activities for user %s.", v.User.Name)
					user_ids = append(user_ids, v.User.UserID)
					umap[v.User.UserID] = struct{}{}
				}
			}
		}
		if len(output.Data) < 1000 {
			break
		}
		offset = offset + 1000
	}
	if len(user_ids) == 0 {
		Log("No new activities found.")
	}
	return
}

// Generate the DLI Report
func (T *DLIExporter) GenerateReports(user_id int, export_types int, StartDate, EndDate time.Time) (exports []dli_export, err error) {

	var dli_resp struct {
		Data []dli_export `json:"data"`
	}

	var types []string

	f := BitFlag(export_types)

	if f.Has(dli_export_emails) {
		types = append(types, "emails")
	}
	if f.Has(dli_export_files) {
		types = append(types, "files")
	}
	if f.Has(dli_export_activities) {
		types =append(types, "activities")
	}

	err = glo.user.Call(APIRequest{
		Method: "POST",
		Path: SetPath("/rest/dli/exports/users/%d", user_id),
		Params: SetParams(PostJSON{"startDate": WriteKWTime(StartDate), "endDate": WriteKWTime(EndDate), "types": types}, Query{"returnEntity": true}),
		Output: &dli_resp,
	})
	if dli_resp.Data != nil {
		for _, v := range dli_resp.Data {
			exports = append(exports, v)
		}
	}

	return 
}

// Downloads Export
func (T *DLIExporter) Download(export *dli_export) (err error) {
	dest := FormatPath(fmt.Sprintf("%s/%s", T.path, export.Filename))
	tmp := fmt.Sprintf("%s.incomplete", dest)
	var offset int64

	if _, err := os.Stat(dest); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		 Notice("%s: File already exists in '%s'.", export.Filename, T.path)
		 return nil
	}

	if f, err := os.Stat(tmp); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		offset = f.Size()
	}

	in_process := false

	err = T.DLICheck(export)
	if err != nil {
		return err
	}
	for export.Status == "inprocess" {
		if !in_process {
			Log("kiteworks server is preparing %s for export.", export.Filename)
			in_process = true
		}
		time.Sleep(time.Second * 5)
		err = T.DLICheck(export)
		if err != nil {
			return err
		}
	}

	if export.Status == "nodata" {
		return nil
	}

	Log("Starting download of %s.", export.Filename)

	req, err := glo.user.NewRequest("GET", SetPath("/rest/dli/exports/%s/content", export.ID), 7)
	if err != nil {
		return err
	}

	dl := TransferMonitor(export.Filename, -1, true, glo.user.WebDownload(req))
	defer dl.Close()

	_, err = dl.Seek(offset, 0)
	if err != nil {
		return err
	}

	d_file, err := os.OpenFile(tmp, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644) 
	if err != nil {
		return err
	}

	_, err = io.Copy(d_file, dl)
	if err != nil {
		return err
	}

	err = d_file.Close()
	if err != nil {
		return err
	}
	Log("Download of %s completed succesfully!", export.Filename)

	return os.Rename(tmp, dest)
}