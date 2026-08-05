package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// resumeAISystemInstruction is prefixed to every Resume Studio AI prompt
// (parse, job analysis, gap, tailoring, cover letter — Appendix E).
const resumeAISystemInstruction = `You are a resume assistant. Reply ONLY with valid JSON in the requested format — no extra text, no markdown, no comments. Never fabricate data: use only what is present in the provided material. If a field is not in the source, leave it empty ("" or []).`

// resumeParsePrompt builds the Appendix E.1 prompt that asks the AI to
// convert extracted resume text into the CanonicalResume JSON shape.
func resumeParsePrompt(text string) string {
	return fmt.Sprintf(`%s

Converta o texto de currículo abaixo no JSON com EXATAMENTE esta estrutura (mesmas chaves):
{schemaVersion, basics{name,headline,email,phone,location,links[{label,url}]},
 target{jobTitle,category,seniority}, summary, skills{hard[],soft[],tools[]},
 experience[{company,role,start,end,location,bullets[]}],
 education[{institution,degree,area,start,end]},
 projects[{name,description,url,bullets[]}],
 licenses[{name,issuer,jurisdiction,number,expires}],
 certifications[{name,issuer,year}], languages[{language,fluency}]}
Regras: use schemaVersion=2. Em basics.name, coloque SOMENTE o nome da pessoa; cidade, estado e
país pertencem a basics.location, mesmo quando a extração do PDF juntar colunas na mesma linha.
Separe licenças profissionais (por exemplo RN, medical/bar/driver licenses) de certificações.
Datas no formato "YYYY-MM" (ou "YYYY" se só houver ano; "present" para atual). NÃO invente
empresas, cargos, datas, métricas, skills ou certificações — extraia apenas o que está no texto.
Nunca invente número/validade de licença. Se não houver "summary" ou "target", deixe vazios.

TEXTO DO CURRÍCULO:
%s`, resumeAISystemInstruction, text)
}

// parseResumeToCanonical asks the configured AI provider to convert raw
// resume text into a CanonicalResume, then validates/normalizes the result
// so the rest of the pipeline (diagnose/gap/tailor/export) can rely on a
// well-formed document. It never invents data: the prompt instructs the AI
// to leave unknown fields empty, and Validate() rejects malformed shapes.
// If the AI leaves basics.name empty, a deterministic heuristic
// (guessNameFromResumeText) tries to recover it from the user's own resume
// text before giving up (RS-BUG-01); when it succeeds, the returned
// warnings slice carries "name_inferred_from_first_line".
func (a *api) parseResumeToCanonical(ctx context.Context, config appConfig, apiKey, text string) (CanonicalResume, []string, error) {
	raw, err := a.scraper.generateJSON(ctx, config, apiKey, resumeParsePrompt(text))
	if err != nil {
		return CanonicalResume{}, nil, err
	}

	return decodeResumeParseResult(raw, text)
}

func (a *api) parseResumeToCanonicalWithProvider(ctx context.Context, config appConfig, _ string, text string) (CanonicalResume, []string, string, error) {
	result, err := a.runLLMWithFallback(ctx, "resume_parse", config, resumeParsePrompt(text), a.scraper.generateJSONOnce)
	if err != nil {
		return CanonicalResume{}, nil, "", err
	}
	canonical, warnings, err := decodeResumeParseResult(result.Raw, text)
	if err != nil {
		return CanonicalResume{}, nil, "", err
	}
	return canonical, warnings, result.ProviderUsed, nil
}

func decodeResumeParseResult(raw string, text string) (CanonicalResume, []string, error) {
	canonical, err := decodeCanonicalResumeJSON(raw)
	if err != nil {
		return CanonicalResume{}, nil, fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}
	canonical = normalizeCanonical(canonical)
	var warnings []string
	if strings.TrimSpace(canonical.Basics.Name) == "" {
		if guess := guessNameFromResumeText(text); guess != "" {
			canonical.Basics.Name = guess
			warnings = append(warnings, "name_inferred_from_first_line")
		}
	}
	if name, changed := resumeNameWithoutLocation(canonical, text); changed {
		canonical.Basics.Name = name
		warnings = append(warnings, "name_location_suffix_removed")
	}
	if err := canonical.Validate(); err != nil {
		return CanonicalResume{}, nil, fmt.Errorf("invalid extracted resume: %w", err)
	}
	return canonical, warnings, nil
}

// resumeNameWithoutLocation repairs a common PDF extraction artifact where a
// right-aligned city is concatenated to the name. It requires both the name
// boundary from the source header and a matching parsed location.
func resumeNameWithoutLocation(canonical CanonicalResume, sourceText string) (string, bool) {
	nameWords := strings.Fields(canonical.Basics.Name)
	sourceName := guessNameFromResumeText(sourceText)
	sourceWords := strings.Fields(sourceName)
	if len(sourceWords) < 2 || len(nameWords) <= len(sourceWords) {
		return strings.TrimSpace(canonical.Basics.Name), false
	}
	for i := range sourceWords {
		if normalizeText(nameWords[i]) != normalizeText(sourceWords[i]) {
			return strings.TrimSpace(canonical.Basics.Name), false
		}
	}
	suffixWords := nameWords[len(sourceWords):]

	locations := []string{canonical.Basics.Location}
	for _, experience := range canonical.Experience {
		locations = append(locations, experience.Location)
	}
	for _, location := range locations {
		parts := strings.FieldsFunc(location, func(r rune) bool {
			return r == ',' || r == '|' || r == ';' || r == '•'
		})
		if len(parts) == 0 {
			continue
		}
		locality := strings.TrimSpace(parts[0])
		localityWords := strings.Fields(locality)
		if locality == "" || len(localityWords) != len(suffixWords) {
			continue
		}
		matches := true
		for i := range localityWords {
			if normalizeText(suffixWords[i]) != normalizeText(localityWords[i]) {
				matches = false
				break
			}
		}
		if matches {
			return strings.Join(sourceWords, " "), true
		}
	}
	return strings.TrimSpace(canonical.Basics.Name), false
}

