package pubsub

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterAdminTask(new(WebhookManagerTask)) }

// webhookExport is the on-disk representation used for export and import.
// The PubSub API never returns secret or token, so those are blank on export
// and must be supplied in the file before importing.
type webhookExport struct {
	ID            string   `json:"id,omitempty"`
	URL           string   `json:"url"`
	Token         string   `json:"token,omitempty"`
	Secret        string   `json:"secret,omitempty"`
	Enabled       bool     `json:"enabled"`
	Subscriptions []string `json:"subscriptions"`
}

// WebhookManagerTask manages PubSub consumer webhooks: listing, exporting to
// and importing from a file, and creating, updating, and deleting individual
// webhooks.
type WebhookManagerTask struct {
	action string // resolved operation: list, export, import, create, update, or delete.
	input  struct {
		uuid          string
		file          string
		url           string
		token         string
		secret        string
		subscriptions []string
		disable       bool
		all           bool
	}
	KiteBrokerTask
}

func (T WebhookManagerTask) Name() string { return "pubsub_webhooks" }

func (T WebhookManagerTask) Desc() string {
	return "PubSub: Manage PubSub consumer webhooks (list/export/import/create/update/delete)."
}

func (T *WebhookManagerTask) Init() (err error) {
	do_list := T.Flags.Bool("list", "List all configured webhooks.")
	do_export := T.Flags.Bool("export", "Export webhook(s) to --file (all, or one when --uuid is set).")
	do_import := T.Flags.Bool("import", "Import and create webhooks from --file.")
	do_create := T.Flags.Bool("create", "Create a new webhook.")
	do_delete := T.Flags.Bool("delete", "Delete the webhook identified by --uuid.")
	T.Flags.StringVar(&T.input.uuid, "uuid", "<uuid of webhook>", "Webhook UUID to act on (required for delete; optional for export; set with fields to update).")
	T.Flags.StringVar(&T.input.file, "file", "<webhooks.json>", "File to export to or import from (required for export and import).")
	T.Flags.StringVar(&T.input.url, "url", "<https://example.com/webhook>", "Webhook destination URL (required for create and full update).")
	T.Flags.StringVar(&T.input.secret, "secret", "<my-secret>", "Optional webhook signing secret.")
	T.Flags.StringVar(&T.input.token, "token", "<my-token>", "Optional bearer token sent with webhook deliveries.")
	T.Flags.MultiVar(&T.input.subscriptions, "subscriptions", "<file_folder_modify.>", "Subscription pattern(s) (required for create and full update); use 'all' to subscribe to every subject.")
	T.Flags.BoolVar(&T.input.disable, "disable", "Create or update the webhook in a disabled state.")
	T.Flags.BoolVar(&T.input.all, "all", "With --delete, delete every webhook (prompts for confirmation).")
	T.Flags.Order("list", "export", "import", "create", "delete", "uuid", "all", "file", "url", "secret", "token", "subscriptions", "disable")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	T.input.subscriptions = expandSubscriptions(T.input.subscriptions)

	// Resolve which operation to perform. Exactly one explicit action flag may
	// be set; with none, an update is inferred from --uuid plus updatable fields.
	var actions []string
	if *do_list {
		actions = append(actions, "list")
	}
	if *do_export {
		actions = append(actions, "export")
	}
	if *do_import {
		actions = append(actions, "import")
	}
	if *do_create {
		actions = append(actions, "create")
	}
	if *do_delete {
		actions = append(actions, "delete")
	}

	if len(actions) > 1 {
		return fmt.Errorf("Please specify only one of --list, --export, --import, --create, or --delete.")
	}

	if len(actions) == 1 {
		T.action = actions[0]
	} else if !IsBlank(T.input.uuid) && T.hasUpdatableField() {
		T.action = "update"
	} else {
		return fmt.Errorf("No action specified. Use --list, --export, --import, --create, --delete, or set --uuid with fields (--url/--secret/--subscriptions/--token/--disable) to update.")
	}

	// Per-action validation.
	switch T.action {
	case "export", "import":
		if IsBlank(T.input.file) {
			return fmt.Errorf("--file is required for %s.", T.action)
		}
	case "create":
		return T.requireWebhookFields()
	case "delete":
		if IsBlank(T.input.uuid) && !T.input.all {
			return fmt.Errorf("delete requires --uuid=<uuid of webhook>, or --all to delete every webhook.")
		}
	}

	return nil
}

