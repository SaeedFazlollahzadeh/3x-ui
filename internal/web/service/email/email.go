package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// EmailService sends email notifications via SMTP.
type EmailService struct {
	settingService service.SettingService
}

// SMTPTestResult holds the result of an SMTP connection test.
type SMTPTestResult struct {
	Success bool   `json:"success"`
	Stage   string `json:"stage"`   // "connect" | "auth" | "send"
	Message string `json:"message"` // classified error message
}

// NewEmailService creates a new EmailService.
func NewEmailService(settingService service.SettingService) *EmailService {
	return &EmailService{settingService: settingService}
}

// smtpConnectTimeout bounds the TCP dial. smtpDeadline bounds every SMTP
// protocol step after the connection is up, so a server that accepts the socket
// but then stalls cannot block the sender goroutine (and leak its socket) long
// after the caller's own timeout has already fired. smtpDeadline is a var only
// so tests can shorten it.
const smtpConnectTimeout = 10 * time.Second

var smtpDeadline = 30 * time.Second

// smtpMaxServerBytes caps what one session reads from the server, TLS handshake
// included: net/smtp buffers a reply of any length, so the deadline alone is no bound.
const smtpMaxServerBytes = 1 << 20

var errSMTPReplyTooLarge = errors.New("smtp server sent more data than a session may read")

// limitedConn fails every Read once the peer has sent remaining bytes.
type limitedConn struct {
	net.Conn
	remaining int64
}

func (c *limitedConn) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, errSMTPReplyTooLarge
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= int64(n)
	return n, err
}

// dialSMTP opens a connection capped at smtpMaxServerBytes; timeout covers the
// dial and, with implicitTLS, the handshake.
func dialSMTP(addr, host string, timeout time.Duration, implicitTLS bool) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// The cap sits below TLS: net/smtp allows PLAIN auth only on a *tls.Conn,
	// and this keeps it in force across a later STARTTLS upgrade.
	limited := &limitedConn{Conn: raw, remaining: smtpMaxServerBytes}
	if !implicitTLS {
		return limited, nil
	}
	conn := tls.Client(limited, &tls.Config{ServerName: host})
	if err = conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

// Send sends an HTML email to all configured recipients.
func (s *EmailService) Send(subject, body string) error {
	return s.sendConfigured(func(w io.Writer, from, fromName string, recipients []string) error {
		_, err := w.Write(buildMessage(from, fromName, recipients, subject, body))
		return err
	})
}

func (s *EmailService) sendConfigured(write func(io.Writer, string, string, []string) error) error {
	host, err := s.settingService.GetSmtpHost()
	if err != nil || host == "" {
		return fmt.Errorf("smtp host not configured")
	}
	port, err := s.settingService.GetSmtpPort()
	if err != nil || port <= 0 {
		port = 587
	}
	username, _ := s.settingService.GetSmtpUsername()
	password, _ := s.settingService.GetSmtpPassword()
	fromAddr, _ := s.settingService.GetSmtpFrom()
	fromName, _ := s.settingService.GetSmtpFromName()
	toStr, _ := s.settingService.GetSmtpTo()
	encryptionType, _ := s.settingService.GetSmtpEncryptionType()

	from := fromAddr
	if from == "" {
		from = username
	}
	if from == "" {
		return fmt.Errorf("smtp from not configured")
	}
	from, fromName = resolveFrom(from, fromName)

	recipients := parseRecipients(toStr)
	if len(recipients) == 0 {
		return fmt.Errorf("no recipients configured")
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	var auth smtp.Auth
	if username != "" && password != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	return sendSMTP(addr, host, encryptionType, auth, from, recipients, func(w io.Writer) error {
		return write(w, from, fromName, recipients)
	})
}

// TestConnection tests SMTP connection stage by stage and sends a test email.
func (s *EmailService) TestConnection() SMTPTestResult {
	host, err := s.settingService.GetSmtpHost()
	if err != nil || host == "" {
		return SMTPTestResult{false, "connect", "smtpHostNotConfigured"}
	}
	port, err := s.settingService.GetSmtpPort()
	if err != nil || port <= 0 {
		port = 587
	}
	username, _ := s.settingService.GetSmtpUsername()
	password, _ := s.settingService.GetSmtpPassword()
	fromAddr, _ := s.settingService.GetSmtpFrom()
	fromName, _ := s.settingService.GetSmtpFromName()
	toStr, _ := s.settingService.GetSmtpTo()
	encryptionType, _ := s.settingService.GetSmtpEncryptionType()

	from := fromAddr
	if from == "" {
		from = username
	}
	if from == "" {
		return SMTPTestResult{false, "send", "smtpFromNotConfigured"}
	}
	from, fromName = resolveFrom(from, fromName)

	recipients := parseRecipients(toStr)
	if len(recipients) == 0 {
		return SMTPTestResult{false, "send", "smtpNoRecipients"}
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// Stage 1: Connect
	conn, err := dialSMTP(addr, host, 5*time.Second, encryptionType == "tls")
	if err != nil {
		return SMTPTestResult{false, "connect", classifySMTPError(err)}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpDeadline))

	// Stage 2: Handshake + Auth
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return SMTPTestResult{false, "auth", classifySMTPError(err)}
	}
	defer client.Close()

	if err = client.Hello("localhost"); err != nil {
		return SMTPTestResult{false, "auth", classifySMTPError(err)}
	}

	if err := upgradeSMTP(client, host, encryptionType); err != nil {
		return SMTPTestResult{false, "auth", classifySMTPError(err)}
	}

	if username != "" && password != "" {
		auth := smtp.PlainAuth("", username, password, host)
		if err = client.Auth(auth); err != nil {
			return SMTPTestResult{false, "auth", classifySMTPError(err)}
		}
	}

	// Stage 3: Send test email
	if err = client.Mail(from); err != nil {
		return SMTPTestResult{false, "send", classifySMTPError(err)}
	}
	for _, r := range recipients {
		if err = client.Rcpt(r); err != nil {
			return SMTPTestResult{false, "send", classifySMTPError(err)}
		}
	}

	msg := buildMessage(from, fromName, recipients, "[3x-ui] Test email",
		`<html><body style="font-family:monospace;font-size:14px">
<h2>Test email from 3x-ui</h2>
<p>If you received this, SMTP is configured correctly.</p>
</body></html>`)

	w, err := client.Data()
	if err != nil {
		return SMTPTestResult{false, "send", classifySMTPError(err)}
	}
	if _, err = w.Write(msg); err != nil {
		return SMTPTestResult{false, "send", classifySMTPError(err)}
	}
	if err = w.Close(); err != nil {
		return SMTPTestResult{false, "send", classifySMTPError(err)}
	}

	return SMTPTestResult{true, "send", "smtpTestSuccess"}
}