// resumeSectionHeaderWords are first-line words that indicate a section header
// rather than a person's name.
var resumeSectionHeaderWords = map[string]bool{
	"resume": true, "currículo": true, "curriculo": true, "curriculum": true,
	"cv": true, "summary": true, "objective": true, "objetivo": true,
	"profile": true, "perfil": true, "contact": true, "contato": true,
	"experience": true, "experiência": true, "experiencia": true,
}

var resumeColumnWhitespace = regexp.MustCompile(`\t+| {2,}`)

// guessNameFromResumeText promotes the first plausible line of the user's own
// resume text to basics.name when the AI leaves it empty (RS-BUG-01). It only
// reuses text the user provided - it never invents data (AGENTS.md).
func guessNameFromResumeText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Only the FIRST non-empty line is considered (spec D2).
		if idx := strings.IndexAny(line, "|•;,"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if idx := resumeColumnWhitespace.FindStringIndex(line); idx != nil {
			line = strings.TrimSpace(line[:idx[0]])
		}
		if line == "" || len(line) >= 60 {
			return ""
		}
		lower := strings.ToLower(line)
		if strings.ContainsAny(line, "@0123456789") || strings.Contains(lower, "http") || strings.Contains(lower, "www.") {
			return ""
		}
		words := strings.Fields(line)
		if len(words) < 2 || len(words) > 5 {
			return ""
		}
		if resumeSectionHeaderWords[strings.TrimRight(strings.ToLower(words[0]), ":")] {
			return ""
		}
		return line
	}
	return ""
}

// stripJSONFence removes a ```json ... ``` (or bare ```) code fence some
// providers add despite being instructed to reply with raw JSON.
func stripJSONFence(raw string) string {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

func decodeCanonicalResumeJSON(raw string) (CanonicalResume, error) {
	text := stripJSONFence(raw)
	var canonical CanonicalResume
	if err := json.Unmarshal([]byte(text), &canonical); err == nil {
		return canonical, nil
	} else {
		normalized, normalizeErr := normalizeCanonicalSchemaVersionJSON(text)
		if normalizeErr != nil || normalized == text {
			return CanonicalResume{}, err
		}
		if retryErr := json.Unmarshal([]byte(normalized), &canonical); retryErr != nil {
			return CanonicalResume{}, err
		}
		return canonical, nil
	}
}

func normalizeCanonicalSchemaVersionJSON(text string) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return text, err
	}
	raw, ok := obj["schemaVersion"]
	if !ok {
		return text, nil
	}
	var numeric int
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return text, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return text, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(asString))
	if err != nil {
		return text, err
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return text, err
	}
	obj["schemaVersion"] = encoded
	normalized, err := json.Marshal(obj)
	if err != nil {
		return text, err
	}
	return string(normalized), nil
}

func resumeDocumentName(config appConfig, canonical CanonicalResume) string {
	if name := strings.TrimSpace(canonical.Basics.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(config.Form.ResumeName); name != "" {
		return name
	}
	return "Resume"
}

func (a *api) resumeParse(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload resumeParseRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}

	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for resume parse: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}

	text := strings.TrimSpace(payload.Text)
	if text == "" {
		text = strings.TrimSpace(config.Form.ResumeText)
	}
	if text == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "empty_resume_text", "No resume text provided. Paste your resume or upload a file first."))
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

	aiStart := time.Now()
	canonical, warnings, providerUsed, err := a.parseResumeToCanonicalWithProvider(r.Context(), config, apiKey, text)
	a.logger.Printf("[ RESUME ] parse: AI call took %.1fs (err=%v)", time.Since(aiStart).Seconds(), err != nil)
	if err != nil {
		a.logger.Printf("resume parse: %v", err)
		writeResumeError(w, classifyResumeError(err))
		return
	}

	docID, err := a.configStore.saveResumeDocument(canonical, resumeDocumentName(config, canonical), config.Form.ResumeName, "")
	if err != nil {
		a.logger.Printf("save resume document: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}

	writeJSON(w, http.StatusOK, resumeParseResponse{DocumentID: docID, Canonical: canonical, Warnings: warnings, ProviderUsed: providerUsed})
}

// gapAnalysisPrompt builds the Appendix E.3 prompt that classifies each job
// requirement against the base resume as found/partial/missing/toConfirm.
func gapAnalysisPrompt(canonicalJSON, requirementsJSON string) string {
	return fmt.Sprintf(`%s

Compare o CURRÍCULO (JSON canonical) com os REQUISITOS da vaga (JSON). Classifique cada
requisito e devolva o JSON:
{found[{term,evidence}], partial[{term,evidence}], missing[{term,evidence}], toConfirm[{term,evidence}]}
Definições:
- found: o termo aparece claramente no currículo (evidence = trecho exato do currículo).
- partial: aparece em skills mas não é demonstrado em experiência, ou vice-versa (evidence = onde).
- missing: não aparece de forma alguma (evidence = "").
- toConfirm: o currículo sugere algo próximo, mas afirmar posse seria arriscado; peça
  confirmação (evidence = por que é dúbio).
REGRA CRÍTICA: só use "found"/"partial" se houver evidência textual real no currículo. Na
dúvida, use "toConfirm" ou "missing". Nunca afirme experiência que o currículo não mostra.
Todo item de hardRequirements deve aparecer em exatamente um dos quatro buckets; não omita
requisitos obrigatórios, mesmo quando não houver evidência no currículo.
Cada "evidence" deve ter no máximo 15 palavras — cite o trecho, não a seção inteira.

CURRÍCULO:
%s
REQUISITOS:
%s`, resumeAISystemInstruction, canonicalJSON, requirementsJSON)
}

// gapAnalysis asks the AI to classify job requirements against the resume,
// then re-verifies every "found"/"partial" claim deterministically against
// the resume's own text (enforceGapEvidenceGate) — the anti-invention gate
// applies both in the prompt and in code, per the Global Constraints.
func (a *api) gapAnalysis(ctx context.Context, config appConfig, apiKey string, base CanonicalResume, req jobRequirements) (gapResult, error) {
	canonicalJSON, err := json.Marshal(base)
	if err != nil {
		return gapResult{}, err
	}
	requirementsJSON, err := json.Marshal(req)
	if err != nil {
		return gapResult{}, err
	}

	raw, err := a.scraper.generateJSON(ctx, config, apiKey, gapAnalysisPrompt(string(canonicalJSON), string(requirementsJSON)))
	if err != nil {
		return gapResult{}, err
	}

	gap, err := decodeGapAnalysisResult(base, raw)
	if err != nil {
		return gapResult{}, err
	}
	return ensureGapHardRequirementCoverage(req, gap), nil
}

func (a *api) gapAnalysisWithProvider(ctx context.Context, config appConfig, _ string, base CanonicalResume, req jobRequirements) (gapResult, string, error) {
	canonicalJSON, err := json.Marshal(base)
	if err != nil {
		return gapResult{}, "", err
	}
	requirementsJSON, err := json.Marshal(req)
	if err != nil {
		return gapResult{}, "", err
	}

	result, err := a.runLLMWithFallback(ctx, "resume_gap", config, gapAnalysisPrompt(string(canonicalJSON), string(requirementsJSON)), a.scraper.generateJSONOnce)
	if err != nil {
		return gapResult{}, "", err
	}
	gap, err := decodeGapAnalysisResult(base, result.Raw)
	if err != nil {
		return gapResult{}, "", err
	}
	return ensureGapHardRequirementCoverage(req, gap), result.ProviderUsed, nil
}

func decodeGapAnalysisResult(base CanonicalResume, raw string) (gapResult, error) {
	var gap gapResult
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &gap); err != nil {
		return gapResult{}, fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}
	return enforceGapEvidenceGate(base, gap), nil
}

