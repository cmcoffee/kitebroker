package pubsub

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterWebhookTask(new(ZeroByteUploadNotifyTask)) }

// triggerEvent is the Kiteworks event_name treated as the authoritative
// "upload finished" signal on the webhook path. An upload emits add_file_version
// first (while the fingerprint is still "Generating...") and filehash_generated
// once the file is finalized and its size is known, so keying on the latter both
// confirms the final size and dedupes the two-events-per-upload behavior.
const triggerEvent = "filehash_generated"

// uploadEventNames are the activity-log event names representing a file upload,
// used on the polling path.
var uploadEventNames = map[string]struct{}{
	"add_file":         {},
	"add_file_version": {},
	"upload":           {},
	"ec_upload_file":   {},
	"upload_to_tray":   {},
}

// cursorKey is the database key holding the timestamp of the last processed
// activity (RFC3339, UTC) on the polling path.
const cursorKey = "zero_byte_notify_cursor"

// ZeroByteUploadNotifyTask notifies an administrator, via Kiteworks email, when
// a 0-byte file is uploaded — reporting who uploaded it and where.
//
// It uses whichever transport is configured: when webhook delivery is enabled
// (--setup), it hosts the PubSub listener and reacts to deliveries in real time;
// otherwise it falls back to periodically polling the admin activity log (run it
// with --repeat). Both paths produce an identical notification.
type ZeroByteUploadNotifyTask struct {
	input struct {
		notify    []string
		subject   string
		lookback  int
		page_size int
	}
	checked Tally
	alerted Tally
	KiteBrokerTask
}

func (T *ZeroByteUploadNotifyTask) Name() string { return "zero_byte_upload_notify" }

func (T *ZeroByteUploadNotifyTask) Desc() string {
	return "PubSub: Notify an administrator when a 0-byte file is uploaded (webhook or activity-log polling)."
}

// Subjects declares the file/folder event families to subscribe to when hosting
// the webhook listener.
func (T *ZeroByteUploadNotifyTask) Subjects() []string {
	return []string{
		"file_folder_access.>",
		"file_folder_modify.>",
		"file_folder_control.>",
		"file_folder_manage.>",
	}
}

func (T *ZeroByteUploadNotifyTask) Init() (err error) {
	T.Flags.MultiVar(&T.input.notify, "notify", "<admin@domain.com>", "Administrator e-mail address(es) to notify.")
	T.Flags.StringVar(&T.input.subject, "subject", "Kiteworks: 0-byte file uploaded", "Subject line for the notification email.")
	T.Flags.IntVar(&T.input.lookback, "lookback_hours", 24, "Polling fallback: on the first run (no saved cursor), how many hours back to scan.")
	T.Flags.IntVar(&T.input.page_size, "page_size", 1000, "Polling fallback: number of activities to retrieve per API page.")
	T.Flags.Order("notify", "subject", "lookback_hours", "page_size")
	if err = T.Flags.Parse(); err != nil {
		return err
	}
	if len(T.input.notify) == 0 {
		return fmt.Errorf("--notify is required: at least one administrator e-mail address to notify.")
	}
	if T.input.page_size < 1 || T.input.page_size > 1000 {
		return fmt.Errorf("--page_size must be between 1 and 1000.")
	}
	return nil
}

// Main selects the transport: host the webhook listener when enabled, otherwise
// poll the activity log.
func (T *ZeroByteUploadNotifyTask) Main() (err error) {
	if WebhookListenerEnabled() {
		Log("Webhook delivery is enabled; hosting listener for 0-byte upload notifications.")
		return HostWebhookTask(T, T.KW)
	}
	Log("Webhook delivery is not enabled; polling the activity log for 0-byte uploads.")
	return T.poll()
}

// ---- Webhook path ----

// zeroByteData is the subset of a webhook delivery's payload.data we read.
type zeroByteData struct {
	File struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Uploader struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"file_uploader"`
	} `json:"file"`
	ParentFolder struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"parent_folder"`
}

// Handle processes a single webhook delivery, notifying on a confirmed 0-byte
// upload. It acts only on the finalized filehash_generated event and reads
// everything it needs from the payload.
func (T *ZeroByteUploadNotifyTask) Handle(ev WebhookEvent) (err error) {
	if !strings.EqualFold(ev.Subject, triggerEvent) {
		return nil
	}

	T.ensureTallies()
	T.checked.Add(1)

	var d zeroByteData
	if derr := ev.DecodeData(&d); derr != nil {
		Debug("[%s] could not decode payload data: %v", ev.Subject, derr)
		return nil
	}
	if d.File.ID == 0 || d.File.Size != 0 {
		return nil
	}

	location := firstSet(d.File.Path, d.ParentFolder.Path)
	if IsBlank(location) {
		location = d.ParentFolder.Name
	}
	uploader := d.File.Uploader.Name
	if IsBlank(uploader) {
		uploader = fmt.Sprintf("(user id %d)", d.File.Uploader.ID)
	}

	f := zeroByteFile{
		ID:       fmt.Sprintf("%d", d.File.ID),
		Name:     d.File.Name,
		Location: location,
		Uploader: uploader,
		When:     ev.Timestamp,
	}
	if err = sendZeroByteAlert(T.KW, T.input.notify, T.input.subject, f); err != nil {
		return fmt.Errorf("notify admin of 0-byte upload %q (file %d): %w", d.File.Name, d.File.ID, err)
	}

	T.alerted.Add(1)
	Notice("0-byte upload %q by %s; notified %s.", d.File.Name, uploader, strings.Join(T.input.notify, ", "))
	return nil
}

