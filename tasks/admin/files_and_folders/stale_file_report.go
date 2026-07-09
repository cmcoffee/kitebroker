package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterAdminTask(new(StaleFileReportTask)) }

// StaleFileReportTask walks user-owned folders and identifies files that have
// had no activity (uploads, version updates, views, downloads, or any other
// event) within --max_days. It produces a CSV report of removal candidates.
//
// The report intentionally does not delete anything; the resulting CSV is
// designed to be fed into a separate deletion task. The "File ID" column is the
// identifier required to act on each file.
type StaleFileReportTask struct {
	input struct {
		all_users   bool
		user_emails []string
		folders     []string
		profile_id  int
		max_days    int
		output      string
		no_fallback bool
	}
	cutoff          time.Time
	csv_writer      *csv.Writer
	csv_file        *os.File
	locker          sync.Mutex
	limiter         LimitGroup
	user_count      Tally
	folders_count   Tally
	files_count     Tally
	stale_count     Tally
	stale_size      Tally
	fallback_count  Tally
	debug_dumped    bool
	KiteBrokerTask
}

func (T StaleFileReportTask) Name() string {
	return "stale_file_report"
}

func (T StaleFileReportTask) Desc() string {
	return "Files & Folders:Report files with no activity within a specified number of days."
}

func (T *StaleFileReportTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "User(s) whose folders should be scanned.")
	T.Flags.MultiVar(&T.input.folders, "folders", "<My Folder>", "Limit scan to specific top-level folder(s).")
	T.Flags.IntVar(&T.input.profile_id, "profile_id", 0, "Target Profile ID.")
	T.Flags.BoolVar(&T.input.all_users, "all_users", "Scan folders for all users.")
	T.Flags.IntVar(&T.input.max_days, "max_days", 90, "Flag files with no activity within this many days.")
	T.Flags.StringVar(&T.input.output, "output", "", "Output CSV filename (default: Stale-Files-Report-<timestamp>.csv).")
	T.Flags.BoolVar(&T.input.no_fallback, "no_fallback", "Skip per-file activity lookups; rely only on folder activities and modified time.")
	run := T.Flags.Bool("run", "Execute the task.")
	T.Flags.Order("users", "folders", "profile_id", "all_users", "max_days", "output", "no_fallback", "run")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if !*run {
		return fmt.Errorf("Please specify --run to execute this task.")
	}

	if T.input.max_days < 0 {
		return fmt.Errorf("--max_days must be 0 or greater.")
	}

	if !T.input.all_users && T.input.profile_id < 1 && len(T.input.user_emails) == 0 {
		return fmt.Errorf("Select users based on --all_users, --profile_id or --users.")
	}

	return
}

