package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// maxJobDescriptionPromptChars caps how much of a job description reaches the
// model. Postings routinely end in benefits copy, diversity statements and
// legal boilerplate that carry no requirements but do carry tokens — and on a
// free-tier key, tokens are the budget. The requirements live at the top.
const maxJobDescriptionPromptChars = 6000

// promptJobDescription trims a posting to the part that actually informs the
// answer. truncate() is rune-safe, so an accented character is never split.
func promptJobDescription(description string) string {
	return truncate(strings.TrimSpace(description), maxJobDescriptionPromptChars)
}

// jobRequirementsPrompt builds the Appendix E.2 prompt that asks the AI to
// extract structured requirements out of a free-text job description.
func jobRequirementsPrompt(description, category, seniority string) string {
	description = promptJobDescription(description)
	var context string
	if strings.TrimSpace(category) != "" || strings.TrimSpace(seniority) != "" {
		context = fmt.Sprintf("\nCONTEXTO (opcional): categoria=%s senioridade=%s\n",
			coalesce(strings.TrimSpace(category), "-"), coalesce(strings.TrimSpace(seniority), "-"))
	}
	return fmt.Sprintf(`%s

Analise a descrição de vaga abaixo e devolva o JSON:
{category, jobTitle, hardRequirements[], niceToHave[], seniority, atsKeywords[]}
Regras: category em minúsculas (ex.: "tech","financeiro","saude","marketing","educacao").
hardRequirements = requisitos obrigatórios; niceToHave = desejáveis; atsKeywords = termos que
um ATS provavelmente busca (siglas, ferramentas, metodologias). seniority um de:
"Estágio","Júnior","Pleno","Sênior","Especialista","Liderança". Extraia apenas do texto.%s
DESCRIÇÃO DA VAGA:
%s`, resumeAISystemInstruction, context, description)
}

// analyzeJob asks the configured AI provider to turn a job description into
// structured jobRequirements (Appendix E.2).
func (a *api) analyzeJob(ctx context.Context, config appConfig, apiKey, description, category, seniority string) (jobRequirements, error) {
	raw, err := a.scraper.generateJSON(ctx, config, apiKey, jobRequirementsPrompt(description, category, seniority))
	if err != nil {
		return jobRequirements{}, err
	}

	return decodeJobRequirements(raw)
}

func (a *api) analyzeJobWithProvider(ctx context.Context, config appConfig, _ string, description, category, seniority string) (jobRequirements, string, error) {
	result, err := a.runLLMWithFallback(ctx, "job_analyze", config, jobRequirementsPrompt(description, category, seniority), a.scraper.generateJSONOnce)
	if err != nil {
		return jobRequirements{}, "", err
	}
	requirements, err := decodeJobRequirements(result.Raw)
	if err != nil {
		return jobRequirements{}, "", err
	}
	return requirements, result.ProviderUsed, nil
}

func decodeJobRequirements(raw string) (jobRequirements, error) {
	var requirements jobRequirements
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &requirements); err != nil {
		return jobRequirements{}, fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}
	requirements.Category = strings.ToLower(strings.TrimSpace(requirements.Category))
	requirements.JobTitle = strings.TrimSpace(requirements.JobTitle)
	requirements.Seniority = strings.TrimSpace(requirements.Seniority)
	requirements.HardRequirements = trimStrings(requirements.HardRequirements)
	requirements.NiceToHave = trimStrings(requirements.NiceToHave)
	requirements.ATSKeywords = trimStrings(requirements.ATSKeywords)
	return requirements, nil
}

func (a *api) resumeAnalyzeJob(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload analyzeJobRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}

	description := strings.TrimSpace(payload.Description)
	if description == "" && strings.TrimSpace(payload.JobID) != "" {
		job, err := a.configStore.getJobByID(payload.JobID)
		if err != nil {
			writeResumeError(w, resumeErrorFor(http.StatusNotFound, "job_not_found", "Job not found."))
			return
		}
		description = strings.TrimSpace(job.Description)
	}
	if description == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "empty_job_description", "No job description provided. Paste one or pick a job first."))
		return
	}

	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for analyze-job: %v", err)
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
	requirements, providerUsed, err := a.analyzeJobWithProvider(r.Context(), config, apiKey, description, payload.Category, payload.Seniority)
	a.logger.Printf("[ RESUME ] analyze-job: AI call took %.1fs (err=%v)", time.Since(aiStart).Seconds(), err != nil)
	if err != nil {
		a.logger.Printf("analyze job: %v", err)
		writeResumeError(w, classifyResumeError(err))
		return
	}

	writeJSON(w, http.StatusOK, analyzeJobResponse{Requirements: requirements, ProviderUsed: providerUsed})
}