// subscription_all is the NATS subject wildcard that matches every subject.
const subscription_all = ">"

// expandSubscriptions maps the convenience keyword "all" (case-insensitive) to
// the catch-all wildcard ">". If any entry is "all", the result collapses to a
// single ">", which subscribes to every subject.
func expandSubscriptions(subs []string) []string {
	for _, s := range subs {
		if strings.EqualFold(strings.TrimSpace(s), "all") {
			return []string{subscription_all}
		}
	}
	return subs
}

// requireWebhookFields verifies the fields needed to build a full webhook payload.
func (T *WebhookManagerTask) requireWebhookFields() error {
	if IsBlank(T.input.url) || len(T.input.subscriptions) == 0 {
		return fmt.Errorf("%s requires --url and at least one --subscriptions.", T.action)
	}
	return nil
}

// hasUpdatableField reports whether any field that an update could change was provided.
func (T *WebhookManagerTask) hasUpdatableField() bool {
	return !IsBlank(T.input.url) ||
		!IsBlank(T.input.secret) ||
		len(T.input.subscriptions) > 0 ||
		!IsBlank(T.input.token) ||
		T.Flags.IsSet("disable")
}

func (T *WebhookManagerTask) Main() (err error) {
	switch T.action {
	case "list":
		return T.list()
	case "export":
		return T.export()
	case "import":
		return T.doImport()
	case "create":
		return T.create()
	case "update":
		return T.update()
	case "delete":
		return T.delete()
	}
	return fmt.Errorf("Unknown action: %s", T.action)
}

// writeOptions builds the optional create/full-update params from the input flags.
func (T *WebhookManagerTask) writeOptions() PostJSON {
	opts := PostJSON{"enabled": !T.input.disable}
	if !IsBlank(T.input.secret) {
		opts["secret"] = T.input.secret
	}
	if !IsBlank(T.input.token) {
		opts["token"] = T.input.token
	}
	return opts
}

// printWebhook logs a single webhook in a human-readable form.
func (T *WebhookManagerTask) printWebhook(w KiteWebhook) {
	state := "enabled"
	if !w.Enabled {
		state = "disabled"
	}
	Log("  %s  [%s]  %s", w.ID, state, w.URL)
	Log("    subscriptions: %s", strings.Join(w.Subscriptions, ", "))
	if !IsBlank(w.Status.Status) {
		Log("    status: %s (%s)", w.Status.Status, w.Status.Description)
	}
}

func (T *WebhookManagerTask) list() (err error) {
	webhooks, err := T.KW.Webhooks()
	if err != nil {
		return err
	}
	if len(webhooks) == 0 {
		Notice("No webhooks are currently configured.")
		return nil
	}
	Log("Found %d webhook(s):", len(webhooks))
	for _, w := range webhooks {
		T.printWebhook(w)
	}
	return nil
}

func (T *WebhookManagerTask) export() (err error) {
	exported := T.Report.Tally("Webhooks Exported")

	var webhooks []KiteWebhook

	// Export a single webhook when a uuid is supplied, otherwise export all.
	if !IsBlank(T.input.uuid) {
		webhook, err := T.KW.Webhook(T.input.uuid).Info()
		if err != nil {
			return err
		}
		webhooks = append(webhooks, webhook)
	} else {
		webhooks, err = T.KW.Webhooks()
		if err != nil {
			return err
		}
	}

	out := make([]webhookExport, 0, len(webhooks))
	for _, w := range webhooks {
		out = append(out, webhookExport{
			ID:            w.ID,
			URL:           w.URL,
			Enabled:       w.Enabled,
			Subscriptions: w.Subscriptions,
		})
		exported.Add(1)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(T.input.file, data, 0644); err != nil {
		return err
	}

	Log("Exported %d webhook(s) to %s.", len(out), T.input.file)
	if len(out) > 0 {
		Warn("Secrets are not returned by the API and were not exported; set a 'secret' on any entry that needs one before importing.")
	}
	return nil
}

func (T *WebhookManagerTask) doImport() (err error) {
	imported := T.Report.Tally("Webhooks Imported")

	data, err := os.ReadFile(T.input.file)
	if err != nil {
		return err
	}

	var in []webhookExport
	if err = json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("Could not parse %s: %w", T.input.file, err)
	}
	if len(in) == 0 {
		Notice("No webhooks found in %s.", T.input.file)
		return nil
	}

	for i, w := range in {
		w.Subscriptions = expandSubscriptions(w.Subscriptions)
		if IsBlank(w.URL) || len(w.Subscriptions) == 0 {
			Err("Entry %d (%s): skipping, url and subscriptions are required for import.", i+1, w.URL)
			continue
		}
		opts := PostJSON{"enabled": w.Enabled}
		if !IsBlank(w.Secret) {
			opts["secret"] = w.Secret
		}
		if !IsBlank(w.Token) {
			opts["token"] = w.Token
		}
		created, err := T.KW.CreateWebhook(w.URL, w.Subscriptions, opts)
		if err != nil {
			Err("Entry %d (%s): %v", i+1, w.URL, err)
			continue
		}
		imported.Add(1)
		Log("Imported webhook %s (%s).", created.ID, created.URL)
	}

	Log("Imported %d of %d webhook(s) from %s.", imported.Value(), len(in), T.input.file)
	return nil
}

