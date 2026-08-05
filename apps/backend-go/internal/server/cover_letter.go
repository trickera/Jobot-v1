package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// coverLetterRequest is the contract for POST /api/v1/resume/cover-letter
// (CH-06/D8). The letter is generated strictly from the candidate's own
// canonical resume plus the job context — never from invented facts.
type coverLetterRequest struct {
	Canonical      CanonicalResume `json:"canonical"`
	JobID          string          `json:"jobId,omitempty"`
	JobDescription string          `json:"jobDescription,omitempty"`
	Company        string          `json:"company,omitempty"`
	Role           string          `json:"role,omitempty"`
	Language       string          `json:"language,omitempty"` // en(default)|pt|es|auto
	Tone           string          `json:"tone,omitempty"`     // direct|professional|consultative (default professional)
	MaxWords       int             `json:"maxWords,omitempty"` // default 220
	Gap            *gapResult      `json:"gap,omitempty"`
	Confirmed      []string        `json:"confirmed,omitempty"`
}

type coverLetterResponse struct {
	ID        string   `json:"id,omitempty"` // set only when persisted (jobId present)
	Markdown  string   `json:"markdown"`
	PlainText string   `json:"plainText"`
	Warnings  []string `json:"warnings"`
	// RequiresConfirmation is set when the letter still carries a claim the
	// resume cannot back after the retry. The client must not let the user
	// copy or export it until they explicitly confirm the claim is true.
	RequiresConfirmation bool   `json:"requiresConfirmation"`
	ProviderUsed         string `json:"providerUsed,omitempty"`
}

const (
	defaultCoverLetterMaxWords = 220
	// Upper bound: the value is injected verbatim into the AI prompt, so an
	// arbitrarily large request must not inflate the generation target.
	maxCoverLetterMaxWords = 600
)

func resolveCoverLetterMaxWords(requested int) int {
	if requested <= 0 {
		return defaultCoverLetterMaxWords
	}
	if requested > maxCoverLetterMaxWords {
		return maxCoverLetterMaxWords
	}
	return requested
}

// coverLetterPromptInput carries everything the cover-letter prompt needs.
// Forbidden and Violated are the anti-invention half: the job description is
// injected verbatim, so without naming the terms the resume cannot back the
// model happily echoes them back as if the candidate had them.
type coverLetterPromptInput struct {
	CanonicalJSON  string
	JobDescription string
	Company        string
	Role           string
	Language       string
	Tone           string
	MaxWords       int
	// Forbidden are the job's terms the resume cannot evidence and the user
	// has not confirmed.
	Forbidden []string
	// Violated are the forbidden terms a previous draft used anyway; naming
	// them makes the retry concrete instead of just repeating the rule.
	Violated []string
}

// coverLetterPrompt builds the prompt asking the AI to draft a cover letter
// grounded strictly in the candidate's own resume (D8 / anti-invention gate).
func coverLetterPrompt(in coverLetterPromptInput) string {
	jobDescription := promptJobDescription(in.JobDescription)
	return fmt.Sprintf(`%s

Escreva uma carta de apresentação (cover letter) para a vaga abaixo, usando APENAS o que
está no CURRÍCULO (JSON canonical) do candidato. Devolva o JSON: {"markdown": "..."}
(markdown simples: parágrafos corridos e, no máximo, uma lista de bullets; sem endereço
nem data no topo). Tom: %s. Máximo de %d palavras.
REGRAS CRÍTICAS (autenticidade):
- NUNCA invente experiência, cargo, empresa, projeto ou conquista que não esteja no currículo.
- NUNCA afirme conhecer uma ferramenta/tecnologia/skill que não esteja no currículo.
- NUNCA prometa disponibilidade, cidade/localização, pretensão salarial ou data de início —
  essas informações não foram fornecidas aqui.
- NUNCA crie ou cite uma licença ou certificação que não esteja no currículo.
- Pode citar o nome da empresa/vaga (%s / %s) e conectar requisitos reais da descrição a
  fatos REAIS do currículo.
IDIOMA: escreva em %s.
%s%s
CURRÍCULO:
%s
DESCRIÇÃO DA VAGA:
%s`,
		resumeAISystemInstruction, in.Tone, in.MaxWords,
		coalesce(in.Company, "-"), coalesce(in.Role, "-"), in.Language,
		coverLetterForbiddenSection(in.Forbidden), coverLetterViolationSection(in.Violated),
		in.CanonicalJSON, jobDescription)
}

func coverLetterForbiddenSection(forbidden []string) string {
	if len(forbidden) == 0 {
		return ""
	}
	return fmt.Sprintf(`
TERMOS PROIBIDOS (a vaga pede, o currículo NÃO comprova e o candidato NÃO confirmou):
- %s
É PROIBIDO afirmar, sugerir, parafrasear ou reformular qualquer um desses termos como algo
que o candidato tem, sabe, usou ou fez. Não os cite nem para dizer que quer aprendê-los.
Escreva a carta apenas com o que o currículo comprova.
`, strings.Join(forbidden, "\n- "))
}