// ---- Polling path ----

// poll scans the admin activity log for uploads since a persisted cursor and
// notifies on any 0-byte files.
func (T *ZeroByteUploadNotifyTask) poll() (err error) {
	T.ensureTallies()

	now := time.Now().UTC()
	var since time.Time
	var saved string
	if T.DB.Get("kitebroker", cursorKey, &saved) && !IsBlank(saved) {
		if t, perr := time.Parse(time.RFC3339, saved); perr == nil {
			since = t
		}
	}
	if since.IsZero() {
		since = now.Add(-time.Duration(T.input.lookback) * time.Hour)
	}

	startStr := since.Format("2006-01-02T15:04:05.000Z")
	endStr := now.Format("2006-01-02T15:04:05.000Z")
	Log("Scanning activity log for uploads from %s to %s ...", since.Format(time.RFC3339), now.Format(time.RFC3339))

	query := Query{
		"startDateTime":   startStr,
		"endDateTime":     endStr,
		"orderBy":         "created:asc",
		"eventFilters:in": "file_upload",
		"compact":         false,
	}

	newCursor := since
	offset := 0
	for {
		var activities []map[string]interface{}
		if err = T.KW.Admin().Activities(&activities, offset, T.input.page_size, query); err != nil {
			return err
		}

		for _, event := range activities {
			if _, ok := uploadEventNames[mapStr(event, "eventName")]; !ok {
				continue
			}
			if t, perr := ReadKWTime(mapStr(event, "created")); perr == nil && t.After(newCursor) {
				newCursor = t
			}

			file, ok := T.zeroByteFromActivity(event)
			if !ok {
				continue
			}
			T.checked.Add(1)

			if err := sendZeroByteAlert(T.KW, T.input.notify, T.input.subject, file); err != nil {
				Err("Failed to notify admin of 0-byte upload %q (file %s): %v", file.Name, file.ID, err)
				continue
			}
			T.alerted.Add(1)
			Notice("0-byte upload %q by %s; notified %s.", file.Name, file.Uploader, strings.Join(T.input.notify, ", "))
		}

		if len(activities) < T.input.page_size {
			break
		}
		offset += T.input.page_size
	}

	// Persist the cursor one second past the newest activity so the next cycle
	// does not re-fetch it.
	T.DB.Set("kitebroker", cursorKey, newCursor.Add(time.Second).UTC().Format(time.RFC3339))
	return nil
}

// zeroByteFromActivity inspects an upload activity and, when it represents a
// 0-byte file, returns a normalized zeroByteFile. The activity payload usually
// carries data.file.size directly; when size is absent, it falls back to a
// File(id).Info() lookup to confirm authoritatively.
func (T *ZeroByteUploadNotifyTask) zeroByteFromActivity(event map[string]interface{}) (zeroByteFile, bool) {
	data, _ := event["data"].(map[string]interface{})
	fileMap, _ := data["file"].(map[string]interface{})
	if fileMap == nil {
		return zeroByteFile{}, false
	}

	file_id := mapStr(fileMap, "id")

	var size int64
	if _, present := fileMap["size"]; present {
		size = mapNum(fileMap, "size")
	} else if !IsBlank(file_id) {
		info, err := T.KW.File(file_id).Info()
		if err != nil {
			Debug("could not confirm size for file %s: %v", file_id, err)
			return zeroByteFile{}, false
		}
		if info.Deleted || info.PermDeleted {
			return zeroByteFile{}, false
		}
		size = info.Size
	} else {
		return zeroByteFile{}, false
	}

	if size != 0 {
		return zeroByteFile{}, false
	}

	location := firstSet(mapStr(fileMap, "path"), mapStr(event, "fileSourceLocation"))
	uploader := firstSet(uploaderName(fileMap), mapStr(event, "userName"))

	var when time.Time
	if t, err := ReadKWTime(mapStr(event, "created")); err == nil {
		when = t
	}

	return zeroByteFile{
		ID:       file_id,
		Name:     mapStr(fileMap, "name"),
		Location: location,
		Uploader: uploader,
		When:     when,
	}, true
}

// ensureTallies initializes the report tallies once, regardless of transport.
func (T *ZeroByteUploadNotifyTask) ensureTallies() {
	if T.checked.Name() == NONE {
		T.checked = T.Report.Tally("Uploads Checked")
	}
	if T.alerted.Name() == NONE {
		T.alerted = T.Report.Tally("Zero-byte Alerts Sent")
	}
}

// uploaderName extracts the uploader's display name from a file activity's
// nested file_uploader object, if present.
func uploaderName(fileMap map[string]interface{}) string {
	if up, ok := fileMap["file_uploader"].(map[string]interface{}); ok {
		return mapStr(up, "name")
	}
	return ""
}

// mapStr extracts a string value from a map by key.
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
		case string:
			i, _ := strconv.ParseInt(n, 10, 64)
			return i
		}
	}
	return 0
}
