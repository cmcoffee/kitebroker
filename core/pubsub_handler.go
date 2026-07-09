package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebhookEvent represents a single PubSub webhook delivery after parsing.
//
// The exact on-the-wire payload Kiteworks delivers is appliance-specific, so
// parsing is best-effort and the original request body is always preserved in
// Raw. Handlers that need fields the envelope does not surface can re-decode
// Data (or Raw) into a concrete type.
type WebhookEvent struct {
	Subject   string          // Resolved NATS subject, e.g. "file_folder_modify.file.upload".
	Event     string          // Event name, when distinct from the subject.
	Timestamp time.Time       // Best-effort delivery time; zero if absent/unparseable.
	WebhookID string          // ID of the delivering webhook, when present.
	Data      json.RawMessage // Raw event payload (the "data" object), for re-decoding.
	Raw       []byte          // The entire request body, untouched.
	Header    http.Header     // Delivery headers, for handlers needing trace/id values.
}

// DecodeData unmarshals the event's Data payload into the given value.
func (e WebhookEvent) DecodeData(v interface{}) error {
	return json.Unmarshal(e.Data, v)
}

// AsActivity decodes the event payload into a KiteActivity.
func (e WebhookEvent) AsActivity() (activity KiteActivity, err error) {
	err = json.Unmarshal(e.Data, &activity)
	return
}

// AsAdminActivity decodes the event payload into a KiteAdminActivity.
func (e WebhookEvent) AsAdminActivity() (activity KiteAdminActivity, err error) {
	err = json.Unmarshal(e.Data, &activity)
	return
}

// SubjectAll is the NATS subject wildcard that matches every subject.
const SubjectAll = ">"

// WebhookHandler reacts to a single webhook delivery. It receives the parsed
// event and a KWSession for any API calls it needs to make. A non-nil error is
// recorded by the dispatcher but does not prevent other handlers from running.
type WebhookHandler func(event WebhookEvent, kw KWSession) error

// WebhookTask is a Task that is driven by webhook deliveries instead of the
// normal run loop. It declares the subject patterns it consumes and a handler
// invoked for each matching delivery. Because it embeds KiteBrokerTask (via
// the Task interface), Handle may use T.KW, T.Report, T.input, etc. exactly as
// Main would. A webhook task's Main is expected to be non-blocking and simply
// host the task via HostWebhookTask.
type WebhookTask interface {
	Task
	// Subjects returns the subject patterns this task subscribes to (NATS
	// wildcards apply; see SubjectMatch).
	Subjects() []string
	// Handle processes a single matching delivery.
	Handle(event WebhookEvent) error
}

var (
	webhookTaskMu        sync.Mutex
	registeredWebhookTks []WebhookTask
)

// RegisterWebhookTask registers a webhook-powered task. Intended to be called
// from a task package's init(), mirroring RegisterTask/RegisterAdminTask.
func RegisterWebhookTask(task WebhookTask) {
	webhookTaskMu.Lock()
	defer webhookTaskMu.Unlock()
	registeredWebhookTks = append(registeredWebhookTks, task)
}

// RegisteredWebhookTasks returns all registered webhook tasks.
func RegisteredWebhookTasks() []WebhookTask {
	webhookTaskMu.Lock()
	defer webhookTaskMu.Unlock()
	return registeredWebhookTks
}

// webhookRegistration pairs a subject pattern with its handler.
type webhookRegistration struct {
	pattern string
	handler WebhookHandler
}

var (
	webhookRegMu    sync.RWMutex
	webhookHandlers []webhookRegistration
)

// RegisterWebhookHandler registers a handler for deliveries whose subject
// matches subjectPattern (see SubjectMatch for the matching rules). It is
// intended to be called from a task package's init(). Multiple handlers may be
// registered for overlapping patterns; all matching handlers run per delivery.
func RegisterWebhookHandler(subjectPattern string, handler WebhookHandler) {
	webhookRegMu.Lock()
	defer webhookRegMu.Unlock()
	webhookHandlers = append(webhookHandlers, webhookRegistration{subjectPattern, handler})
}

// SubjectMatch reports whether subject matches pattern using NATS subject
// semantics, consistent with the subscription wildcards used elsewhere:
//   - tokens are dot-separated and compared case-insensitively
//   - "*" matches exactly one token
//   - ">" matches one or more trailing tokens and is valid only as the final token
//
// A bare ">" therefore matches every subject.
func SubjectMatch(pattern, subject string) bool {
	pattern = strings.TrimSpace(pattern)
	subject = strings.TrimSpace(subject)
	if pattern == SubjectAll {
		return true
	}

	p := strings.Split(pattern, ".")
	s := strings.Split(subject, ".")

	for i := 0; i < len(p); i++ {
		tok := p[i]
		if tok == ">" {
			// ">" must be the final pattern token and requires at least one
			// remaining subject token to consume.
			return i == len(p)-1 && len(s) > i
		}
		if i >= len(s) {
			return false
		}
		if tok == "*" {
			continue
		}
		if !strings.EqualFold(tok, s[i]) {
			return false
		}
	}

	// All pattern tokens matched; the subject must not have extra trailing tokens.
	return len(s) == len(p)
}

// DispatchWebhookEvent runs every registered handler whose pattern matches the
// event's subject. It returns the number of matching handlers and any errors
// they produced; a failing handler never prevents the others from running.
func DispatchWebhookEvent(event WebhookEvent, kw KWSession) (matched int, errs []error) {
	webhookRegMu.RLock()
	regs := make([]webhookRegistration, len(webhookHandlers))
	copy(regs, webhookHandlers)
	webhookRegMu.RUnlock()

	for _, reg := range regs {
		if !SubjectMatch(reg.pattern, event.Subject) {
			continue
		}
		matched++
		if err := reg.handler(event, kw); err != nil {
			errs = append(errs, err)
		}
	}
	return
}