func coverLetterViolationSection(violated []string) string {
	if len(violated) == 0 {
		return ""
	}
	return fmt.Sprintf(`
A SUA TENTATIVA ANTERIOR FOI REJEITADA porque afirmava: %s.
Reescreva a carta inteira sem essa(s) afirmação(ões), nem em outras palavras.
`, strings.Join(violated, ", "))
}

// coverLetterUnverifiedTerms is the anti-invention gate's input: every job
// term the prior gap analysis could not verify against the resume
// (gap.Missing / gap.ToConfirm) that the user has not explicitly confirmed
// either. These are exactly the claims the letter is forbidden to make.
func coverLetterUnverifiedTerms(base CanonicalResume, gap *gapResult, confirmed []string) []string {
	if gap == nil {
		return nil
	}
	risky := make([]gapItem, 0, len(gap.Missing)+len(gap.ToConfirm))
	risky = append(risky, gap.Missing...)
	risky = append(risky, gap.ToConfirm...)

	terms := make([]string, 0, len(risky))
	seen := map[string]bool{}
	for _, item := range risky {
		term := strings.TrimSpace(item.Term)
		if term == "" {
			continue
		}
		key := normalizeText(term)
		if seen[key] {
			continue
		}
		if _, source, ok := resumeEvidence(base, confirmed, term); ok && resumeEvidenceSupportsClaim(term, source) {
			continue
		}
		seen[key] = true
		terms = append(terms, term)
	}
	return terms
}

// resolveCoverLetterLanguage turns the request's language selector ("en"
// default | "pt" | "es" | "auto") into the English name injected into the
// prompt. "auto" follows the job description's language.
func resolveCoverLetterLanguage(language, jobDescription string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "pt", "pt-br", "portuguese", "português":
		return "Portuguese"
	case "es", "spanish", "español":
		return "Spanish"
	case "auto":
		if looksPortuguese(jobDescription) {
			return "Portuguese"
		}
		if looksSpanish(jobDescription) {
			return "Spanish"
		}
		return "English"
	default:
		return "English"
	}
}

