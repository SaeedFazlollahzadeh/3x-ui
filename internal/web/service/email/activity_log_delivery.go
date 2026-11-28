package email

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
)

// SendActivityLogBackup sends the archive using the configured SMTP sender,
// recipients and transport. The archive is streamed rather than held in RAM.
func (s *EmailService) SendActivityLogBackup(path, filename string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return s.sendConfigured(func(w io.Writer, from, fromName string, recipients []string) error {
		return writeActivityLogMessage(w, from, fromName, recipients, filename, file)
	})
}

func writeActivityLogMessage(w io.Writer, from, fromName string, recipients []string, filename string, archive io.Reader) error {
	parts := multipart.NewWriter(w)
	contentType := mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": parts.Boundary()})
	headers := buildMessageHeaders(from, fromName, recipients, "[3x-ui] Nightly activity log backup")
	if _, err := fmt.Fprintf(w, "%sContent-Type: %s\r\n\r\n", headers, contentType); err != nil {
		return err
	}
	text, err := parts.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(text, "The nightly activity log backup is attached.\r\n"); err != nil {
		return err
	}
	part, err := parts.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"application/zip"},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": filename})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	if err := writeAttachmentBase64(part, archive); err != nil {
		return err
	}
	return parts.Close()
}

func writeAttachmentBase64(w io.Writer, input io.Reader) error {
	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(w)
	// 57 input bytes encode to one MIME line of 76 characters.
	var raw [57]byte
	var line [78]byte
	for {
		n, err := io.ReadFull(reader, raw[:])
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		if n > 0 {
			length := base64.StdEncoding.EncodedLen(n)
			base64.StdEncoding.Encode(line[:length], raw[:n])
			line[length], line[length+1] = '\r', '\n'
			if _, err := writer.Write(line[:length+2]); err != nil {
				return err
			}
		}
		if err != nil {
			return writer.Flush()
		}
	}
}