func upgradeSMTP(client *smtp.Client, host, encryption string) error {
	switch encryption {
	case "none", "tls":
		return nil
	case "starttls":
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer STARTTLS")
		}
		return client.StartTLS(&tls.Config{ServerName: host})
	default:
		return fmt.Errorf("unknown SMTP encryption type: %s", encryption)
	}
}

func (s *EmailService) sendPlain(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error {
	return sendSMTP(addr, host, "none", auth, from, to, func(w io.Writer) error {
		_, err := w.Write(msg)
		return err
	})
}

func sendSMTP(addr, host, encryption string, auth smtp.Auth, from string, to []string, write func(io.Writer) error) error {
	deadline := time.Now().Add(smtpDeadline)
	conn, err := dialSMTP(addr, host, min(smtpDeadline, smtpConnectTimeout), encryption == "tls")
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return err
	}
	if err := upgradeSMTP(client, host, encryption); err != nil {
		return err
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := client.Rcpt(r); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if err := write(writer); err != nil {
		return err
	}
	return writer.Close()
}

// SendTest sends a test email and returns any error with detail.
func (s *EmailService) SendTest() error {
	return s.Send(
		"[3x-ui] Test email",
		`<html><body style="font-family:monospace;font-size:14px">
<h2>Test email from 3x-ui</h2>
<p>If you received this, SMTP is configured correctly.</p>
</body></html>`,
	)
}

// classifySMTPError maps a raw SMTP error to an i18n key. The key is returned
// unprefixed: the caller renders it under "pages.settings.", as does every
// other Message this file produces.
func classifySMTPError(err error) string {
	msg := err.Error()
	msgLower := strings.ToLower(msg)

	switch {
	case strings.Contains(msg, "535") || strings.Contains(msgLower, "authentication"):
		return "smtpErrorAuth"
	case strings.Contains(msg, "534") || strings.Contains(msgLower, "starttls"):
		return "smtpErrorStarttls"
	case strings.Contains(msg, "465") || strings.Contains(msgLower, "tls"):
		return "smtpErrorTls"
	case strings.Contains(msgLower, "connection refused") || strings.Contains(msgLower, "dial"):
		return "smtpErrorRefused"
	case strings.Contains(msgLower, "timeout"):
		return "smtpErrorTimeout"
	case strings.Contains(msg, "550") || strings.Contains(msgLower, "relay"):
		return "smtpErrorRelay"
	case strings.Contains(msgLower, "eof"):
		return "smtpErrorEof"
	default:
		return "smtpErrorUnknown"
	}
}

func parseRecipients(toStr string) []string {
	if toStr == "" {
		return nil
	}
	var out []string
	for s := range strings.SplitSeq(toStr, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// buildMessage assembles an RFC 5322 message. It emits the two mandatory
// header fields (Date, From) plus Message-ID, so strict receivers such as Gmail
// accept it and spam filters do not penalize a missing date or message id. The
// From header is a proper name-addr ("Name" <addr>) via net/mail, and a
// non-ASCII subject is RFC 2047 encoded.
// headerSanitizer drops CR/LF so a crafted address or name cannot inject extra
// header lines. Configured addresses are already validated at save time
// (entity.AllSetting.CheckValid), this is defense in depth for buildMessage.
var headerSanitizer = strings.NewReplacer("\r", "", "\n", "")

func resolveFrom(from, fromName string) (string, string) {
	parsed, err := mail.ParseAddress(from)
	if err != nil {
		return from, fromName
	}
	if fromName == "" {
		fromName = parsed.Name
	}
	return parsed.Address, fromName
}

func buildMessageHeaders(fromAddr, fromName string, to []string, subject string) string {
	fromAddr = headerSanitizer.Replace(fromAddr)
	fromName = headerSanitizer.Replace(fromName)
	from := (&mail.Address{Name: fromName, Address: fromAddr}).String()

	domain := "localhost"
	if at := strings.LastIndex(fromAddr, "@"); at >= 0 && at+1 < len(fromAddr) {
		domain = fromAddr[at+1:]
	}
	var token [16]byte
	_, _ = rand.Read(token[:])
	messageID := fmt.Sprintf("<%s@%s>", hex.EncodeToString(token[:]), domain)

	var msg strings.Builder
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&msg, "Message-ID: %s\r\n", messageID)
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	return msg.String()
}

func buildMessage(fromAddr, fromName string, to []string, subject, body string) []byte {
	return []byte(buildMessageHeaders(fromAddr, fromName, to, subject) + "Content-Type: text/html; charset=utf-8\r\n\r\n" + body)
}
