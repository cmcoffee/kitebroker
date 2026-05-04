package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterAdminTask(new(ActivityReportTask)) }

// eventTypeMap maps API event names to human-readable CSV type names.
// Only events in this map will be included in the report.
// Events listed in eventExtraTypes will also produce an additional row.
var eventTypeMap = map[string]string{
	"alerts":          "Alert",
	"alert_operation": "Alert Operation",

	"user_logged_in":  "Successful Login",
	"admin_logged_in": "Successful Login",
	"session_started": "Successful Login",

	"user_login_failed":  "Failed Login",
	"admin_login_failed": "Failed Login",
	"tfa_login_failed":   "Failed Login",

	"add_file":         "Uploads",
	"add_file_version": "Uploads",
	"upload_to_tray":   "Uploads",
	"ec_upload_file":   "Uploads",
	"upload":           "Uploads",

	"download_file":             "Downloads",
	"download":                  "Downloads",
	"download_watermarked_file": "Downloads",
	"ec_download_file":          "Downloads",
	"download_zip":              "Downloads",
	"download_email":            "Downloads",
	"download_email_zip":        "Downloads",

	"view_mail":    "Received Mail",
	"view_message": "Received Mail",

	"send_mail":    "Sent Mail",
	"send_message": "Sent Mail",
	"send_preview": "Sent Mail",

	"view_file":       "Views",
	"view_attachment": "Views",
}

// eventExtraTypes maps event names that should produce an additional row
// under a secondary type name.
var eventExtraTypes = map[string]string{
	"download_email":     "Received Mail",
	"view_attachment":    "Received Mail",
	"download_email_zip": "Received Mail",
}

// clientGroupMap maps client names that should be grouped together for --split_clients.
var clientGroupMap = map[string]string{
	"Kiteworks Legacy Admin":       "Kiteworks_Admin",
	"Kiteworks Web User":           "Kiteworks_Web",
	"Kiteworks Compliance Console": "Kiteworks_Compliance",
	"Kiteworks Data Network Admin": "Kiteworks_Admin",

	"Kiteworks Desktop Client 2.0 for Win/Mac/Linux": "Kiteworks_Desktop",
	"Kiteworks Desktop Client for Mac":               "Kiteworks_Desktop",
	"Kiteworks Desktop Client for Windows":           "Kiteworks_Desktop",

	"Kiteworks Automation Agent for Linux":   "Kiteworks_Automation_Agent",
	"Kiteworks Automation Agent for Windows": "Kiteworks_Automation_Agent",
	"Kiteworks Automation Agent for Mac":     "Kiteworks_Automation_Agent",

	"Private Data Network Admin": "Kiteworks_Admin",
	"SFTP client":                "SFTP",
	"SSH Client":                 "SFTP",
}

// normalizeClientName returns the grouped client name if one exists, otherwise the original.
func normalizeClientName(name string) string {
	if group, ok := clientGroupMap[name]; ok {
		return group
	}
	return name
}

// allEventFilters are all supported API filter values.
var allEventFilters = []string{
	"valid_login",
	"invalid_login",
	"file_upload",
	"file_download",
	"sent",
	"received",
	"file_view",
}

type ActivityReportTask struct {
	input struct {
		start_date       string
		end_date         string
		order_by         string
		days             int
		chunk_days       int
		page_size        int
		valid_login      bool
		invalid_login    bool
		file_upload      bool
		file_download    bool
		sent             bool
		received         bool
		file_view        bool
		ciso_mode        bool
		split_clients    bool
		split_internal   string
		internal_domains []string
	}
	csv_writer     *csv.Writer
	csv_file       *os.File
	client_csvs    map[string]*clientCSV
	locker         sync.Mutex
	activity_count Tally
	KiteBrokerTask
}

type clientCSV struct {
	file   *os.File
	writer *csv.Writer
}

func (T ActivityReportTask) Name() string {
	return "activity_report"
}

func (T ActivityReportTask) Desc() string {
	return "Users:Generate admin activity report as CSV."
}

