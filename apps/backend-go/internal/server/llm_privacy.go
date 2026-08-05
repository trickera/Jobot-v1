package server

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var errAIDataConsentRequired = errors.New("AI data sharing consent required")

var (
	emailPII  = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	urlPII    = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s"<>]+`)
	phonePII  = regexp.MustCompile(`(?:\+?\d[\d(). \-]{6,}\d)`)
	yearRange = regexp.MustCompile(`^\s*(?:19|20)\d{2}\s*[-–—]\s*(?:19|20)\d{2}\s*$`)
	jsonPII   = regexp.MustCompile(`(?i)"(?:email|phone|url)"\s*:\s*"((?:\\.|[^"\\])*)"`)
)

type piiReplacement struct {
	token string
	value string
}

type piiRedaction struct {
	prompt       string
	replacements []piiReplacement
}

func purposeSharesCandidateData(purpose llmPurpose) bool {
	return purpose != "job_analyze"
}

func (r *piiRedaction) replace(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range r.replacements {
		if existing.value == value {
			r.prompt = strings.ReplaceAll(r.prompt, value, existing.token)
			return
		}
	}
	token := "__SENCIA_PII_" + strconv.Itoa(len(r.replacements)+1) + "__"
	r.prompt = strings.ReplaceAll(r.prompt, value, token)
	r.replacements = append(r.replacements, piiReplacement{token: token, value: value})
}

func (r piiRedaction) restore(raw string) string {
	for _, replacement := range r.replacements {
		encoded, _ := json.Marshal(replacement.value)
		escaped := strings.TrimSuffix(strings.TrimPrefix(string(encoded), `"`), `"`)
		raw = strings.ReplaceAll(raw, replacement.token, escaped)
	}
	return raw
}

func candidateNameAfterMarker(prompt, marker string) string {
	index := strings.Index(prompt, marker)
	if index < 0 {
		return ""
	}
	text := prompt[index+len(marker):]
	return guessNameFromResumeText(strings.TrimLeft(text, "\r\n "))
}

func basicsNameFromJSON(prompt string) string {
	start := strings.Index(strings.ToLower(prompt), `"basics"`)
	if start < 0 {
		return ""
	}
	section := prompt[start:]
	end := strings.Index(section, "}")
	if end >= 0 {
		section = section[:end]
	}
	nameField := regexp.MustCompile(`(?i)"name"\s*:\s*"((?:\\.|[^"\\])*)"`)
	match := nameField.FindStringSubmatch(section)
	if len(match) != 2 {
		return ""
	}
	var value string
	if err := json.Unmarshal([]byte(`"`+match[1]+`"`), &value); err != nil {
		return ""
	}
	return value
}

// redactPromptPII replaces direct identifiers with reversible opaque tokens.
// The provider sees no name, email, phone or URL; the local decoder receives
// the restored JSON, so exports keep the user's real contact data without ever
// asking the model to reproduce it.
func redactPromptPII(_ llmPurpose, prompt string) piiRedaction {
	r := piiRedaction{prompt: prompt}

	for _, marker := range []string{"TEXTO DO CURRÍCULO:", "=== CURRICULO ==="} {
		r.replace(candidateNameAfterMarker(r.prompt, marker))
	}
	r.replace(basicsNameFromJSON(r.prompt))

	for _, match := range jsonPII.FindAllStringSubmatch(r.prompt, -1) {
		if len(match) != 2 {
			continue
		}
		var value string
		if err := json.Unmarshal([]byte(`"`+match[1]+`"`), &value); err == nil {
			r.replace(value)
		}
	}
	for _, value := range emailPII.FindAllString(r.prompt, -1) {
		r.replace(value)
	}
	for _, value := range urlPII.FindAllString(r.prompt, -1) {
		r.replace(value)
	}
	for _, value := range phonePII.FindAllString(r.prompt, -1) {
		if yearRange.MatchString(value) {
			continue
		}
		digits := 0
		for _, char := range value {
			if char >= '0' && char <= '9' {
				digits++
			}
		}
		if digits >= 8 && digits <= 15 {
			r.replace(value)
		}
	}
	return r
}
