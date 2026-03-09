package files

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExtractTextDOCX(t *testing.T) {
	data := buildDOCX(t, "Main body text", "Header text", "Footer text")
	txt, err := ExtractText(context.Background(), "sample.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
	if err != nil {
		t.Fatalf("ExtractText() error: %v", err)
	}
	if !strings.Contains(txt, "Main body text") {
		t.Fatalf("body text not found: %s", txt)
	}
	if !strings.Contains(txt, "Header text") {
		t.Fatalf("header text not found: %s", txt)
	}
	if !strings.Contains(txt, "Footer text") {
		t.Fatalf("footer text not found: %s", txt)
	}
}

func buildDOCX(t *testing.T, body, header, footer string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	files := map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + body + `</w:t></w:r></w:p></w:body></w:document>`,
		"word/header1.xml":  `<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>` + header + `</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml":  `<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>` + footer + `</w:t></w:r></w:p></w:ftr>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write() error: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	return buf.Bytes()
}