func (T *ActivityReportTask) Init() (err error) {
	T.Flags.IntVar(&T.input.days, "days", 7, "Number of days back to retrieve activities for.")
	T.Flags.IntVar(&T.input.chunk_days, "chunk_days", 29, "Maximum number of days per API query chunk.")
	T.Flags.IntVar(&T.input.page_size, "page_size", 1000, "Number of activities to retrieve per API page.")
	now := time.Now().UTC()
	T.Flags.StringVar(&T.input.start_date, "start_date", fmt.Sprintf("<%s>", now.AddDate(0, 0, -7).Format("2006-01-02")), "Start date for activity range (YYYY-MM-DD), overrides --days.")
	T.Flags.StringVar(&T.input.end_date, "end_date", fmt.Sprintf("<%s>", now.Format("2006-01-02")), "End date for activity range (YYYY-MM-DD), overrides --days.")
	T.Flags.StringVar(&T.input.order_by, "order_by", "asc", "Sort order for created date (asc or desc).")
	T.Flags.BoolVar(&T.input.valid_login, "valid_login", "Include successful login events.")
	T.Flags.BoolVar(&T.input.invalid_login, "invalid_login", "Include failed login events.")
	T.Flags.BoolVar(&T.input.file_upload, "file_upload", "Include file upload events.")
	T.Flags.BoolVar(&T.input.file_download, "file_download", "Include file download events.")
	T.Flags.BoolVar(&T.input.sent, "sent", "Include sent mail events.")
	T.Flags.BoolVar(&T.input.received, "received", "Include received mail events.")
	T.Flags.BoolVar(&T.input.file_view, "file_view", "Include file view events.")
	T.Flags.BoolVar(&T.input.ciso_mode, "ciso_mode", "Ciso compatible mode, list each file/attachment on seperate line in the CSV.")
	T.Flags.BoolVar(&T.input.split_clients, "split_clients", "Split output into separate CSV files per client name.")
	T.Flags.StringVar(&T.input.split_internal, "split_internal", "", "Split output into Internal/External CSV files by user email domain (comma-separated domains).")
	run := T.Flags.Bool("run", "Execute the task.")
	T.Flags.Order("days", "chunk_days", "page_size", "start_date", "end_date", "order_by", "valid_login", "invalid_login", "file_upload", "file_download", "sent", "received", "file_view", "ciso_mode", "split_clients", "split_internal", "run")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if !*run {
		return fmt.Errorf("Please specify --run to execute this task.")
	}

	if T.input.order_by != "asc" && T.input.order_by != "desc" {
		return fmt.Errorf("--order_by must be 'asc' or 'desc'.")
	}

	if T.input.chunk_days < 1 || T.input.chunk_days > 29 {
		return fmt.Errorf("--chunk_days must be between 1 and 29.")
	}

	if T.input.page_size < 1 || T.input.page_size > 1000 {
		return fmt.Errorf("--page_size must be between 1 and 1000.")
	}

	// If start_date/end_date are provided, validate them; otherwise derive from --days.
	if T.input.start_date != "" || T.input.end_date != "" {
		if T.input.start_date == "" || T.input.end_date == "" {
			return fmt.Errorf("Both --start_date and --end_date must be provided together.")
		}
		if _, err := time.Parse("2006-01-02", T.input.start_date); err != nil {
			return fmt.Errorf("Invalid --start_date format, expected YYYY-MM-DD: %w", err)
		}
		if _, err := time.Parse("2006-01-02", T.input.end_date); err != nil {
			return fmt.Errorf("Invalid --end_date format, expected YYYY-MM-DD: %w", err)
		}
	} else {
		now := time.Now().UTC()
		T.input.end_date = now.Format("2006-01-02")
		T.input.start_date = now.AddDate(0, 0, -T.input.days).Format("2006-01-02")
	}

	if T.input.split_internal != "" {
		for _, d := range strings.Split(T.input.split_internal, ",") {
			d = strings.TrimSpace(strings.ToLower(d))
			if d != "" {
				T.input.internal_domains = append(T.input.internal_domains, d)
			}
		}
		if len(T.input.internal_domains) == 0 {
			return fmt.Errorf("--split_internal requires at least one domain.")
		}
	}

	return
}

