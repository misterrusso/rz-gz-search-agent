package filter

import (
	"path/filepath"
	"strings"

	"rz_gz_search_agent/internal/model"
)

var preferredNameTokens = []string{
	"тех", "technical", "spec", "specification", "техспеци", "тз", "terms",
}

func SelectBestSpecDocument(docs []model.DocumentRef) (model.DocumentRef, bool) {
	if len(docs) == 0 {
		return model.DocumentRef{}, false
	}

	type scored struct {
		doc   model.DocumentRef
		score int
	}
	candidates := make([]scored, 0, len(docs))
	for _, d := range docs {
		if !isSupported(d) {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(d.Name))
		score := 0
		for _, token := range preferredNameTokens {
			if strings.Contains(name, token) {
				score += 3
			}
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".docx" || ext == ".pdf" || ext == ".txt" {
			score += 2
		}
		if strings.Contains(name, "spec") || strings.Contains(name, "тех") {
			score++
		}
		candidates = append(candidates, scored{doc: d, score: score})
	}
	if len(candidates) == 0 {
		return model.DocumentRef{}, false
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}
	return best.doc, true
}

func isSupported(d model.DocumentRef) bool {
	name := strings.ToLower(strings.TrimSpace(d.Name))
	mime := strings.ToLower(strings.TrimSpace(d.MIMEType))
	return strings.HasSuffix(name, ".pdf") ||
		strings.HasSuffix(name, ".docx") ||
		strings.HasSuffix(name, ".txt") ||
		strings.Contains(mime, "pdf") ||
		strings.Contains(mime, "wordprocessingml.document") ||
		strings.Contains(mime, "text/plain")
}