func (T *StaleFileReportTask) Main() (err error) {
	T.limiter = NewLimitGroup(50)
	T.cutoff = time.Now().UTC().AddDate(0, 0, -T.input.max_days)

	T.user_count = T.Report.Tally("Users Analyzed")
	T.folders_count = T.Report.Tally("Folders Analyzed")
	T.files_count = T.Report.Tally("Files Analyzed")
	T.fallback_count = T.Report.Tally("File Activity Lookups")
	T.stale_count = T.Report.Tally("Stale Files Found")
	T.stale_size = T.Report.Tally("Reclaimable Storage", HumanSize)

	// Open the CSV report and write the header.
	filename := T.input.output
	if filename == "" {
		filename = fmt.Sprintf("Stale-Files-Report-%d.csv", time.Now().Unix())
	}
	T.csv_file, err = os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer T.csv_file.Close()
	T.csv_writer = csv.NewWriter(T.csv_file)
	defer T.csv_writer.Flush()

	header := []string{
		"Owner",
		"File ID",
		"File Name",
		"File Path",
		"Size (bytes)",
		"Size",
		"Created (GMT)",
		"Modified (GMT)",
		"Last Activity (GMT)",
		"Last Activity Event",
		"Days Idle",
		"Determined By",
	}
	if err = T.writeRecord(header); err != nil {
		return err
	}

	message := func() string {
		return fmt.Sprintf("Working .. [Files Analyzed: %d/Stale: %d(%s)]", T.files_count.Value(), T.stale_count.Value(), HumanSize(T.stale_size.Value()))
	}
	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	params := Query{"active": true, "verified": true, "allowsCollaboration": true}

	user_getter, err := T.KW.Admin().Users(T.input.user_emails, T.input.profile_id, params)
	if err != nil {
		return err
	}

	for {
		users, err := user_getter.Next()
		if err != nil {
			return err
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			T.user_count.Add(1)
			if user.Suspended || user.Deactivated || !user.Verified || !user.Active {
				Notice("%s: account is not currently active, skipping user.", user.Email)
				continue
			}

			sess := T.KW.Session(user.Email)

			var folders []*KiteObject
			if len(T.input.folders) == 0 {
				var top []KiteObject
				if err := sess.DataCall(APIRequest{
					Method: "GET",
					Path:   "/rest/folders/top",
					Params: SetParams(Query{"deleted": false, "with": "(path,currentUserRole)"}),
					Output: &top,
				}, -1, 1000); err != nil {
					Err("%s: %v", user.Email, err)
					continue
				}
				for i := range top {
					folders = append(folders, &top[i])
				}
			} else {
				for _, v := range T.input.folders {
					f, err := sess.Folder("0").Find(v)
					if err != nil {
						Err("%s: [%s]: %v", user.Email, v, err)
						continue
					}
					folders = append(folders, &f)
				}
			}

			Log("Scanning folders owned by %s ..", user.Email)
			for _, v := range folders {
				// Only process folders this user owns (role id 5 == owner).
				if v.CurrentUserRole.ID != 5 {
					continue
				}
				T.limiter.Add(1)
				go func(sess KWSession, user KiteUser, folder *KiteObject) {
					defer T.limiter.Done()
					T.ProcessFolder(&sess, &user, folder)
				}(sess, user, v)
			}
			T.limiter.Wait()
		}
	}

	T.limiter.Wait()
	T.csv_writer.Flush()
	if err = T.csv_writer.Error(); err != nil {
		return err
	}
	Log("Report written to %s [%d stale file(s), %s reclaimable].", filename, T.stale_count.Value(), HumanSize(T.stale_size.Value()))
	return nil
}

// writeRecord writes a single CSV record under lock and flushes.
func (T *StaleFileReportTask) writeRecord(record []string) error {
	T.locker.Lock()
	defer T.locker.Unlock()
	if err := T.csv_writer.Write(record); err != nil {
		return err
	}
	T.csv_writer.Flush()
	return T.csv_writer.Error()
}

// ProcessFolder walks a folder tree, scanning files in each folder for staleness.
// It mirrors the concurrent breadth-first walk used by the file_cleanup task.
func (T *StaleFileReportTask) ProcessFolder(sess *KWSession, user *KiteUser, folder *KiteObject) {
	var folders []*KiteObject
	folders = append(folders, folder)

	var n int
	var next []*KiteObject

	for {
		if len(folders) < n+1 {
			folders = folders[0:0]
			if len(next) > 0 {
				for i, o := range next {
					if T.limiter.Try() {
						go func(folder *KiteObject) {
							defer T.limiter.Done()
							T.ProcessFolder(sess, user, folder)
						}(o)
					} else {
						folders = append(folders, next[i])
					}
				}
				next = next[0:0]
				n = 0
				if len(folders) == 0 {
					break
				}
			} else {
				break
			}
		}

		if folders[n].Type == "d" {
			T.folders_count.Add(1)
			activity := T.folderActivityMap(sess, folders[n])
			childs, err := sess.Folder(folders[n].ID).Contents()
			if err == nil {
				for i := 0; i < len(childs); i++ {
					if childs[i].Type == "d" {
						next = append(next, &childs[i])
					} else if childs[i].Type == "f" {
						T.CheckFile(sess, user, &childs[i], activity)
					}
				}
			} else {
				Err("%s: %v", folders[n].Path, err)
			}
		} else if folders[n].Type == "f" {
			T.CheckFile(sess, user, folders[n], nil)
		}
		n++
	}
}