// enforceGapEvidenceGate is the deterministic half of the anti-invention
// gate for gap analysis: any "found"/"partial" item whose term (or claimed
// evidence) cannot actually be located in the base resume's text is
// downgraded to "toConfirm", regardless of what the AI asserted.
func enforceGapEvidenceGate(base CanonicalResume, gap gapResult) gapResult {
	found := make([]gapItem, 0, len(gap.Found))
	partial := make([]gapItem, 0, len(gap.Partial))
	downgraded := make([]gapItem, 0)

	for _, item := range gap.Found {
		if hasRealEvidence(base, nil, item) {
			found = append(found, item)
		} else {
			downgraded = append(downgraded, item)
		}
	}
	for _, item := range gap.Partial {
		if hasRealEvidence(base, nil, item) {
			partial = append(partial, item)
		} else {
			downgraded = append(downgraded, item)
		}
	}

	gap.Found = found
	gap.Partial = partial
	gap.Missing = nonNilGapItems(gap.Missing)
	gap.ToConfirm = append(nonNilGapItems(gap.ToConfirm), downgraded...)
	return dedupeGapBuckets(gap)
}

var gapCoverageQualifiers = map[string]bool{
	"active": true, "current": true, "automation": true, "production": true,
	"experience": true, "expertise": true, "certification": true,
	"certifications": true, "charter": true, "compliance": true,
	"ownership": true, "reporting": true,
}

func gapCoverageRepresentations(term string) map[string]bool {
	tokens := significantTerms(term)
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if gapCoverageQualifiers[token] {
			continue
		}
		if len(token) > 4 && strings.HasSuffix(token, "s") {
			token = strings.TrimSuffix(token, "s")
		}
		filtered = append(filtered, token)
	}

	representations := map[string]bool{}
	if len(filtered) == 0 {
		return representations
	}
	representations[strings.Join(filtered, " ")] = true
	if len(filtered) > 1 {
		var acronym strings.Builder
		for _, token := range filtered {
			acronym.WriteByte(token[0])
		}
		representations[acronym.String()] = true
		var prefixAcronym strings.Builder
		for _, token := range filtered[:len(filtered)-1] {
			prefixAcronym.WriteByte(token[0])
		}
		if filtered[len(filtered)-1] == prefixAcronym.String() {
			representations[filtered[len(filtered)-1]] = true
		}
	}
	return representations
}

func gapRequirementEquivalent(left, right string) bool {
	leftForms := gapCoverageRepresentations(left)
	rightForms := gapCoverageRepresentations(right)
	for form := range leftForms {
		if rightForms[form] {
			return true
		}
	}
	return false
}

