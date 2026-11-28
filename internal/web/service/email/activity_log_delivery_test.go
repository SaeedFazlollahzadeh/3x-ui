package email

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"testing"
)

func TestActivityBackupEmailAttachment(t *testing.T) {
	data := bytes.Repeat([]byte("test zip payload"), 10)
	const name = "activity-logs_2026_11_28_00_00_00.zip"
	var output bytes.Buffer
	if err := writeActivityLogMessage(&output, "panel@example.com", "3x-ui", []string{"admin@example.com"}, name, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(&output)
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("To") != "admin@example.com" {
		t.Fatalf("unexpected recipient: %s", message.Header.Get("To"))
	}
	media, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || media != "multipart/mixed" {
		t.Fatalf("content type %q: %v", media, err)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	body, err := parts.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	attachment, err := parts.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if attachment.FileName() != name || attachment.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("unexpected attachment headers: %v", attachment.Header)
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, attachment))
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("decoding attachment failed: %v", err)
	}
}
