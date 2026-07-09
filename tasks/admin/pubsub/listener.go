package pubsub

import (
	. "github.com/cmcoffee/kitebroker/core"
)

// This file is an example of a "webhook task": a task driven by PubSub webhook
// deliveries rather than the normal run loop. Webhook tasks self-register with
// RegisterWebhookTask, declare the subject patterns they consume via Subjects,
// and process each matching delivery in Handle. The shared listener (bind,
// secret, TLS, self-register, etc.) is configured once via --setup; a webhook
// task only concerns itself with its subjects and its own reactor logic.
//
// Main is non-blocking: it hands the task to the shared listener and returns,
// so multiple webhook tasks can be activated together (each from its own
// task-file section) and all are served on the single configured port.

func init() { RegisterWebhookTask(new(FileEventReactor)) }

// FileEventReactor logs Kiteworks file events as they arrive. It is intended
// as a reference for writing webhook tasks.
type FileEventReactor struct {
	input struct {
		verbose bool
	}
	KiteBrokerTask
}

func (T *FileEventReactor) Name() string { return "on_file_event" }

func (T *FileEventReactor) Desc() string {
	return "PubSub: Log Kiteworks file events as they are delivered (example webhook task)."
}

// Subjects declares the subject patterns this task consumes.
func (T *FileEventReactor) Subjects() []string {
	return []string{
		"file_folder_access.>",
		"file_folder_modify.>",
		"file_folder_control.>",
		"file_folder_manage.>",
	}
}

func (T *FileEventReactor) Init() (err error) {
	T.Flags.BoolVar(&T.input.verbose, "verbose", "Log the full event payload for each delivery.")
	T.Flags.Order("verbose")
	return T.Flags.Parse()
}

// Main hands this task to the shared listener and returns immediately.
func (T *FileEventReactor) Main() (err error) {
	return HostWebhookTask(T, T.KW)
}

// Handle processes a single delivery. It may use T.KW, T.Report, and T.input
// exactly as Main would. ev.Subject is the Kiteworks event_name (e.g.
// add_file_version, filehash_generated) and ev.Data is the payload.data object.
func (T *FileEventReactor) Handle(ev WebhookEvent) (err error) {
	handled := T.Report.Tally("File Events Handled")

	var d struct {
		File struct {
			Name     string `json:"name"`
			Path     string `json:"path"`
			Size     int64  `json:"size"`
			Uploader struct {
				Name string `json:"name"`
			} `json:"file_uploader"`
		} `json:"file"`
	}
	if derr := ev.DecodeData(&d); derr == nil && !IsBlank(d.File.Name) {
		Log("[%s] %s (%s, %d bytes) by %s", ev.Subject, d.File.Name, d.File.Path, d.File.Size, d.File.Uploader.Name)
	} else {
		Log("[%s] event received.", ev.Subject)
	}

	if T.input.verbose {
		Log("  payload: %s", string(ev.Raw))
	}

	handled.Add(1)
	return nil
}
