package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// sendMailHook allows tests to override SMTP sending behavior
var sendMailHook = smtp.SendMail

type Email struct {
	Host, User, Pass string
	Port             int
	To               []string
}

func (e *Email) Name() string { return "Email" }

func (e *Email) Send(ctx context.Context, title, message string) error {
	addr := fmt.Sprintf("%s:%d", e.Host, e.Port)
	auth := smtp.PlainAuth("", e.User, e.Pass, e.Host)
	body := fmt.Sprintf("To: %s\r\nSubject: [Dockhand] %s\r\n\r\n%s", strings.Join(e.To, ","), title, message)
	return sendMailHook(addr, auth, e.User, e.To, []byte(body))
}