// looksSpanish is a lightweight heuristic (common ES stopwords), the Spanish
// counterpart of looksPortuguese — good enough to steer "auto" language
// detection without a full language-detection dependency.
func looksSpanish(text string) bool {
	lower := strings.ToLower(text)
	markers := []string{
		"vacante", "empresa", "requisitos", "experiencia", "conocimiento",
		"responsabilidades", "años", "habilidades", "solicitamos", "puesto",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func decodeCoverLetterResult(raw string) (string, error) {
	var payload struct {
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &payload); err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}
	markdown := strings.TrimSpace(payload.Markdown)
	if markdown == "" {
		return "", fmt.Errorf("%w: empty markdown", errInvalidAIJSON)
	}
	return markdown, nil
}

// coverLetterPlainText strips the lightweight markdown markers the prompt
// allows (bold, headings) so the UI can offer a plain-text copy/export.
func coverLetterPlainText(markdown string) string {
	replacer := strings.NewReplacer("**", "", "#", "")
	return strings.TrimSpace(replacer.Replace(markdown))
}

// coverLetterViolations is the deterministic half of the anti-invention gate
// for cover letters: any unverified job term (see coverLetterUnverifiedTerms)
// that nonetheless shows up in the generated letter is a violation. Matching
// goes through resumeEvidenceMatches — the same resolver gap analysis and
// tailoring use — so a reworded claim ("WCAG-compliant accessibility audits"
// for "accessibility (WCAG)") is caught, while mentioning only the half the
// resume does back ("accessibility") stays silent. Terms the gap analysis
// verified (gap.Found / gap.Partial) never trip the gate: enforceGapEvidenceGate
// already checked those against the resume's own text. Without a gap (the user
// generated a letter without running gap analysis first), there is nothing
// deterministic to check against, so the prompt-level anti-invention
// instructions are the only gate in that case.
func coverLetterViolations(letter string, gap *gapResult, base CanonicalResume, confirmed []string) []string {
	var violations []string
	for _, term := range coverLetterUnverifiedTerms(base, gap, confirmed) {
		if resumeEvidenceMatches(letter, term) {
			violations = append(violations, term)
		}
	}
	return violations
}

// coverLetterWarnings renders the violations as the client-facing warning
// codes.
func coverLetterWarnings(letter string, gap *gapResult, base CanonicalResume, confirmed []string) []string {
	violations := coverLetterViolations(letter, gap, base, confirmed)
	warnings := make([]string, 0, len(violations))
	for _, term := range violations {
		warnings = append(warnings, "mentions_skill_not_in_resume: "+strings.ToLower(term))
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func (a *api) generateCoverLetterWithProvider(ctx context.Context, config appConfig, canonical CanonicalResume, in coverLetterPromptInput) (string, string, error) {
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return "", "", err
	}
	in.CanonicalJSON = string(canonicalJSON)

	result, err := a.runLLMWithFallback(ctx, "cover_letter", config, coverLetterPrompt(in), a.scraper.generateJSONOnce)
	if err != nil {
		return "", "", err
	}
	markdown, err := decodeCoverLetterResult(result.Raw)
	if err != nil {
		return "", "", err
	}
	return markdown, result.ProviderUsed, nil
}

// resumeCoverLetter drafts a cover letter for the given resume + job context
// (CH-06/D8). It never invents facts: the prompt is instructed to use only
// the candidate's own resume, and coverLetterWarnings re-checks the result
// against the gap analysis the user already ran. When a jobId is present,
// the letter is persisted to cover_letters for later retrieval.
func (a *api) resumeCoverLetter(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload coverLetterRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	canonical := normalizeCanonical(payload.Canonical)
	if err := canonical.Validate(); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", err.Error()))
		return
	}

	description := strings.TrimSpace(payload.JobDescription)
	company := strings.TrimSpace(payload.Company)
	role := strings.TrimSpace(payload.Role)
	jobID := strings.TrimSpace(payload.JobID)
	if jobID != "" {
		job, err := a.configStore.getJobByID(jobID)
		if err != nil {
			writeResumeError(w, resumeErrorFor(http.StatusNotFound, "job_not_found", "Job not found."))
			return
		}
		if description == "" {
			description = strings.TrimSpace(job.Description)
		}
		if company == "" {
			company = strings.TrimSpace(job.Company)
		}
		if role == "" {
			role = strings.TrimSpace(job.Title)
		}
	}
	if description == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "empty_job_description", "No job description provided. Paste one or pick a job first."))
		return
	}

	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for cover letter: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	apiKey, err := a.configStore.aiAPIKey()
	if err != nil {
		a.logger.Printf("load ai api key: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	if strings.TrimSpace(apiKey) == "" {
		writeResumeError(w, resumeErrorFor(http.StatusConflict, "ai_key_required", "This step needs an AI key. Add one in Settings > AI."))
		return
	}

	maxWords := resolveCoverLetterMaxWords(payload.MaxWords)
	tone := strings.TrimSpace(payload.Tone)
	if tone == "" {
		tone = "professional"
	}
	language := resolveCoverLetterLanguage(payload.Language, description)

	in := coverLetterPromptInput{
		JobDescription: description,
		Company:        company,
		Role:           role,
		Language:       language,
		Tone:           tone,
		MaxWords:       maxWords,
		Forbidden:      coverLetterUnverifiedTerms(canonical, payload.Gap, payload.Confirmed),
	}

	aiStart := time.Now()
	markdown, providerUsed, err := a.generateCoverLetterWithProvider(r.Context(), config, canonical, in)
	a.logger.Printf("[ RESUME ] cover-letter: AI call took %.1fs (err=%v)", time.Since(aiStart).Seconds(), err != nil)
	if err != nil {
		a.logger.Printf("resume cover letter: %v", err)
		writeResumeError(w, classifyResumeError(err))
		return
	}

	// The prompt is the first half of the anti-invention gate; this is the
	// second. A draft that claims something the resume cannot back gets one
	// retry naming the exact offending terms — and if that draft still claims
	// it, the letter is handed back gated: warned, not persisted, and only
	// copyable/exportable after the user confirms the claim is true.
	violations := coverLetterViolations(markdown, payload.Gap, canonical, payload.Confirmed)
	if len(violations) > 0 {
		in.Violated = violations
		retried, retriedProvider, retryErr := a.generateCoverLetterWithProvider(r.Context(), config, canonical, in)
		if retryErr != nil {
			a.logger.Printf("resume cover letter: anti-invention retry failed, keeping the first draft: %v", retryErr)
		} else {
			markdown, providerUsed = retried, retriedProvider
			violations = coverLetterViolations(markdown, payload.Gap, canonical, payload.Confirmed)
		}
		a.logger.Printf("[ RESUME ] cover-letter: anti-invention retry left %d unsupported claim(s)", len(violations))
	}

	warnings := make([]string, 0, len(violations))
	for _, term := range violations {
		warnings = append(warnings, "mentions_skill_not_in_resume: "+strings.ToLower(term))
	}
	requiresConfirmation := len(violations) > 0

	var id string
	if jobID != "" && !requiresConfirmation {
		id, err = a.configStore.insertCoverLetter(jobID, "", markdown)
		if err != nil {
			a.logger.Printf("insert cover letter: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, coverLetterResponse{
		ID:                   id,
		Markdown:             markdown,
		PlainText:            coverLetterPlainText(markdown),
		Warnings:             warnings,
		RequiresConfirmation: requiresConfirmation,
		ProviderUsed:         providerUsed,
	})
}