// folderActivityMap fetches a single page of recent folder activities and
// returns a map of file identifier -> most recent activity time found.
//
// The folder-activities response is not strongly typed in the codebase, so it
// is read as raw maps and any of the file's id/guid/file_guid keys are indexed.
func (T *StaleFileReportTask) folderActivityMap(sess *KWSession, folder *KiteObject) map[string]time.Time {
	result := make(map[string]time.Time)

	var raw []map[string]interface{}
	// Fetch the most recent page of activities (newest first). One page is
	// sufficient: any file without a recent activity here falls through to the
	// per-file lookup in CheckFile.
	if err := sess.DataCall(APIRequest{
		Method: "GET",
		Path:   SetPath("/rest/folders/%s/activities", folder.ID),
		Params: SetParams(Query{"orderBy": "created:desc"}),
		Output: &raw,
	}, 0, 1000); err != nil {
		Debug("%s: folder activities unavailable: %v", folder.Path, err)
		return result
	}

	if !T.debug_dumped && len(raw) > 0 {
		T.locker.Lock()
		if !T.debug_dumped {
			if b, e := json.MarshalIndent(raw[0], "", "  "); e == nil {
				Debug("Folder activity sample:\n%s", string(b))
			}
			T.debug_dumped = true
		}
		T.locker.Unlock()
	}

	for _, event := range raw {
		created := mapStr(event, "created")
		if created == "" {
			continue
		}
		t, err := ReadKWTime(created)
		if err != nil {
			continue
		}
		data, _ := event["data"].(map[string]interface{})
		if data == nil {
			continue
		}
		file, _ := data["file"].(map[string]interface{})
		if file == nil {
			continue
		}
		for _, key := range []string{"id", "guid", "file_guid"} {
			if id := mapStr(file, key); id != "" {
				if prev, ok := result[id]; !ok || t.After(prev) {
					result[id] = t
				}
			}
		}
	}

	return result
}

// CheckFile determines whether a file is stale and, if so, writes it to the report.
func (T *StaleFileReportTask) CheckFile(sess *KWSession, user *KiteUser, file *KiteObject, activity map[string]time.Time) {
	T.files_count.Add(1)

	// Establish the baseline last-activity time from the file's own timestamps.
	last, _ := ReadKWTime(file.Modified)
	if created, err := ReadKWTime(file.Created); err == nil && created.After(last) {
		last = created
	}
	source := "modified"

	// Fold in any recent folder activity referencing this file.
	if activity != nil {
		if t, ok := activity[file.ID]; ok && t.After(last) {
			last = t
			source = "folder_activity"
		}
	}

	// If it still looks stale, confirm with an authoritative per-file lookup.
	if last.Before(T.cutoff) && !T.input.no_fallback {
		T.fallback_count.Add(1)
		if acts, err := sess.File(file.ID).Activities(Query{"orderBy": "created:desc"}); err == nil {
			for i := range acts {
				if t, err := ReadKWTime(acts[i].Created); err == nil && t.After(last) {
					last = t
					source = "file_activity"
				}
			}
		} else {
			Debug("%s: file activities unavailable: %v", file.Path, err)
		}
	}

	if !last.Before(T.cutoff) {
		// File had activity within the window; keep it.
		return
	}

	T.stale_count.Add(1)
	T.stale_size.Add64(file.Size)

	days_idle := int(time.Now().UTC().Sub(last).Hours() / 24)
	last_event := T.lastEventName(sess, file, source)

	record := []string{
		user.Email,
		file.ID,
		file.Name,
		file.Path,
		strconv.FormatInt(file.Size, 10),
		HumanSize(file.Size),
		formatKWTime(file.Created),
		formatKWTime(file.Modified),
		last.Format("02 Jan 2006 15:04:05"),
		last_event,
		strconv.Itoa(days_idle),
		source,
	}

	if err := T.writeRecord(record); err != nil {
		Err("%s: %v", file.Path, err)
	}
}

// lastEventName returns a best-effort name of the most recent activity event
// for a file, used purely as descriptive context in the report.
func (T *StaleFileReportTask) lastEventName(sess *KWSession, file *KiteObject, source string) string {
	if source == "modified" {
		return "modified"
	}
	acts, err := sess.File(file.ID).Activities(Query{"orderBy": "created:desc"})
	if err != nil || len(acts) == 0 {
		return source
	}
	latest := acts[0]
	for i := range acts {
		if a, err := ReadKWTime(acts[i].Created); err == nil {
			if l, err := ReadKWTime(latest.Created); err != nil || a.After(l) {
				latest = acts[i]
			}
		}
	}
	if latest.Event != "" {
		return latest.Event
	}
	return source
}

// mapStr extracts a string value from a map by key, returning "" if absent or nil.
func mapStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// formatKWTime renders a Kiteworks timestamp as "02 Jan 2006 15:04:05" or "-" if empty/invalid.
func formatKWTime(input string) string {
	if input == "" {
		return "-"
	}
	t, err := ReadKWTime(input)
	if err != nil {
		return input
	}
	return t.Format("02 Jan 2006 15:04:05")
}
