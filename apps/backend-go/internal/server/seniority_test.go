package server

import "testing"

func TestSeniorityPlenoOnlyBlocksSeniorTitles(t *testing.T) {
	config := defaultConfig()
	config.Form.Levels = "Pleno"

	cases := []string{
		"Analista de Infraestrutura - DBA Sênior (Remoto)",
		"DevOps - Perfil pleno e Sr",
		"Analista de infraestrutura SR #1009724",
		"Analista de Infraestrutura Linux - Sênior",
		"Tech Lead DevOps",
	}
	for _, title := range cases {
		if reason := seniorityBlockReason(config, jobPost{Title: title}); reason == "" {
			t.Fatalf("expected %q to be blocked for Pleno-only config", title)
		}
	}

	if reason := seniorityBlockReason(config, jobPost{Title: "Analista DevOps Pleno"}); reason != "" {
		t.Fatalf("expected pleno title to pass, got %s", reason)
	}
	if reason := seniorityBlockReason(config, jobPost{Title: "Analista DevOps"}); reason != "" {
		t.Fatalf("expected neutral title to pass before description, got %s", reason)
	}
}

func TestSeniorityJuniorPlenoAllowsJuniorAndPleno(t *testing.T) {
	config := defaultConfig()
	config.Form.Levels = "Junior, Pleno"

	if reason := seniorityBlockReason(config, jobPost{Title: "DevOps Junior"}); reason != "" {
		t.Fatalf("expected junior title to pass, got %s", reason)
	}
	if reason := seniorityBlockReason(config, jobPost{Title: "DevOps Pleno"}); reason != "" {
		t.Fatalf("expected pleno title to pass, got %s", reason)
	}
	if reason := seniorityBlockReason(config, jobPost{Title: "DevOps Senior"}); reason == "" {
		t.Fatal("expected senior title to be blocked")
	}
}

func TestSeniorityTargetRoleManagerOverridesDefaultExclusion(t *testing.T) {
	config := defaultConfig()
	config.Form.Role = "Digital Marketing Manager"
	config.Form.Roles = "Digital Marketing Manager"

	if reason := seniorityBlockReason(config, jobPost{Title: "Digital Marketing Manager - B2B SaaS"}); reason != "" {
		t.Fatalf("target role must not be rejected by its own manager term, got %s", reason)
	}
	if reason := seniorityBlockReason(config, jobPost{Title: "Engineering Manager"}); reason == "" {
		t.Fatal("unrelated manager title must remain excluded")
	}
}

func TestExplicitlyAllowedManagerOverridesEquivalentExclusion(t *testing.T) {
	config := defaultConfig()
	config.Form.Role = "Digital Marketing Manager"
	config.Form.Roles = "Digital Marketing Manager"
	config.Form.Levels = "Manager, Gerente, Coordenador"

	if reason := seniorityBlockReason(config, jobPost{Title: "Marketing Manager"}); reason != "" {
		t.Fatalf("an explicitly allowed manager level must override the equivalent default exclusion, got %s", reason)
	}
}

func TestSeniorityBlockAvoidsDescriptionFalsePositive(t *testing.T) {
	config := defaultConfig()
	config.Form.Levels = "Junior, Jr, Pleno, Senior, Sr, Especialista"
	reason := seniorityBlockReason(config, jobPost{
		Title:       "Analista de Sistemas Junior",
		Description: "Principais atividades: sustentacao de sistemas e atendimento de chamados.",
	})
	if reason != "" {
		t.Fatalf("did not expect description word principal/principais to block job, got %s", reason)
	}

	reason = seniorityBlockReason(config, jobPost{Title: "Tech Lead DevOps"})
	if reason == "" {
		t.Fatal("expected strong seniority signal in title to block job")
	}
}

func TestSeniorityBlockIgnoresExcludedRoleWordsInDescription(t *testing.T) {
	config := defaultConfig()
	config.Form.Role = "Digital Marketing Manager"
	config.Form.Roles = "Digital Marketing Manager"

	job := jobPost{
		Title:       "Digital Marketing Manager - B2B SaaS",
		Description: "Own lead routing, lead scoring, and reporting with the Field Sales Manager.",
	}
	if reason := seniorityBlockReason(config, job); reason != "" {
		t.Fatalf("description role words must not be treated as the job's seniority, got %s", reason)
	}
	if reason := seniorityBlockReason(config, jobPost{Title: "Tech Lead DevOps"}); reason == "" {
		t.Fatal("excluded seniority in the title must remain blocked")
	}
}

func TestSeniorityEnfermeiroAssistencialNotBlockedByPrincipalBoilerplate(t *testing.T) {
	config := defaultConfig()
	config.Form.Levels = "Pleno"
	config.Form.ExcludedLevels = "Tech Lead, Lead, Staff, Principal, Manager"

	reason := seniorityBlockReason(config, jobPost{
		Title:       "Enfermeiro Assistencial",
		Description: "Principais atividades: assistencia direta ao paciente e apoio a equipe.",
	})
	if reason != "" {
		t.Fatalf("expected assistencial nurse job to pass, got %s", reason)
	}
}

func TestSeniorityYearsParserRequiresExperienceContext(t *testing.T) {
	config := defaultConfig()
	config.Form.Levels = "Junior, Pleno"
	config.Form.MaxYears = 8

	reason := seniorityBlockReason(config, jobPost{
		Title:       "Desenvolvedor Full-Stack Jr",
		Description: "Stack moderna com Node 20, React 18 e integracao continua. Vaga para perfil junior.",
	})
	if reason != "" {
		t.Fatalf("did not expect loose year tokens to block junior job, got %s", reason)
	}

	reason = seniorityBlockReason(config, jobPost{
		Title:       "DevOps Engineer",
		Description: "Experiencia minima de 10 anos com Kubernetes em producao.",
	})
	if reason == "" {
		t.Fatal("expected explicit experience requirement to block when above maxYears")
	}
}