// selectedFilters returns the event filters based on user flags.
// If none are selected, all filters are returned.
func (T *ActivityReportTask) selectedFilters() []string {
	filterFlags := map[string]bool{
		"valid_login":   T.input.valid_login,
		"invalid_login": T.input.invalid_login,
		"file_upload":   T.input.file_upload,
		"file_download": T.input.file_download,
		"sent":          T.input.sent,
		"received":      T.input.received,
		"file_view":     T.input.file_view,
	}

	var selected []string
	for _, f := range allEventFilters {
		if filterFlags[f] {
			selected = append(selected, f)
		}
	}

	if len(selected) == 0 {
		return allEventFilters
	}
	return selected
}

func (T *ActivityReportTask) writeRecordTo(w *csv.Writer, record []string) error {
	T.locker.Lock()
	defer T.locker.Unlock()
	if err := w.Write(record); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func (T *ActivityReportTask) writeRecord(record []string) error {
	return T.writeRecordTo(T.csv_writer, record)
}

// splitKey builds the file split key from the enabled split options.
func (T *ActivityReportTask) splitKey(clientName, userName string) string {
	var parts []string
	if T.input.split_clients {
		name := normalizeClientName(clientName)
		if name == "" {
			name = "Unknown"
		}
		parts = append(parts, name)
	}
	if len(T.input.internal_domains) > 0 {
		if T.isInternalUser(userName) {
			parts = append(parts, "Internal")
		} else {
			parts = append(parts, "External")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "-")
}

// isInternalUser checks if the userName's email domain matches any internal domain.
func (T *ActivityReportTask) isInternalUser(userName string) bool {
	at := strings.LastIndex(userName, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(userName[at+1:])
	for _, d := range T.input.internal_domains {
		if domain == d {
			return true
		}
	}
	return false
}

// getSplitWriter returns the CSV writer for the given split key, creating the file if needed.
func (T *ActivityReportTask) getSplitWriter(key string, timestamp int64, header []string) (*csv.Writer, error) {
	if cw, ok := T.client_csvs[key]; ok {
		return cw.writer, nil
	}
	safeName := sanitizeFilename(key)
	if safeName == "" {
		safeName = "Unknown"
	}
	filename := fmt.Sprintf("Activities-Report-%d-%s.csv", timestamp, safeName)
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		f.Close()
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return nil, err
	}
	T.client_csvs[key] = &clientCSV{file: f, writer: w}
	Log("Created report file: %s", filename)
	return w, nil
}

// sanitizeFilename replaces non-alphanumeric characters with underscores.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (T *ActivityReportTask) Main() (err error) {
	T.activity_count = T.Report.Tally("Activities")
	T.client_csvs = make(map[string]*clientCSV)
	defer func() {
		for _, cw := range T.client_csvs {
			cw.writer.Flush()
			cw.file.Close()
		}
	}()

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("Activities-Report-%d.csv", timestamp)

	splitting := T.input.split_clients || len(T.input.internal_domains) > 0

	if !splitting {
		T.csv_file, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return err
		}
		defer T.csv_file.Close()
		T.csv_writer = csv.NewWriter(T.csv_file)
	}

	header := []string{
		"Type",
		"Event time (GMT)",
		"Time stamp",
		"Alert UUID",
		"IP",
		"User location",
		"File/Source location",
		"Username/email",
		"Message",
		"File name",
		"Recipients",
		"Last accessed (GMT)",
		"Time stamp",
		"File path",
		"File size",
		"Client",
		"User agent",
	}
	if !splitting {
		if err = T.writeRecord(header); err != nil {
			return err
		}
	}

	startDate, _ := time.Parse("2006-01-02", T.input.start_date)
	endDate, _ := time.Parse("2006-01-02", T.input.end_date)
	orderBy := "created:" + T.input.order_by

	debugDumped := false
	totalCount := 0
	// Chunk the date range into <= 29-day windows to stay within API limits.
	// Walk forward (asc) or backward (desc) through the range.
	for _, chunk := range dateChunks(startDate, endDate, T.input.order_by, T.input.chunk_days) {
		chunkStart := chunk[0].Format("2006-01-02") + "T00:00:00.000Z"
		chunkEnd := chunk[1].Format("2006-01-02") + "T23:59:59.999Z"
		chunkCount := 0

		Log("Fetching activities from %s to %s ...", chunk[0].Format("2006-01-02"), chunk[1].Format("2006-01-02"))

		query := Query{
			"startDateTime":   chunkStart,
			"endDateTime":     chunkEnd,
			"orderBy":         orderBy,
			"eventFilters:in": strings.Join(T.selectedFilters(), ","),
			"compact":         false,
		}

		pageSize := T.input.page_size
		offset := 0

		for {
			var rawActivities []map[string]interface{}

			err = T.KW.Admin().Activities(&rawActivities, offset, pageSize, query)
			if err != nil {
				return err
			}

			for _, event := range rawActivities {
				eventName := mapStr(event, "eventName")

				// Look up the human-readable type; skip events not in the map.
				typeName, ok := eventTypeMap[eventName]
				if !ok {
					continue
				}

				T.activity_count.Add(1)
				totalCount++
				chunkCount++

				// Debug dump the first activity to discover field names.
				if !debugDumped {
					if raw, err := json.MarshalIndent(event, "", "  "); err == nil {
						Debug("Activity list sample (compact=false):\n%s", string(raw))
					}
					debugDumped = true
				}

				// Parse "created" to derive both formatted time and unix timestamp.
				created := mapStr(event, "created")
				eventTimeStr := formatEventTime(created)
				timestampStr := formatUnixTimestamp(created)

				// Extract the nested data map.
				var data map[string]interface{}
				if d, ok := event["data"].(map[string]interface{}); ok {
					data = d
				}

				// Extract file info from data.attachments[], data.attachment, or data.file.
				fileInfos := allAttachments(data)
				if len(fileInfos) == 0 {
					if f, ok := data["attachment"].(map[string]interface{}); ok {
						fileInfos = []map[string]interface{}{f}
					}
				}
				if len(fileInfos) == 0 {
					if f, ok := data["file"].(map[string]interface{}); ok {
						fileInfos = []map[string]interface{}{f}
					}
				}
				fileName, filePath, fileSize := joinFileInfo(fileInfos, T.input.ciso_mode)

				// Extract recipients as comma-separated names from data.recipients[].
				recipients := extractRecipients(data)

				clientName := mapStr(event, "clientName")
				userName := mapStr(event, "userName")

				record := []string{
					typeName,
					eventTimeStr,
					timestampStr,
					dashIfEmpty(mapStr(event, "alertUuid")),
					dashIfEmpty(mapStr(event, "ipAddress")),
					emptyDefault(mapStr(data, "location"), "Unknown Location"),
					dashIfEmpty(mapStr(data, "fileSourceLocation")),
					dashIfEmpty(userName),
					mapStr(event, "description"),
					fileName,
					dashIfEmpty(recipients),
					dashIfEmpty(mapStr(data, "lastAccessed")),
					dashIfEmpty(mapStr(data, "lastAccessedTimestamp")),
					filePath,
					fileSize,
					clientName,
					dashIfEmpty(mapStr(event, "userAgent")),
				}

				if splitting {
					key := T.splitKey(clientName, userName)
					w, err := T.getSplitWriter(key, timestamp, header)
					if err != nil {
						return err
					}
					if err = T.writeRecordTo(w, record); err != nil {
						return err
					}
				} else {
					if err = T.writeRecord(record); err != nil {
						return err
					}
				}

				// Write an additional row if this event maps to a secondary type.
				if extraType, ok := eventExtraTypes[eventName]; ok {
					T.activity_count.Add(1)
					totalCount++
					chunkCount++
					extraRecord := make([]string, len(record))
					copy(extraRecord, record)
					extraRecord[0] = extraType
					if splitting {
						key := T.splitKey(clientName, userName)
						w, err := T.getSplitWriter(key, timestamp, header)
						if err != nil {
							return err
						}
						if err = T.writeRecordTo(w, extraRecord); err != nil {
							return err
						}
					} else {
						if err = T.writeRecord(extraRecord); err != nil {
							return err
						}
					}
				}
			}

			if len(rawActivities) < pageSize {
				break
			}
			offset += pageSize
		}

		Log("Processed %d entries for %s to %s.", chunkCount, chunk[0].Format("2006-01-02"), chunk[1].Format("2006-01-02"))
	}

	if splitting {
		Log("Report split into %d files with %d total activities.", len(T.client_csvs), totalCount)
	} else {
		Log("Report written to %s with %d activities.", filename, totalCount)
	}
	return nil
}

