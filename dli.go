package main

import (
	"fmt"
	"github.com/cmcoffee/go-logger"
	"os"
	"strconv"
	"strings"
	"time"
	"sync/atomic"
)

const (
	export_activities = 1 << iota
	export_files
	export_emails
)

// dli_export record from kiteworks
type dli_export struct {
	ID        string      `json:"id"`
	StartDate interface{} `json:"startDate"`
	EndDate   interface{} `json:"endDate"`
	Status    string      `json:"status"`
	Filename  string      `json:"fileName"`
	Types     []string    `json:"types"`
}

type DLIRequest struct {
	Account   string                `json:"account"`
	UID       int                   `json:"uid"`
	Flag      int                   `json:"flags"`
	StartTime time.Time             `json:"start_time"`
	EndTime   time.Time             `json:"end_time"`
	Exports   map[string]dli_export `json:"exports"`
}

var ErrNotReady = fmt.Errorf("Export not ready.")
var ErrExportErr = fmt.Errorf("Server error on export.")

// Download DLI export.
func (j *Task) DLIDownload(target dli_export) (err error) {

	s := Session(Config.SGet(j.task_id, "dli_admin_user"))

	err = s.DLICheck(&target)
	if err != nil {
		return err
	}

	switch target.Status {
	case "inprocess":
		return ErrNotReady
	case "error":
		return ErrExportErr
	}

	var f *os.File

	local_path := cleanPath(fmt.Sprintf("%s/%s", Config.SGet(j.task_id, "local_path"), j.session))

	err = MkDir(local_path)
	if err != nil {
		return
	}

	fname := cleanPath(fmt.Sprintf("%s/%s", local_path, target.Filename))
	temp_fname := cleanPath(fmt.Sprintf("%s/%s.dli.incomplete", cleanPath(Config.SGet("configuration", "temp_path")), target.Filename))

	var offset int64

	fstat, err := os.Stat(temp_fname)
	if err == nil || !os.IsNotExist(err) {
		offset = fstat.Size()
	}

	f, err = os.OpenFile(temp_fname, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	req, err := s.NewRequest("GET", fmt.Sprintf("/rest/dli/exports/%s/content", target.ID))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		return
	}
	defer resp.Body.Close()

	if resp.ContentLength == -1 {
		err = s.respError(resp)
		if err != nil {
			return
		}
		logger.Warn("Content-Length header was less than zero.")
	}

	total_size := offset + resp.ContentLength

	tm := NewTMonitor("download", total_size)
	tm.Offset(offset)

	HideLoader()
	logger.Log("[%v]: Downloading %s(%s).\n", j.session, target.Filename, showSize(total_size))
	show_transfer := uint32(1)
	defer atomic.StoreUint32(&show_transfer, 0)

	go func() {
		for atomic.LoadUint32(&show_transfer) == 1 {
			tm.ShowTransfer()
			time.Sleep(time.Second)
		}
	}()

	// Resume transfer if we've already started downloading a file
	if offset > 0 {
		start := resp.Header.Get("Content-Range")
		if start == NONE {
			goto MoveFile
		}
		start = strings.TrimPrefix(start, "bytes")
		byte_range := strings.Split(start, "-")
		start = byte_range[0]
		start = strings.TrimSpace(start)
		offset, err = strconv.ParseInt(start, 10, 64)
		if err != nil {
			return
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			return
		}
	}

	err = Transfer(resp.Body, f, tm)
	if err != nil {
		return
	}

	err = s.DecodeJSON(resp, nil)
	if err != nil {
		return
	}

MoveFile:
	tm.ShowTransfer()
	fmt.Println(NONE)
	logger.Log("[%v]: Download completed succesfully.", j.session)
	ShowLoader()

	// Close the file stream.
	if err = f.Close(); err != nil {
		return
	}

	// Rename file.
	if err = moveFile(temp_fname, fname); err != nil {
		return
	}

	return
}