// ensureGapHardRequirementCoverage closes a failure mode that prompt-only
// enforcement cannot: a model may silently omit one of the input requirements
// from every bucket. An unclassified hard requirement has no resume evidence or
// user confirmation, so Missing is the only conservative deterministic result.
func ensureGapHardRequirementCoverage(req jobRequirements, gap gapResult) gapResult {
	gap = dedupeGapBuckets(gap)
	all := make([]gapItem, 0, len(gap.Found)+len(gap.Partial)+len(gap.Missing)+len(gap.ToConfirm))
	all = append(all, gap.Found...)
	all = append(all, gap.Partial...)
	all = append(all, gap.Missing...)
	all = append(all, gap.ToConfirm...)

	for _, requirement := range req.HardRequirements {
		requirement = strings.TrimSpace(requirement)
		if requirement == "" {
			continue
		}
		covered := false
		for _, item := range all {
			if gapRequirementEquivalent(requirement, item.Term) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		item := gapItem{Term: requirement}
		gap.Missing = append(gap.Missing, item)
		all = append(all, item)
	}

	return dedupeGapBuckets(gap)
}

// dedupeGapBuckets makes the four classifications mutually exclusive even
// when the model emits the same requirement more than once or contradicts
// itself across buckets. Evidence-backed Found/Partial wins; between the two
// unsupported outcomes, Missing wins over ToConfirm because it is the safer
// action and cannot invite confirmation for a term the same response blocked.
func dedupeGapBuckets(gap gapResult) gapResult {
	seen := make([]string, 0)
	dedupe := func(items []gapItem) []gapItem {
		out := make([]gapItem, 0, len(items))
		for _, item := range items {
			term := strings.TrimSpace(item.Term)
			duplicate := false
			if term != "" {
				for _, prior := range seen {
					if gapRequirementEquivalent(prior, term) {
						duplicate = true
						break
					}
				}
			}
			if duplicate {
				continue
			}
			if term != "" {
				seen = append(seen, term)
			}
			out = append(out, item)
		}
		return out
	}

	gap.Found = dedupe(nonNilGapItems(gap.Found))
	gap.Partial = dedupe(nonNilGapItems(gap.Partial))
	gap.Missing = dedupe(nonNilGapItems(gap.Missing))
	gap.ToConfirm = dedupe(nonNilGapItems(gap.ToConfirm))
	return gap
}

// nonNilGapItems guarantees a non-nil slice so gapResult always serializes
// its arrays as JSON `[]` instead of `null` — the AI can omit a bucket or
// return JSON null for "nothing to report here", and the frontend spreads
// these arrays without a null guard (JobGapAnalysis.tsx).
func nonNilGapItems(items []gapItem) []gapItem {
	if items != nil {
		return items
	}
	return []gapItem{}
}

// shortTermMaxTokens is the boundary between an atomic skill/tool/technology
// term (matched generously, token-by-token) and a full requirement sentence
// (held to the stricter whole-phrase/evidence check). Loosening long sentences
// is where false "found" claims would slip in, so only short terms get the
// scattered-token match.
const shortTermMaxTokens = 3

// evidenceStopwords are connective words ignored when counting/matching the
// significant tokens of a short term (EN + PT), so "cloud infrastructure" and
// "experience with docker" reduce to their meaningful skill tokens.
var evidenceStopwords = map[string]bool{
	"of": true, "in": true, "the": true, "with": true, "and": true, "a": true,
	"an": true, "for": true, "to": true, "on": true, "or": true, "at": true,
	"as": true, "by": true,
	"de": true, "da": true, "do": true, "com": true, "e": true, "para": true,
	"em": true, "os": true, "no": true, "na": true, "um": true, "uma": true,
}

// significantTerms splits a term into its meaningful tokens (normalized,
// stopwords and 1-char tokens dropped), used to decide whether a term is
// "short" and to match it token-by-token.
func significantTerms(term string) []string {
	fields := strings.Fields(normalizeText(term))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,;:()[]{}")
		if len(f) < 2 || evidenceStopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func hasRealEvidence(base CanonicalResume, confirmed []string, item gapItem) bool {
	if _, source, ok := resumeEvidence(base, confirmed, item.Term); ok && resumeEvidenceSupportsClaim(item.Term, source) {
		return true
	}
	if evidence := strings.TrimSpace(string(item.Evidence)); evidence != "" {
		if _, source, ok := resumeEvidence(base, confirmed, evidence); ok && resumeEvidenceSupportsClaim(item.Term, source) {
			return true
		}
	}
	return false
}

func (a *api) resumeGap(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload gapRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	canonical := normalizeCanonical(payload.Canonical)

	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for resume gap: %v", err)
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

	aiStart := time.Now()
	gap, providerUsed, err := a.gapAnalysisWithProvider(r.Context(), config, apiKey, canonical, payload.Requirements)
	a.logger.Printf("[ RESUME ] gap: AI call took %.1fs (err=%v)", time.Since(aiStart).Seconds(), err != nil)
	if err != nil {
		a.logger.Printf("resume gap: %v", err)
		writeResumeError(w, classifyResumeError(err))
		return
	}

	writeJSON(w, http.StatusOK, gapResponse{Gap: gap, ProviderUsed: providerUsed})
}

// Output budget for the tailoring patch set. This is the slowest AI call in
// the app and its cost is dominated by what it emits, not what it reads: an
// unbounded op list, each carrying a rewritten bullet plus a free-text reason,
// was routinely large enough to outlive the request. Both numbers are prompt
// guidance, not a hard gate — decodeTailorResult still validates every op.
const (
	maxTailorPatchOps    = 25
	maxTailorReasonWords = 10
)

// tailorResumePrompt builds the Appendix E.4 prompt asking the AI to
// optimize the resume for the job as a list of RFC 6902 JSON Patch
// operations (each with a "reason"), respecting the anti-invention rule
// and the confirmed-skills list.
func tailorResumePrompt(canonicalJSON, requirementsJSON, confirmedJSON, language, voice string) string {
	return fmt.Sprintf(`%s

Otimize o CURRÍCULO (JSON canonical) para a vaga (REQUISITOS), respeitando as SKILLS
CONFIRMADAS pelo usuário. Devolva SOMENTE uma lista JSON de operações RFC 6902:
[{op, path, value, reason}]  // op ∈ "replace"|"add"|"remove"; path = JSON Pointer; reason = motivo curto
ORÇAMENTO DE SAÍDA: no máximo %d operações, priorizando as de maior impacto para esta vaga.
Cada "reason" deve ter no máximo %d palavras — é um rótulo curto para o usuário, não um parágrafo.
Use JSON Pointer paths EXACTLY as in the schema: "/summary" is top-level (NEVER "/basics/summary");
basics fields live under "/basics/..." (name, headline, email, phone, location, links); skills under
"/skills/hard|soft|tools"; experience bullets under "/experience/<index>/bullets/<index>".
O que pode fazer: reescrever "summary"; melhorar "bullets" de experiência (verbos de ação,
clareza, keywords já presentes); adicionar uma skill já presente ou confirmada; remover uma skill
ou uma experiência/formação irrelevante.
USE OPERAÇÕES INDIVIDUAIS, uma por mudança — NUNCA reescreva um array inteiro:
- para remover uma vaga/formação irrelevante, emita UMA operação {"op":"remove","path":"/experience/<index>"}
  (ou "/education/<index>"), sem tocar nas demais;
- para remover ou adicionar uma skill, use {"op":"remove","path":"/skills/hard/<index>"} ou
  {"op":"add","path":"/skills/hard/-","value":"<skill já presente/confirmada>"};
- para melhorar um bullet, {"op":"replace","path":"/experience/<i>/bullets/<j>","value":"..."}.
Não emita um replace do array "/experience", "/education" ou "/skills/hard|soft|tools" inteiro.
PROIBIDO (viola a regra de autenticidade): adicionar skill/ferramenta/certificação/experiência
que não esteja no currículo base nem na lista de confirmadas; inventar métricas, datas,
empresas ou cargos; alterar datas/empresas/cargos reais.
Regras de escrita dos bullets: começar com verbo de ação (ajustado à voz abaixo); quantificar
quando o dado existir; 1–2 linhas; foco em conquista, não tarefa; keywords naturais.
VOZ NARRATIVA (summary/bullets): escreva em %s.
IDIOMA DO TEXTO GERADO (summary/bullets): escreva em %s. Se o currículo estiver em
outro idioma, traduza o texto reescrito para %s, mantendo nomes próprios de empresas.
SKILLS CONFIRMADAS: %s

CURRÍCULO:
%s
REQUISITOS:
%s`, resumeAISystemInstruction, maxTailorPatchOps, maxTailorReasonWords, voice, language, language, confirmedJSON, canonicalJSON, requirementsJSON)
}

// resolveResumeLanguage turns the request's language selector ("en"
// default | "pt" | "es" | "auto") into the English name injected as
// {LANGUAGE} in AI prompts. "auto" is meant to follow the job description's
// language (Global Constraints); since optimizeRequest only carries the
// already-extracted jobRequirements (not the raw description text),
// detection runs over its text fields instead, falling back to English as
// specified.
func resolveResumeLanguage(language string, requirements jobRequirements) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "pt", "pt-br", "portuguese", "português":
		return "Portuguese"
	case "es", "spanish", "español":
		return "Spanish"
	case "auto":
		haystack := strings.Join([]string{
			requirements.JobTitle,
			requirements.Category,
			strings.Join(requirements.HardRequirements, " "),
			strings.Join(requirements.NiceToHave, " "),
			strings.Join(requirements.ATSKeywords, " "),
		}, " ")
		if looksPortuguese(haystack) {
			return "Portuguese"
		}
		if looksSpanish(haystack) {
			return "Spanish"
		}
		return "English"
	default:
		return "English"
	}
}