// dateChunks splits a date range into chunks of chunkDays or fewer.
// When order is "asc", chunks are returned oldest-first; when "desc", newest-first.
func dateChunks(start, end time.Time, order string, chunkDays int) [][2]time.Time {
	var chunks [][2]time.Time

	if order == "desc" {
		// Walk backward from end to start.
		for end.After(start) {
			chunkStart := end.AddDate(0, 0, -(chunkDays - 1))
			if chunkStart.Before(start) {
				chunkStart = start
			}
			chunks = append(chunks, [2]time.Time{chunkStart, end})
			end = chunkStart.AddDate(0, 0, -1)
		}
	} else {
		// Walk forward from start to end.
		for start.Before(end) || start.Equal(end) {
			chunkEnd := start.AddDate(0, 0, chunkDays-1)
			if chunkEnd.After(end) {
				chunkEnd = end
			}
			chunks = append(chunks, [2]time.Time{start, chunkEnd})
			start = chunkEnd.AddDate(0, 0, 1)
		}
	}

	return chunks
}

// emptyDefault returns the fallback string if the input is empty, otherwise returns the input.
func emptyDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// dashIfEmpty returns "-" if the input string is empty, otherwise returns the input.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// formatEventTime converts a Kiteworks timestamp to "2 Jan 2006 15:04:05" format.
func formatEventTime(created string) string {
	if created == "" {
		return "-"
	}
	t, err := ReadKWTime(created)
	if err != nil {
		return created
	}
	return t.Format("02 Jan 2006 15:04:05")
}