// Generate DLI Export Request.
func (s Session) DLIGenerateReport(account string, types int, start_time, end_time time.Time) (request *DLIRequest, err error) {

	uid, err := s.FindUser(account)
	if err != nil {
		return nil, err
	}

	var (
		types_str []string
		req_count int
	)

	if types&export_activities == export_activities {
		types_str = append(types_str, "activities")
		req_count++
	}

	if types&export_files == export_files {
		types_str = append(types_str, "files")
		req_count++
	}

	if types&export_emails == export_emails {
		types_str = append(types_str, "emails")
		req_count++
	}

	request = &DLIRequest{
		account,
		uid,
		types,
		start_time,
		end_time,
		make(map[string]dli_export),
	}

	type dli_array struct {
		Records []dli_export `json:"data"`
	}

	var x_call, x_act, x_files, x_mail bool

	var dli_requests dli_array

	// Give it 10 attempts before giving up.
	for i := 0; i < 10; i++ {

		// First check to see if we already have an outstanding request for this user and time period.
		err = s.Call("GET", "/rest/dli/exports", &dli_requests, Query{"user_id": request.UID, "limit": 10, "mode": "full"})
		if err != nil {
			return nil, err
		}

		for _, v := range dli_requests.Records {
			if v.StartDate == write_kw_time(request.StartTime) {
				if v.EndDate == write_kw_time(request.EndTime) {
					for _, t := range v.Types {
						switch t {
						case "activities":
							if types&export_activities == export_activities && !x_act {
								request.Exports["activities"] = v
								x_act = true
								req_count--
							}
						case "files":
							if types&export_files == export_files && !x_files {
								request.Exports["files"] = v
								x_files = true
								req_count--
							}
						case "emails":
							if types&export_emails == export_emails && !x_mail {
								request.Exports["emails"] = v
								x_mail = true
								req_count--
							}
						}
					}
				}
			}
		}

		if req_count == 0 {
			return
		}

		// If we've got this far, but don't have a result, we need to request an export.
		if !x_call {
			err = s.Call("POST", fmt.Sprintf("/rest/dli/exports/users/%d", uid), nil, PostJSON{"startDate": write_kw_time(start_time), "endDate": write_kw_time(end_time), "returnEntity": true, "types": types_str, "mode": "full"})
			if err != nil {
				return nil, err
			}
			x_call = true
		}
	}

	return nil, fmt.Errorf("Something unexpected happened, kiteworks was unable to find our DLI request.")
}

// Remove existing export.
func (s *Session) DeleteExport(export_id string) {
	s.Call("DELETE", fmt.Sprintf("/rest/dli/exports/%s", export_id), nil)
}

// Query status of report
func (s *Session) DLICheck(input *dli_export) (err error) {
	err = s.Call("GET", fmt.Sprintf("/rest/dli/exports/%s", input.ID), &input, Query{"id": input.ID})
	if err != nil {
		return err
	}
	return
}