// resolveResumeVoice turns the request's voice selector ("first"|"third",
// default "third") into the instruction injected into the tailoring prompt
// (CH-06b). Third person is the default because most ATS-safe resumes drop
// first-person pronouns; first person is an explicit opt-in.
func resolveResumeVoice(voice string) string {
	if strings.EqualFold(strings.TrimSpace(voice), "first") {
		return "first person (I/my)"
	}
	return "third person (no first-person pronouns)"
}

// looksPortuguese is a lightweight heuristic (accented PT words + common
// stopwords) — good enough to steer "auto" language detection without a
// full language-detection dependency.
func looksPortuguese(text string) bool {
	lower := strings.ToLower(text)
	markers := []string{
		"não", "ção", "vaga", "responsabilidade", "experiência", "conhecimento",
		"desenvolv", "atuação", "requisitos", "diferenciais", "obrigatório",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// --- Anti-invention gate for tailoring patches ---
//
// The prompt above already instructs the AI never to add unconfirmed
// skills/certifications/experience or touch real dates/companies/roles.
// This section is the deterministic, code-level reinforcement the Global
// Constraints require: every proposed operation is checked against the
// base resume + the user-confirmed terms before being applied, and
// anything that fails is moved to "rejected" instead of silently dropped.

var (
	pathExperienceOrEducationItem = regexp.MustCompile(`^/(experience|education)/(-|\d+)$`)
	pathProtectedFactualLeaf      = regexp.MustCompile(`^/(experience|education)/\d+/(company|role|institution|degree|start|end)$`)
	pathSkillsWholeArray          = regexp.MustCompile(`^/skills/(hard|soft|tools)$`)
	pathSkillsItem                = regexp.MustCompile(`^/skills/(hard|soft|tools)/(-|\d+)$`)
	pathCertificationItem         = regexp.MustCompile(`^/certifications/(-|\d+)$`)
	pathCertificationsWholeArray  = regexp.MustCompile(`^/certifications$`)
	pathLicenseItem               = regexp.MustCompile(`^/licenses/(-|\d+)$`)
	pathLicensesWholeArray        = regexp.MustCompile(`^/licenses$`)
	pathLicenseProtectedLeaf      = regexp.MustCompile(`^/licenses/\d+/(name|issuer|jurisdiction|number|expires)$`)
	pathProjectsWholeArray        = regexp.MustCompile(`^/projects$`)
	pathProjectItem               = regexp.MustCompile(`^/projects/(-|\d+)$`)
	pathProjectProtectedLeaf      = regexp.MustCompile(`^/projects/\d+/(name|url)$`)
	pathLanguagesWholeArray       = regexp.MustCompile(`^/languages$`)
	pathLanguageItem              = regexp.MustCompile(`^/languages/(-|\d+)$`)
	pathLanguageProtectedLeaf     = regexp.MustCompile(`^/languages/\d+/(language|fluency)$`)
)

// identityKeyed lets validateArrayReplacePreservesIdentities work for both
// ResumeExperience and ResumeEducation: the same "reorder yes, fabricate/
// alter no" rule applies to both arrays.
type identityKeyed interface {
	identityKey() string
}

func (e ResumeExperience) identityKey() string {
	return normalizeText(e.Company) + "|" + normalizeText(e.Role) + "|" + normalizeText(e.Start) + "|" + normalizeText(e.End)
}

func (e ResumeEducation) identityKey() string {
	return normalizeText(e.Institution) + "|" + normalizeText(e.Degree) + "|" + normalizeText(e.Start) + "|" + normalizeText(e.End)
}

// A project's identity is its name+url; description/bullets are tailoring
// prose and may change (same split as experience: identity fixed, prose free).
func (p ResumeProject) identityKey() string {
	return normalizeText(p.Name) + "|" + normalizeText(p.URL)
}

// validateArrayReplacePreservesIdentities allows a whole-array replace of
// experience/education (used to reorder entries by relevance) only when
// every entry in the new array matches — by company/role/dates or
// institution/degree/dates — an entry already present in the original
// array. Bullets may differ (that's the point of tailoring); the
// company/role/institution/degree/dates identity may not.
func validateArrayReplacePreservesIdentities[T identityKeyed](original []T, raw json.RawMessage) (bool, string) {
	var updated []T
	if err := json.Unmarshal(raw, &updated); err != nil {
		return false, "invalid replacement payload"
	}
	if len(updated) != len(original) {
		return false, "cannot add or remove entries via a whole-array replace"
	}
	seen := make(map[string]bool, len(original))
	for _, o := range original {
		seen[o.identityKey()] = true
	}
	for _, u := range updated {
		if !seen[u.identityKey()] {
			return false, "cannot introduce a new/altered entry via a whole-array replace"
		}
	}
	return true, ""
}

func skillsForCategory(base CanonicalResume, category string) []string {
	switch category {
	case "hard":
		return base.Skills.Hard
	case "soft":
		return base.Skills.Soft
	case "tools":
		return base.Skills.Tools
	default:
		return nil
	}
}

// skillAllowed permits a skill only if it is already present in that
// category, appears anywhere in the resume's own text, or was explicitly
// confirmed by the user (the gap-analysis "confirm before adding" flow,
// including confirmations persisted onto the resume as ConfirmedSkills).
func skillAllowed(base CanonicalResume, confirmed []string, skill string, original []string) bool {
	for _, o := range original {
		if strings.EqualFold(normalizeText(o), normalizeText(skill)) {
			return true
		}
	}
	_, source, ok := resumeEvidence(base, confirmed, skill)
	return ok && resumeEvidenceSupportsClaim(skill, source)
}

// confirmedContains reports whether a term was confirmed by the user, checking
// both the request's confirmed list and the durable ConfirmedSkills persisted
// on the resume, with tolerant matching so a formatting variation still counts.
func confirmedContains(base CanonicalResume, confirmed []string, term string) bool {
	_, source, ok := resumeEvidence(base, confirmed, term)
	return ok && source == resumeEvidenceConfirmed
}

// certificationAllowed permits a certification only if a certification with
// the same name already exists on the base resume or the user explicitly
// confirmed it. Unlike skills, a certification is never inferred from the
// resume's free text — it is a stronger, verifiable claim, so it requires
// an exact base match or an explicit confirmation.
func certificationAllowed(base CanonicalResume, confirmed []string, name string) bool {
	_, source, ok := resumeEvidence(base, confirmed, name)
	return ok && (source == resumeEvidenceCertification || source == resumeEvidenceLicense || source == resumeEvidenceConfirmed)
}

func licenseAllowed(base CanonicalResume, confirmed []string, license ResumeLicense) bool {
	for _, existing := range base.Licenses {
		if !resumeEvidenceMatches(existing.Name, license.Name) && !resumeEvidenceMatches(license.Name, existing.Name) {
			continue
		}
		return sameOrEmpty(license.Issuer, existing.Issuer) &&
			sameOrEmpty(license.Jurisdiction, existing.Jurisdiction) &&
			sameOrEmpty(license.Number, existing.Number) &&
			sameOrEmpty(license.Expires, existing.Expires)
	}
	_, source, ok := resumeEvidence(base, confirmed, license.Name)
	if !ok || (source != resumeEvidenceCertification && source != resumeEvidenceConfirmed) {
		return false
	}
	return strings.TrimSpace(license.Issuer) == "" && strings.TrimSpace(license.Jurisdiction) == "" &&
		strings.TrimSpace(license.Number) == "" && strings.TrimSpace(license.Expires) == ""
}

func sameOrEmpty(updated, original string) bool {
	updated = normalizeText(strings.TrimSpace(updated))
	return updated == "" || updated == normalizeText(strings.TrimSpace(original))
}

// languageAllowed permits a language entry only if the base resume already
// lists the same language with the same fluency (reorder/reformat), or the
// user explicitly confirmed the language. Upgrading the fluency of a real
// language is a new claim, so it requires confirmation too.
func languageAllowed(base CanonicalResume, confirmed []string, lang ResumeLanguage) bool {
	for _, existing := range base.Languages {
		if strings.EqualFold(normalizeText(existing.Language), normalizeText(lang.Language)) &&
			strings.EqualFold(normalizeText(existing.Fluency), normalizeText(lang.Fluency)) {
			return true
		}
	}
	return confirmedContains(base, confirmed, lang.Language)
}

// validateTailoringPatch is the deterministic anti-invention gate: it
// inspects a single proposed JSON Patch operation and decides whether it
// may touch the base resume's authoritative facts (company/role/dates/
// institution/degree) or introduce new skills/certifications/experience
// that the user never confirmed having.
func validateTailoringPatch(base CanonicalResume, confirmed []string, op jsonPatchOp) (bool, string) {
	// Only add/remove/replace are supported. jsonPatchOp carries no "from"
	// field, so a move/copy that slipped through here would reach evanphx
	// without its source pointer and fail the ENTIRE batch — rejecting it
	// up front keeps the rest of the suggestions alive.
	switch strings.ToLower(strings.TrimSpace(op.Op)) {
	case "add", "remove", "replace":
	default:
		return false, fmt.Sprintf("unsupported patch operation %q — only add/remove/replace are allowed", op.Op)
	}
	switch {
	case op.Path == "/experience" && (op.Op == "replace" || op.Op == "add"):
		return validateArrayReplacePreservesIdentities(base.Experience, op.Value)
	case op.Path == "/education" && (op.Op == "replace" || op.Op == "add"):
		return validateArrayReplacePreservesIdentities(base.Education, op.Value)
	case pathExperienceOrEducationItem.MatchString(op.Path):
		if op.Op == "remove" {
			return true, ""
		}
		return false, "cannot add or replace a whole experience/education entry directly"
	case pathProtectedFactualLeaf.MatchString(op.Path):
		return false, "cannot modify company/role/dates/institution/degree — they must stay real"
	case pathSkillsWholeArray.MatchString(op.Path) && (op.Op == "replace" || op.Op == "add"):
		m := pathSkillsWholeArray.FindStringSubmatch(op.Path)
		var newSkills []string
		if err := json.Unmarshal(op.Value, &newSkills); err != nil {
			return false, "invalid skills payload"
		}
		original := skillsForCategory(base, m[1])
		for _, skill := range newSkills {
			if !skillAllowed(base, confirmed, skill, original) {
				return false, fmt.Sprintf("cannot add unconfirmed skill %q", skill)
			}
		}
		return true, ""
	case pathSkillsItem.MatchString(op.Path) && (op.Op == "add" || op.Op == "replace"):
		m := pathSkillsItem.FindStringSubmatch(op.Path)
		var skill string
		if err := json.Unmarshal(op.Value, &skill); err != nil {
			return false, "invalid skill value"
		}
		if !skillAllowed(base, confirmed, skill, skillsForCategory(base, m[1])) {
			return false, fmt.Sprintf("cannot add unconfirmed skill %q", skill)
		}
		return true, ""
	case pathCertificationItem.MatchString(op.Path) && (op.Op == "add" || op.Op == "replace"):
		var cert ResumeCertification
		if err := json.Unmarshal(op.Value, &cert); err != nil {
			return false, "invalid certification payload"
		}
		if !certificationAllowed(base, confirmed, cert.Name) {
			return false, fmt.Sprintf("cannot add unconfirmed certification %q", cert.Name)
		}
		return true, ""
	case pathCertificationsWholeArray.MatchString(op.Path) && (op.Op == "replace" || op.Op == "add"):
		var certs []ResumeCertification
		if err := json.Unmarshal(op.Value, &certs); err != nil {
			return false, "invalid certifications payload"
		}
		for _, cert := range certs {
			if !certificationAllowed(base, confirmed, cert.Name) {
				return false, fmt.Sprintf("cannot add unconfirmed certification %q", cert.Name)
			}
		}
		return true, ""
	case pathLicenseItem.MatchString(op.Path) && (op.Op == "add" || op.Op == "replace"):
		var license ResumeLicense
		if err := json.Unmarshal(op.Value, &license); err != nil {
			return false, "invalid license payload"
		}
		if !licenseAllowed(base, confirmed, license) {
			return false, fmt.Sprintf("cannot add unconfirmed license %q", license.Name)
		}
		return true, ""
	case pathLicensesWholeArray.MatchString(op.Path) && (op.Op == "replace" || op.Op == "add"):
		var licenses []ResumeLicense
		if err := json.Unmarshal(op.Value, &licenses); err != nil {
			return false, "invalid licenses payload"
		}
		for _, license := range licenses {
			if !licenseAllowed(base, confirmed, license) {
				return false, fmt.Sprintf("cannot add unconfirmed license %q", license.Name)
			}
		}
		return true, ""
	case pathLicenseProtectedLeaf.MatchString(op.Path):
		return false, "cannot modify license facts directly — they must stay exactly as provided"
	case pathProjectsWholeArray.MatchString(op.Path) && (op.Op == "replace" || op.Op == "add"):
		return validateArrayReplacePreservesIdentities(base.Projects, op.Value)
	case pathProjectItem.MatchString(op.Path):
		if op.Op == "remove" {
			return true, ""
		}
		return false, "cannot add or replace a whole project entry directly"
	case pathProjectProtectedLeaf.MatchString(op.Path):
		return false, "cannot modify a project's name/url — they must stay real"
	case pathLanguagesWholeArray.MatchString(op.Path) && (op.Op == "replace" || op.Op == "add"):
		var langs []ResumeLanguage
		if err := json.Unmarshal(op.Value, &langs); err != nil {
			return false, "invalid languages payload"
		}
		for _, lang := range langs {
			if !languageAllowed(base, confirmed, lang) {
				return false, fmt.Sprintf("cannot add unconfirmed language %q", lang.Language)
			}
		}
		return true, ""
	case pathLanguageItem.MatchString(op.Path):
		if op.Op == "remove" {
			return true, ""
		}
		var lang ResumeLanguage
		if err := json.Unmarshal(op.Value, &lang); err != nil {
			return false, "invalid language payload"
		}
		if !languageAllowed(base, confirmed, lang) {
			return false, fmt.Sprintf("cannot add unconfirmed language %q", lang.Language)
		}
		return true, ""
	case pathLanguageProtectedLeaf.MatchString(op.Path):
		return false, "cannot modify a language/fluency claim — confirm it via gap analysis instead"
	default:
		return true, ""
	}
}

// tailorResume asks the AI for a tailoring patch set, then splits it into
// gate-approved and gate-rejected operations. Rejected operations are never
// applied — they are reported back so the UI can show *why* a suggestion
// was blocked instead of silently dropping it.
func (a *api) tailorResume(ctx context.Context, config appConfig, apiKey string, base CanonicalResume, req jobRequirements, confirmed []string, language, voice string) (tailorResult, error) {
	canonicalJSON, err := json.Marshal(base)
	if err != nil {
		return tailorResult{}, err
	}
	requirementsJSON, err := json.Marshal(req)
	if err != nil {
		return tailorResult{}, err
	}
	confirmedJSON, err := json.Marshal(confirmed)
	if err != nil {
		return tailorResult{}, err
	}

	resolvedLanguage := resolveResumeLanguage(language, req)
	resolvedVoice := resolveResumeVoice(voice)
	raw, err := a.scraper.generateJSON(ctx, config, apiKey, tailorResumePrompt(string(canonicalJSON), string(requirementsJSON), string(confirmedJSON), resolvedLanguage, resolvedVoice))
	if err != nil {
		return tailorResult{}, err
	}

	return decodeTailorResult(base, req, confirmed, raw)
}

func (a *api) tailorResumeWithProvider(ctx context.Context, config appConfig, _ string, base CanonicalResume, req jobRequirements, confirmed []string, language, voice string) (tailorResult, string, error) {
	canonicalJSON, err := json.Marshal(base)
	if err != nil {
		return tailorResult{}, "", err
	}
	requirementsJSON, err := json.Marshal(req)
	if err != nil {
		return tailorResult{}, "", err
	}
	confirmedJSON, err := json.Marshal(confirmed)
	if err != nil {
		return tailorResult{}, "", err
	}

	resolvedLanguage := resolveResumeLanguage(language, req)
	resolvedVoice := resolveResumeVoice(voice)
	result, err := a.runLLMWithFallback(ctx, "resume_optimize", config, tailorResumePrompt(string(canonicalJSON), string(requirementsJSON), string(confirmedJSON), resolvedLanguage, resolvedVoice), a.scraper.generateJSONOnce)
	if err != nil {
		return tailorResult{}, "", err
	}
	tailored, err := decodeTailorResult(base, req, confirmed, result.Raw)
	if err != nil {
		return tailorResult{}, "", err
	}
	return tailored, result.ProviderUsed, nil
}

func decodeTailorResult(base CanonicalResume, req jobRequirements, confirmed []string, raw string) (tailorResult, error) {
	var patches []jsonPatchOp
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &patches); err != nil {
		return tailorResult{}, fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}

	var accepted, rejected []jsonPatchOp
	for _, op := range patches {
		if ok, reason := validateTailoringPatch(base, confirmed, op); ok {
			accepted = append(accepted, op)
		} else {
			op.Reason = strings.TrimSpace(op.Reason + " [rejected: " + reason + "]")
			rejected = append(rejected, op)
		}
	}
	return applyReviewRiskToTailorResult(base, req, confirmed, tailorResult{Patches: accepted, Rejected: rejected}), nil
}

// normalizeTailoringPatchPath fixes common AI mistakes in JSON Pointer paths
// before patches are validated/applied (e.g. summary is top-level, not under basics).
func normalizeTailoringPatchPath(path string) string {
	switch strings.TrimSpace(path) {
	case "/basics/summary":
		return "/summary"
	default:
		return path
	}
}

// jsonPointerExists reports whether a RFC 6901 pointer resolves on the given JSON document.
func jsonPointerExists(doc []byte, pointer string) bool {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" {
		return true
	}
	if pointer == "/" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}

	tokens := strings.Split(pointer[1:], "/")
	for i := range tokens {
		tokens[i] = strings.ReplaceAll(tokens[i], "~1", "/")
		tokens[i] = strings.ReplaceAll(tokens[i], "~0", "~")
	}

	var current any
	if err := json.Unmarshal(doc, &current); err != nil {
		return false
	}

	for _, token := range tokens {
		if token == "-" {
			return false
		}
		switch node := current.(type) {
		case map[string]any:
			child, ok := node[token]
			if !ok {
				return false
			}
			current = child
		case []any:
			idx, err := strconv.Atoi(token)
			if err != nil || idx < 0 || idx >= len(node) {
				return false
			}
			current = node[idx]
		default:
			return false
		}
	}
	return true
}