// formatUnixTimestamp converts a Kiteworks timestamp to a unix timestamp string.
func formatUnixTimestamp(created string) string {
	if created == "" {
		return "0"
	}
	t, err := ReadKWTime(created)
	if err != nil {
		return "0"
	}
	return strconv.FormatInt(t.Unix(), 10)
}

// extractFirstEvent extracts the first event from the {"events": [...]} response envelope.
func extractFirstEvent(detail map[string]interface{}) map[string]interface{} {
	if detail == nil {
		return nil
	}
	v, ok := detail["events"]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return nil
	}
	m, _ := arr[0].(map[string]interface{})
	return m
}

// allAttachments extracts all elements from data.attachments[] as maps.
func allAttachments(data map[string]interface{}) []map[string]interface{} {
	if data == nil {
		return nil
	}
	v, ok := data["attachments"]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return nil
	}
	var results []map[string]interface{}
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			results = append(results, m)
		}
	}
	return results
}

// joinFileInfo joins file names, paths, and sizes from multiple attachments.
// When cisoMode is true, entries are separated with ",\n" for one-per-line output;
// otherwise a simple ", " separator is used.
func joinFileInfo(fileInfos []map[string]interface{}, cisoMode bool) (fileName, filePath, fileSize string) {
	if len(fileInfos) == 0 {
		return "", "", "0"
	}
	var names, paths, sizes []string
	for _, f := range fileInfos {
		names = append(names, mapStr(f, "name"))
		paths = append(paths, mapStr(f, "path"))
		sizes = append(sizes, mapNumStr(f, "size"))
	}
	sep := ", "
	if cisoMode {
		sep = ",\n"
	}
	return strings.Join(names, sep), strings.Join(paths, sep), strings.Join(sizes, sep)
}

// extractRecipients extracts names from data.recipients[] and joins them.
func extractRecipients(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	v, ok := data["recipients"]
	if !ok || v == nil {
		return ""
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return ""
	}
	var names []string
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if name := mapStr(m, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return strings.Join(names, ",")
}

// mapKeys returns the keys of a map for debug logging.
func mapKeys(m map[string]interface{}) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// mapStr extracts a string value from a map by key.
func mapStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		switch s := v.(type) {
		case string:
			return s
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// mapNum extracts a numeric value from a map as int64.
func mapNum(m map[string]interface{}, key string) int64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

// mapNumStr extracts a numeric value from a map as a string.
func mapNumStr(m map[string]interface{}, key string) string {
	n := mapNum(m, key)
	return strconv.FormatInt(n, 10)
}
