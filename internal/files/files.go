package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	pdf "github.com/ledongthuc/pdf"
)

type FileType string

const (
	FileTypePDF     FileType = "pdf"
	FileTypeDOCX    FileType = "docx"
	FileTypeDOC     FileType = "doc"
	FileTypeText    FileType = "text"
	FileTypeUnknown FileType = "unknown"
)

func DetectFileType(name string, mime string, data []byte) FileType {
	lowName := strings.ToLower(strings.TrimSpace(name))
	lowMIME := strings.ToLower(strings.TrimSpace(mime))
	if strings.HasSuffix(lowName, ".docx") || strings.Contains(lowMIME, "wordprocessingml.document") {
		return FileTypeDOCX
	}
	if strings.HasSuffix(lowName, ".doc") || strings.Contains(lowMIME, "msword") {
		return FileTypeDOC
	}
	if strings.HasSuffix(lowName, ".pdf") || strings.Contains(lowMIME, "pdf") {
		return FileTypePDF
	}
	detected := strings.ToLower(http.DetectContentType(data))
	if strings.Contains(detected, "pdf") {
		return FileTypePDF
	}
	if strings.Contains(detected, "text/plain") || strings.HasSuffix(lowName, ".txt") {
		return FileTypeText
	}
	ext := strings.ToLower(filepath.Ext(lowName))
	if ext == ".docx" {
		return FileTypeDOCX
	}
	if ext == ".doc" {
		return FileTypeDOC
	}
	if ext == ".pdf" {
		return FileTypePDF
	}
	return FileTypeUnknown
}

func ExtractText(ctx context.Context, filename string, mime string, data []byte) (string, error) {
	_ = ctx
	ft := DetectFileType(filename, mime, data)
	switch ft {
	case FileTypeDOCX:
		return extractDOCXText(data)
	case FileTypePDF:
		return extractPDFText(data)
	case FileTypeDOC:
		return "", errors.New("unsupported format: legacy .doc is not implemented")
	case FileTypeText:
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported file type: %s", ft)
	}
}

func extractDOCXText(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	var parts []string
	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if name != "word/document.xml" && !strings.HasPrefix(name, "word/header") && !strings.HasPrefix(name, "word/footer") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open docx part %s: %w", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return "", fmt.Errorf("read docx part %s: %w", f.Name, err)
		}
		txt, err := extractWordMLText(b)
		if err != nil {
			return "", fmt.Errorf("parse docx part %s: %w", f.Name, err)
		}
		if strings.TrimSpace(txt) != "" {
			parts = append(parts, txt)
		}
	}
	result := strings.TrimSpace(strings.Join(parts, "\n"))
	if result == "" {
		return "", errors.New("docx contains no extractable text")
	}
	return result, nil
}

func extractWordMLText(xmlData []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(xmlData))
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if el.Name.Local == "t" {
				var text string
				if err := dec.DecodeElement(&text, &el); err != nil {
					return "", err
				}
				if strings.TrimSpace(text) != "" {
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
	}
	return sb.String(), nil
}

func extractPDFText(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	textReader, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract pdf text: %w", err)
	}
	extracted, err := io.ReadAll(textReader)
	if err != nil {
		return "", fmt.Errorf("read extracted pdf text: %w", err)
	}
	out := strings.TrimSpace(string(extracted))
	if out == "" {
		return "", errors.New("pdf contains no extractable text; OCR not implemented")
	}
	return out, nil
}
