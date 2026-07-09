package pubsub

import (
	"fmt"
	"strings"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
)

// zeroByteFile is a normalized view of a 0-byte upload, sourced either from a
// webhook delivery payload or an activity-log entry, so both the webhook and
// polling tasks produce an identical administrator notification.
type zeroByteFile struct {
	ID       string
	Name     string
	Location string
	Uploader string
	When     time.Time
}

// composeZeroByteBody builds the notification email body.
func composeZeroByteBody(f zeroByteFile) string {
	uploader := f.Uploader
	if IsBlank(uploader) {
		uploader = "(unknown)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "A 0-byte file was uploaded to Kiteworks.\n\n")
	fmt.Fprintf(&b, "File:     %s\n", f.Name)
	fmt.Fprintf(&b, "Location: %s\n", f.Location)
	fmt.Fprintf(&b, "Uploader: %s\n", uploader)
	if !f.When.IsZero() {
		fmt.Fprintf(&b, "Uploaded: %s\n", f.When.Format(time.RFC1123))
	}
	if !IsBlank(f.ID) {
		fmt.Fprintf(&b, "File ID:  %s\n", f.ID)
	}
	return b.String()
}

// sendZeroByteAlert composes and sends the administrator notification email.
func sendZeroByteAlert(kw KWSession, to []string, subject string, f zeroByteFile) error {
	mail := kw.NewMail()
	mail.To = to
	mail.Subject = subject
	mail.Body = composeZeroByteBody(f)
	_, err := mail.Send()
	return err
}

// firstSet returns value if it is non-blank, otherwise fallback.
func firstSet(value, fallback string) string {
	if IsBlank(value) {
		return fallback
	}
	return value
}