func (T *WebhookManagerTask) create() (err error) {
	created := T.Report.Tally("Webhooks Created")

	webhook, err := T.KW.CreateWebhook(T.input.url, T.input.subscriptions, T.writeOptions())
	if err != nil {
		return err
	}
	created.Add(1)
	Log("Created webhook:")
	T.printWebhook(webhook)
	return nil
}

func (T *WebhookManagerTask) update() (err error) {
	updated := T.Report.Tally("Webhooks Updated")

	var webhook KiteWebhook

	// A full update (PUT) replaces the webhook and requires url and
	// subscriptions. When only a subset is supplied, partially update (PATCH)
	// just the provided fields.
	if !IsBlank(T.input.url) && len(T.input.subscriptions) > 0 {
		webhook, err = T.KW.Webhook(T.input.uuid).Update(T.input.url, T.input.subscriptions, T.writeOptions())
	} else {
		payload := PostJSON{}
		if !IsBlank(T.input.url) {
			payload["url"] = T.input.url
		}
		if !IsBlank(T.input.secret) {
			payload["secret"] = T.input.secret
		}
		if len(T.input.subscriptions) > 0 {
			payload["subscriptions"] = T.input.subscriptions
		}
		if !IsBlank(T.input.token) {
			payload["token"] = T.input.token
		}
		// Only touch enabled when --disable was explicitly provided, so a
		// partial update never silently re-enables a disabled webhook.
		if T.Flags.IsSet("disable") {
			payload["enabled"] = !T.input.disable
		}
		webhook, err = T.KW.Webhook(T.input.uuid).Patch(payload)
	}
	if err != nil {
		return err
	}
	updated.Add(1)
	Log("Updated webhook:")
	T.printWebhook(webhook)
	return nil
}

func (T *WebhookManagerTask) delete() (err error) {
	if T.input.all {
		return T.deleteAll()
	}

	deleted := T.Report.Tally("Webhooks Deleted")

	if err = T.KW.Webhook(T.input.uuid).Delete(); err != nil {
		return err
	}
	deleted.Add(1)
	Log("Deleted webhook %s.", T.input.uuid)
	return nil
}

// deleteAll deletes every configured webhook, prompting for confirmation first.
// Deletion continues past individual failures, which are recorded as errors.
func (T *WebhookManagerTask) deleteAll() (err error) {
	deleted := T.Report.Tally("Webhooks Deleted")

	webhooks, err := T.KW.Webhooks()
	if err != nil {
		return err
	}
	if len(webhooks) == 0 {
		Notice("No webhooks are currently configured.")
		return nil
	}

	// Pause the loading animation so it doesn't clobber the prompt.
	PleaseWait.Hide()
	confirmed := ConfirmDefault(fmt.Sprintf("Delete ALL %d webhook(s)?", len(webhooks)), false)
	PleaseWait.Show()
	if !confirmed {
		Notice("Aborted; no webhooks were deleted.")
		return nil
	}

	for _, w := range webhooks {
		if err := T.KW.Webhook(w.ID).Delete(); err != nil {
			Err("%s (%s): %v", w.ID, w.URL, err)
			continue
		}
		deleted.Add(1)
		Log("Deleted webhook %s (%s).", w.ID, w.URL)
	}

	Log("Deleted %d of %d webhook(s).", deleted.Value(), len(webhooks))
	return nil
}
