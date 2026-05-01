package notify

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

type Service struct {
	host string
	port string
	user string
	pass string
	from string
}

func NewService(host, port, user, pass, from string) *Service {
	return &Service{host: host, port: port, user: user, pass: pass, from: from}
}

func (s *Service) Send(to []string, subject, body string) error {
	if s.host == "" || s.port == "" || s.from == "" || len(to) == 0 {
		log.Printf("[NOTIFY] SMTP not configured, skipping email: %s", subject)
		return nil
	}
	addr := fmt.Sprintf("%s:%s", s.host, s.port)
	header := map[string]string{
		"From":         s.from,
		"To":           strings.Join(to, ", "),
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=UTF-8",
	}
	var msg strings.Builder
	for k, v := range header {
		msg.WriteString(k)
		msg.WriteString(": ")
		msg.WriteString(v)
		msg.WriteString("\r\n")
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)

	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	return smtp.SendMail(addr, auth, s.from, to, []byte(msg.String()))
}