// prepareTailoringPatches normalizes AI patch paths and upgrades replace→add when the
// target key is absent, so a bad pointer from the model does not 502 the whole optimize flow.
func prepareTailoringPatches(baseJSON []byte, ops []jsonPatchOp) []jsonPatchOp {
	if len(ops) == 0 {
		return ops
	}
	prepared := make([]jsonPatchOp, len(ops))
	for i, op := range ops {
		op.Path = normalizeTailoringPatchPath(op.Path)
		if strings.EqualFold(op.Op, "replace") && !jsonPointerExists(baseJSON, op.Path) {
			op.Op = "add"
		}
		prepared[i] = op
	}
	return prepared
}

// applyPatches applies the accepted JSON Patch operations on top of the
// base resume using RFC 6902 semantics (github.com/evanphx/json-patch/v5),
// producing the tailored preview. The returned patch list is the normalized
// version actually applied (path aliases, replace→add upgrades).
func applyPatches(base CanonicalResume, ops []jsonPatchOp) (CanonicalResume, []jsonPatchOp, error) {
	if len(ops) == 0 {
		return base, nil, nil
	}

	baseJSON, err := json.Marshal(base)
	if err != nil {
		return CanonicalResume{}, nil, err
	}
	prepared := prepareTailoringPatches(baseJSON, ops)
	patchJSON, err := json.Marshal(prepared)
	if err != nil {
		return CanonicalResume{}, nil, err
	}
	patch, err := jsonpatch.DecodePatch(patchJSON)
	if err != nil {
		return CanonicalResume{}, nil, fmt.Errorf("patch inválido: %w", err)
	}
	patchedJSON, err := patch.Apply(baseJSON)
	if err != nil {
		return CanonicalResume{}, nil, fmt.Errorf("falha ao aplicar patch: %w", err)
	}

	var patched CanonicalResume
	if err := json.Unmarshal(patchedJSON, &patched); err != nil {
		return CanonicalResume{}, nil, err
	}
	return normalizeCanonical(patched), prepared, nil
}

func (a *api) resumeOptimize(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload optimizeRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	canonical := normalizeCanonical(payload.Canonical)

	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for resume optimize: %v", err)
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

	aiStart := time.Now()
	result, providerUsed, err := a.tailorResumeWithProvider(r.Context(), config, apiKey, canonical, payload.Requirements, payload.Confirmed, payload.Language, payload.Voice)
	a.logger.Printf("[ RESUME ] optimize: AI call took %.1fs (err=%v)", time.Since(aiStart).Seconds(), err != nil)
	if err != nil {
		a.logger.Printf("resume optimize: %v", err)
		writeResumeError(w, classifyResumeError(err))
		return
	}

	preview, patches, err := applyPatches(canonical, result.Patches)
	if err != nil {
		a.logger.Printf("apply resume patches: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusBadGateway, "invalid_ai_json", "The AI suggested changes that could not be applied. Try optimizing again."))
		return
	}

	if patches == nil {
		patches = []jsonPatchOp{}
	}
	rejected := result.Rejected
	if rejected == nil {
		rejected = []jsonPatchOp{}
	}

	writeJSON(w, http.StatusOK, optimizeResponse{Patches: patches, Preview: preview, Rejected: rejected, ProviderUsed: providerUsed})
}
