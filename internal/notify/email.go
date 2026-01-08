// Package notify provides notification backends for Dockhand.
package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// sendMailHook allows tests to override SMTP sending behavior
// sendMailHook allows tests to override SMTP sending behavior.
var sendMailHook = smtp.SendMail

// Email sends notifications via SMTP.
type Email struct {
	Host, User, Pass string
	Port             int
	To               []string
}
// Name returns the notifier backend name.
func (e *Email) Name() string {
	_ = e
	return "Email"
}

// Send sends an email with the provided title and message via SMTP.
func (e *Email) Send(ctx context.Context, title, message string) error {
	_ = ctx
	addr := fmt.Sprintf("%s:%d", e.Host, e.Port)
	auth := smtp.PlainAuth("", e.User, e.Pass, e.Host)
	header := fmt.Sprintf(
		"To: %s\r\nSubject: [Dockhand] %s\r\n\r\n",
		strings.Join(e.To, ","),
		title,
	)
	body := header + message
	return sendMailHook(addr, auth, e.User, e.To, []byte(body))
}