// Exports DLI Report as requested.
func (j *Task) DLIReport() (err error) {
	s := Session(Config.SGet(j.task_id, "dli_admin_user"))

	var flag []int

	if strings.ToLower(Config.SGet(j.task_id, "export_activities")) == "yes" {
		flag = append(flag, export_activities)
	}
	if strings.ToLower(Config.SGet(j.task_id, "export_emails")) == "yes" {
		flag = append(flag, export_emails)
	}
	if strings.ToLower(Config.SGet(j.task_id, "export_files")) == "yes" {
		flag = append(flag, export_files)
	}

	type Dli_export_record struct {
		Completed   bool      `json:"completed"`
		Export_id   string    `json:"export_id"`
		Start_time  time.Time `json:"start_time"`
		Export_time time.Time `json:"export_time"`
	}

	lastUpdate := make(map[int]Dli_export_record)

	_, err = DB.Get(j.task_id, j.session, &lastUpdate)
	if err != nil {
		return err
	}

	// Set initial date for export.
	for _, n := range flag {
		if lastUpdate[n].Export_time.IsZero() {
			tmp := lastUpdate[n]

			// Check if last_update doesn't
			if tmp.Export_time.IsZero() {
				tmp.Start_time, err = time.Parse("2006-Jan-02", Config.SGet(j.task_id, "start_date"))
				if err != nil {
					return err
				}
			}
			tmp.Completed = true
			lastUpdate[n] = tmp
			err = DB.Set(j.task_id, j.session, &lastUpdate)
			if err != nil {
				return err
			}
		}

		// Attempt to resume a previous export if download got cut short, restart previous export on issue.
		if lastUpdate[n].Export_id != NONE && lastUpdate[n].Completed == false {
			var dli_resume dli_export

			var t_name string

			switch n {
			case export_files:
				t_name = "files"
			case export_activities:
				t_name = "activities"
			case export_emails:
				t_name = "emails"
			}
			logger.Log("[%v]: Resuming previous %s export.", j.session, t_name)
			err := s.Call("GET", fmt.Sprintf("/rest/dli/exports/%s", lastUpdate[n].Export_id), &dli_resume, Query{"id": lastUpdate[n].Export_id})
			errors_found := false
			for {
				if err != nil {
					logger.Err("[%v]: Unable to resume previous %s export. %s", j.session, t_name, err.Error())
					errors_found = true
					break
				}
				err := j.DLIDownload(dli_resume)
				if err != nil && err == ErrNotReady {
					time.Sleep(time.Second * 10)
					err = nil
					continue
				} else if err != nil {
					logger.Err("[%v]: Unable to resume previous %s export. %s", j.session, t_name, err.Error())
					errors_found = true
					break
				} else {
					break
				}
			}
			tmp := lastUpdate[n]
			tmp.Completed = true
			if !errors_found {
				tmp.Start_time = tmp.Export_time
			}
			lastUpdate[n] = tmp
			err = DB.Set(j.task_id, j.session, &lastUpdate)
			if err != nil {
				return err
			}
			s.DeleteExport(lastUpdate[n].Export_id)
		}

		// Generate a new request.
		x, err := s.DLIGenerateReport(string(j.session), n, lastUpdate[n].Start_time, task_time)
		if err != nil {
			return err
		}

		// From report, process all exports.
		for k, v := range x.Exports {
			if strings.Contains(v.Status, "nodata") {
				logger.Log("[%v]: No new %s to export.", j.session, k)
				s.DeleteExport(x.Exports[k].ID)
				continue
			} else {
				logger.Log("[%v]: Processing new %s export.", j.session, k)
				tmp := lastUpdate[n]
				tmp.Completed = false
				tmp.Export_id = x.Exports[k].ID
				tmp.Export_time = task_time
				lastUpdate[n] = tmp
				err = DB.Set(j.task_id, j.session, &lastUpdate)
				if err != nil {
					return err
				}
				for {
					// Loop until we download the export, or error out.
					err := j.DLIDownload(x.Exports[k])
					if err != nil && err == ErrNotReady {
						time.Sleep(time.Second * 10)
						continue
					}
					tmp := lastUpdate[n]
					tmp.Completed = true
					if err == nil {
						tmp.Start_time = task_time
						if next_time, ok := x.Exports[k].EndDate.(string); ok {
							if tmp.Start_time, err = read_kw_time(next_time); err != nil {
								tmp.Start_time = task_time
							}
						}
					}
					tmp.Export_id = NONE
					lastUpdate[n] = tmp
					s.DeleteExport(x.Exports[k].ID)
					if db_err := DB.Set(j.task_id, j.session, &lastUpdate); db_err != nil {
						if err == nil {
							return db_err
						} else {
							logger.Err(db_err)
							return err
						}
					}
					if err != nil {
						return err
					} 
					break
				}
			}
		}
	}
	return
}
